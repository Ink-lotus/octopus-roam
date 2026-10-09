package op

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

var channelRefreshFixtureID atomic.Int64

func assertRefreshKeyChanges(t *testing.T, result model.ChannelModelRefreshKeyResult, added, removed int) {
	t.Helper()
	if result.AddedCount == nil || result.RemovedCount == nil {
		t.Fatalf("key %q has no change counts", result.KeyName)
	}
	if *result.AddedCount != added || *result.RemovedCount != removed {
		t.Fatalf("key %q changes = +%d -%d, want +%d -%d", result.KeyName, *result.AddedCount, *result.RemovedCount, added, removed)
	}
}

// 检查持久化行和读侧缓存，失败探测不能删除模型、授权或手动选择。
func assertFailedRefreshPreservesState(t *testing.T, before model.ChannelDetail, group model.Group, grantID int) {
	t.Helper()
	after, err := ChannelDetailGet(before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("failed refresh changed channel cache: err=%v", err)
	}
	afterGroup, err := GroupGet(group.ID)
	if err != nil || len(afterGroup.Items) != 1 || afterGroup.ActiveItemID != group.ActiveItemID {
		t.Fatalf("failed refresh changed group cache: %+v err=%v", afterGroup, err)
	}
	var storedGroup model.Group
	if err := db.GetDB().Preload("Items").First(&storedGroup, group.ID).Error; err != nil || len(storedGroup.Items) != 1 || storedGroup.ActiveItemID != group.ActiveItemID {
		t.Fatalf("failed refresh changed stored group: %+v err=%v", storedGroup, err)
	}
	var grant model.ChannelGrant
	if err := db.GetDB().First(&grant, grantID).Error; err != nil {
		t.Fatalf("failed refresh removed stored grant: %v", err)
	}
	var storedModel model.ChannelModel
	if err := db.GetDB().First(&storedModel, grant.ChannelModelID).Error; err != nil || storedModel.Name != "old-model" {
		t.Fatalf("failed refresh changed stored model: %+v err=%v", storedModel, err)
	}
}

func TestRefreshChannelModelsRejectsMalformedSuccessResponses(t *testing.T) {
	for _, body := range []string{
		`{"error":{"message":"temporary failure"}}`, `{}`, `null`,
		`{"data":null}`, `{"data":{}}`, `{"data":[],"error":"failure"}`,
		`{"data":[{}]}`, `{"data":[{"id":" "}]}`, `{"data":[{"id":1}]}`,
		`{"data":[]} {"data":[]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := newModelListServer(t, func(_ string, _ bool) (int, string) { return http.StatusOK, body })
			id, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
			group, grantID := addRefreshGroup(t, id, keys["key"])
			before, _ := ChannelDetailGet(id)
			result, err := RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
			if err != nil || result.Success || len(result.KeyResults) != 1 || result.KeyResults[0].Success || result.KeyResults[0].Error == "" {
				t.Fatalf("malformed response accepted or diagnostics missing: %+v err=%v", result, err)
			}
			assertFailedRefreshPreservesState(t, before, group, grantID)
		})
	}
}

func TestRefreshChannelModelsEmptyClearsDisabledKeyReferences(t *testing.T) {
	server := newModelListServer(t, func(_ string, _ bool) (int, string) { return http.StatusOK, `{"data":[]}` })
	id, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "enabled", Key: "placeholder-a", Enabled: true},
		{Name: "disabled", Key: "placeholder-b", Enabled: true},
	})
	group, _ := addRefreshGroup(t, id, keys["disabled"])
	detail, _ := ChannelDetailGet(id)
	for i := range detail.Keys {
		if detail.Keys[i].Name == "disabled" {
			detail.Keys[i].Enabled = false
		}
	}
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success || !result.ZeroModels || len(result.KeyResults) != 1 {
		t.Fatalf("empty refresh: %+v err=%v", result, err)
	}
	after, _ := ChannelDetailGet(id)
	afterGroup, err := GroupGet(group.ID)
	if len(after.Models) != 0 || len(after.Grants) != 0 || err != nil || len(afterGroup.Items) != 0 || afterGroup.ActiveItemID != 0 {
		t.Fatalf("empty refresh retained old state: models=%v grants=%v group=%+v err=%v", after.Models, after.Grants, afterGroup, err)
	}
}

type deadlineCheckingTransport struct {
	base        http.RoundTripper
	sawDeadline atomic.Bool
}

func (transport *deadlineCheckingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if deadline, ok := req.Context().Deadline(); ok && time.Until(deadline) > 0 && time.Until(deadline) <= channelModelProbeTimeout {
		transport.sawDeadline.Store(true)
	}
	return transport.base.RoundTrip(req)
}

func TestRefreshChannelModelsTimeoutAndCancellationReleaseLock(t *testing.T) {
	for _, scenario := range []string{"headers", "body", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			var hanging atomic.Bool
			hanging.Store(true)
			started := make(chan struct{}, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if hanging.Load() {
					if scenario == "body" {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{"data":`))
						w.(http.Flusher).Flush()
					}
					select {
					case started <- struct{}{}:
					default:
					}
					<-r.Context().Done()
					return
				}
				_, _ = w.Write([]byte(`{"data":[]}`))
			}))
			defer server.Close()
			id, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
			group, grantID := addRefreshGroup(t, id, keys["key"])
			before, _ := ChannelDetailGet(id)
			client := newRefreshHTTPClient(t)
			transport := &deadlineCheckingTransport{base: client.Transport}
			client.Transport = transport
			// 短客户端期限加速集成测试；同时校验公共入口给所有请求设置了服务期限。
			client.Timeout = 100 * time.Millisecond
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancel" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result, err := RefreshChannelModelsWithClient(ctx, id, model.ChannelModelRefreshOptions{}, client)
			if err != nil || result.Success || len(result.KeyResults) != 1 || result.KeyResults[0].Success || result.KeyResults[0].Error == "" {
				t.Fatalf("hung refresh: %+v err=%v", result, err)
			}
			if !transport.sawDeadline.Load() {
				t.Fatal("probe has no bounded context")
			}
			assertFailedRefreshPreservesState(t, before, group, grantID)
			hanging.Store(false)
			// 移除客户端期限，确认服务自己仍给重试请求设置了期限。
			client.Timeout = 0
			transport.sawDeadline.Store(false)
			result, err = RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{}, client)
			if err != nil || !result.Success || !result.ZeroModels {
				t.Fatalf("refresh lock not released: %+v err=%v", result, err)
			}
			if !transport.sawDeadline.Load() {
				t.Fatal("probe relies on caller timeout instead of its own deadline")
			}
		})
	}
}

func TestAnthropicModelPaginationRejectsMissingOrRepeatedCursor(t *testing.T) {
	for _, cursor := range []string{"", "same-cursor"} {
		t.Run(cursor, func(t *testing.T) {
			var requests atomic.Int32
			server := newModelListServer(t, func(_ string, _ bool) (int, string) {
				requests.Add(1)
				return http.StatusOK, fmt.Sprintf(`{"data":[{"id":"new-model"}],"has_more":true,"last_id":%q}`, cursor)
			})
			id, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
			setRefreshGrantProtocols(t, id, model.ProtocolAnthropicMessage)
			group, grantID := addRefreshGroup(t, id, keys["key"])
			before, _ := ChannelDetailGet(id)
			result, err := RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
			if err != nil || result.Success || len(result.KeyResults) != 1 || !strings.Contains(result.KeyResults[0].Error, "pagination") || requests.Load() > 3 {
				t.Fatalf("invalid pagination accepted: %+v err=%v requests=%d", result, err, requests.Load())
			}
			assertFailedRefreshPreservesState(t, before, group, grantID)
		})
	}
}

type modelListResponse func(key string, anthropic bool) (int, string)

func newModelListServer(t *testing.T, respond modelListResponse) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		anthropic := r.Header.Get("X-Api-Key") != ""
		if anthropic {
			key = r.Header.Get("X-Api-Key")
		}
		status, body := respond(key, anthropic)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func newRefreshHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport}
}

func newRefreshChannel(t *testing.T, baseURL string, keys []model.ChannelKeyConfig) (int, map[string]int) {
	t.Helper()
	name := fmt.Sprintf("channel-refresh-%d", channelRefreshFixtureID.Add(1))
	grants := make([]model.ChannelGrantConfig, 0, len(keys))
	for _, key := range keys {
		grants = append(grants, model.ChannelGrantConfig{
			ModelName: "old-model",
			KeyName:   key.Name,
			Protocols: model.ProtocolOpenAIResponse,
		})
	}
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                 name,
			Enabled:              true,
			BaseURL:              baseURL,
			OpenAIResponsePath:   "/v1/responses",
			AnthropicMessagePath: "/v1/messages",
		},
		Keys:   keys,
		Models: []string{"old-model"},
		Grants: grants,
	}, t.Context())
	if err != nil {
		t.Fatalf("create refresh fixture channel: %v", err)
	}
	keyIDs := make(map[string]int, len(keys))
	for _, channelKey := range channelKeyCache.GetAll() {
		if channelKey.ChannelID == detail.ID {
			keyIDs[channelKey.Name] = channelKey.ID
		}
	}
	return detail.ID, keyIDs
}

func addRefreshGroup(t *testing.T, channelID, keyID int) (model.Group, int) {
	t.Helper()
	var grantID int
	for _, grant := range channelGrantCache.GetAll() {
		if grant.ChannelKeyID == keyID {
			if channelModel, ok := channelModelCache.Get(grant.ChannelModelID); ok && channelModel.ChannelID == channelID && channelModel.Name == "old-model" {
				grantID = grant.ID
				break
			}
		}
	}
	if grantID == 0 {
		t.Fatal("fixture old model grant not found")
	}
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  fmt.Sprintf("channel-refresh-group-%d", channelRefreshFixtureID.Add(1)),
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("create refresh fixture group: %v", err)
	}
	activeItemID := group.Items[0].ID
	group, err = GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &activeItemID}, t.Context())
	if err != nil {
		t.Fatalf("select refresh fixture group member: %v", err)
	}
	return *group, grantID
}

func refreshGrantID(t *testing.T, channelID, keyID int, modelName string) int {
	t.Helper()
	for _, grant := range channelGrantCache.GetAll() {
		if grant.ChannelKeyID != keyID {
			continue
		}
		channelModel, ok := channelModelCache.Get(grant.ChannelModelID)
		if ok && channelModel.ChannelID == channelID && channelModel.Name == modelName {
			return grant.ID
		}
	}
	t.Fatalf("fixture grant not found: channel=%d key=%d model=%s", channelID, keyID, modelName)
	return 0
}

func setRefreshGrantProtocols(t *testing.T, channelID int, protocols model.Protocol) {
	t.Helper()
	detail, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range detail.Grants {
		detail.Grants[i].Protocols = protocols
	}
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("update fixture grant protocols: %v", err)
	}
}

func TestRefreshChannelModelsMergesKeysAndReplacesGrants(t *testing.T) {
	server := newModelListServer(t, func(key string, anthropic bool) (int, string) {
		if key == "key-a" && !anthropic {
			return http.StatusOK, `{"data":[{"id":"shared"},{"id":"only-a"}]}`
		}
		if key == "key-b" && anthropic {
			return http.StatusOK, `{"data":[{"id":"shared"},{"id":"only-b"}],"has_more":false}`
		}
		return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "key-a", Key: "key-a", Enabled: true},
		{Name: "key-b", Key: "key-b", Enabled: true},
	})
	detail, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range detail.Grants {
		if detail.Grants[i].KeyName == "key-b" {
			detail.Grants[i].Protocols = model.ProtocolAnthropicMessage
		}
	}
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("configure existing protocol grants: %v", err)
	}
	group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key-a"])

	result, err := RefreshChannelModels(t.Context(), channelID, model.ChannelModelRefreshOptions{})
	if err != nil {
		t.Fatalf("refresh channel models: %v", err)
	}
	if !result.Success || result.ZeroModels || !slices.Equal(result.AddedModels, []string{"only-a", "only-b", "shared"}) || !slices.Equal(result.RemovedModels, []string{"old-model"}) {
		t.Fatalf("unexpected refresh delta: %+v", result)
	}
	if len(result.KeyResults) != 2 || !result.KeyResults[0].Success || !result.KeyResults[1].Success || result.KeyResults[0].ModelCount != 2 || result.KeyResults[1].ModelCount != 2 {
		t.Fatalf("unexpected per-key results: %+v", result.KeyResults)
	}
	for _, keyResult := range result.KeyResults {
		assertRefreshKeyChanges(t, keyResult, 2, 1)
	}
	if _, ok := channelGrantCache.Get(oldGrantID); ok {
		t.Fatal("removed model grant remains in cache")
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(updated.Models, []string{"only-a", "only-b", "shared"}) || len(updated.Grants) != 4 {
		t.Fatalf("updated channel detail = models %v grants %v", updated.Models, updated.Grants)
	}
	protocols := make(map[string]model.Protocol, len(updated.Grants))
	for _, grant := range updated.Grants {
		protocols[grant.ModelName+":"+grant.KeyName] = grant.Protocols
	}
	if protocols["shared:key-a"] != model.ProtocolOpenAIResponse || protocols["shared:key-b"] != model.ProtocolAnthropicMessage {
		t.Fatalf("shared model grants were not merged by key: %v", protocols)
	}
	updatedGroup, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedGroup.ActiveItemID != 0 || len(updatedGroup.Items) != 0 {
		t.Fatalf("removed active grant left group state: %+v", updatedGroup)
	}
}

func TestRefreshChannelModelsReportsIndependentKeyChanges(t *testing.T) {
	var changed atomic.Bool
	server := newModelListServer(t, func(key string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		if changed.Load() {
			if key == "key-a" {
				return http.StatusOK, `{"data":[{"id":"shared"},{"id":"only-b"},{"id":"extra"}]}`
			}
			return http.StatusOK, `{"data":[{"id":"shared"}]}`
		}
		if key == "key-a" {
			return http.StatusOK, `{"data":[{"id":"shared"},{"id":"only-a"}]}`
		}
		return http.StatusOK, `{"data":[{"id":"shared"},{"id":"only-b"}]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "key-a", Key: "key-a", Enabled: true},
		{Name: "key-b", Key: "key-b", Enabled: true},
	})
	result, err := RefreshChannelModels(t.Context(), channelID, model.ChannelModelRefreshOptions{})
	if err != nil || !result.Success {
		t.Fatalf("initial refresh: result=%+v err=%v", result, err)
	}
	changed.Store(true)
	result, err = RefreshChannelModels(t.Context(), channelID, model.ChannelModelRefreshOptions{})
	if err != nil || !result.Success || !slices.Equal(result.AddedModels, []string{"extra"}) || !slices.Equal(result.RemovedModels, []string{"only-a"}) {
		t.Fatalf("channel refresh delta: result=%+v err=%v", result, err)
	}
	want := map[string]struct{ models, added, removed int }{
		"key-a": {models: 3, added: 2, removed: 1},
		"key-b": {models: 1, added: 0, removed: 1},
	}
	if len(result.KeyResults) != len(want) {
		t.Fatalf("unexpected key count: %+v", result.KeyResults)
	}
	for _, keyResult := range result.KeyResults {
		expected, ok := want[keyResult.KeyName]
		if !ok || !keyResult.Success || keyResult.ModelCount != expected.models {
			t.Fatalf("unexpected key result: %+v", keyResult)
		}
		assertRefreshKeyChanges(t, keyResult, expected.added, expected.removed)
	}
}

func TestRefreshChannelModelsAutoAddsNewGrantsByMatchMode(t *testing.T) {
	tests := []struct {
		name      string
		groupName string
		modelName string
		mode      model.GroupModelMatchMode
		wantItems int
	}{
		{name: "keyword match", groupName: "new-keyword", modelName: "new-keyword-model", mode: model.GroupModelMatchModeKeyword, wantItems: 2},
		{name: "exact rejects suffix", groupName: "new-exact", modelName: "new-exact-model", mode: model.GroupModelMatchModeExact, wantItems: 1},
		{name: "exact match", groupName: "new-exact-match-model", modelName: "new-exact-match-model", mode: model.GroupModelMatchModeExact, wantItems: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
				if anthropic {
					return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
				}
				return http.StatusOK, fmt.Sprintf(`{"data":[{"id":"old-model"},{"id":%q}]}`, test.modelName)
			})
			channelID, keyIDs := newRefreshChannel(t, upstream.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
			group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key"])
			t.Cleanup(func() { _ = GroupDel(group.ID, t.Context()) })
			matchMode := test.mode
			if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Name: &test.groupName, ModelMatchMode: &matchMode}, t.Context()); err != nil {
				t.Fatalf("update group matching config: %v", err)
			}
			oldItemID := group.Items[0].ID

			result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{AutoAddGroups: true}, newRefreshHTTPClient(t))
			if err != nil || !result.Success {
				t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
			}
			updated, err := GroupGet(group.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.Items) != test.wantItems {
				t.Fatalf("group items = %+v, want %d", updated.Items, test.wantItems)
			}
			if updated.Items[0].ID != oldItemID || updated.Items[0].ChannelGrantID != oldGrantID || updated.Items[0].Priority != 1 {
				t.Fatalf("existing item changed: %+v", updated.Items[0])
			}
			if test.wantItems == 2 {
				if updated.Items[1].ModelName != test.modelName || updated.Items[1].Priority != 2 {
					t.Fatalf("new item was not appended: %+v", updated.Items)
				}
			}
		})
	}
}

func TestRefreshChannelModelsBackfillsExistingGrants(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, `{"data":[{"id":"old-model"}]}`
	})
	channelID, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "fixture-key", Enabled: true}})
	grantID := refreshGrantID(t, channelID, keys["key"], "old-model")
	group, err := GroupCreate(&model.GroupCreateRequest{Name: "old-model", ModelMatchMode: model.GroupModelMatchModeExact}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = GroupDel(group.ID, t.Context()) })
	t.Cleanup(func() { _ = ChannelDel(channelID, t.Context()) })

	var itemID int
	for _, autoAdd := range []bool{false, true, true} {
		result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{AutoAddGroups: autoAdd}, newRefreshHTTPClient(t))
		if err != nil || !result.Success || len(result.AddedModels) != 0 {
			t.Fatalf("refresh existing model: result=%+v err=%v", result, err)
		}
		updated, err := GroupGet(group.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !autoAdd {
			if len(updated.Items) != 0 {
				t.Fatal("disabled auto-add appended an existing grant")
			}
			continue
		}
		if len(updated.Items) != 1 || updated.Items[0].ChannelGrantID != grantID || updated.Items[0].Priority != 1 {
			t.Fatalf("existing matching grant was not backfilled once: %+v", updated.Items)
		}
		if itemID != 0 && updated.Items[0].ID != itemID {
			t.Fatal("repeated refresh replaced an existing group member")
		}
		itemID = updated.Items[0].ID
	}
}

func TestRefreshChannelModelsDoesNotAutoAddWhenDisabled(t *testing.T) {
	token := fmt.Sprintf("new-disabled-%d", channelRefreshFixtureID.Add(1))
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, fmt.Sprintf(`{"data":[{"id":%q}]}`, token)
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	group, _ := addRefreshGroup(t, channelID, keyIDs["key"])
	groupName := token
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Name: &groupName}, t.Context()); err != nil {
		t.Fatalf("update group name: %v", err)
	}
	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	updated, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 0 || updated.ActiveItemID != 0 {
		t.Fatalf("auto-add disabled changed group items: %+v", updated.Items)
	}
}

func TestRefreshChannelModelsPreservesSameNameModelFromOtherChannel(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelA, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "a", Enabled: true}})
	channelB, keysB := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "b", Enabled: true}})
	groupB, grantB := addRefreshGroup(t, channelB, keysB["key"])
	if _, err := RefreshChannelModelsWithClient(t.Context(), channelA, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t)); err != nil {
		t.Fatalf("refresh channel A: %v", err)
	}
	if _, ok := channelGrantCache.Get(grantB); !ok {
		t.Fatal("refresh channel A removed same-name grant from channel B")
	}
	updated, err := GroupGet(groupB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 1 || updated.Items[0].ID != groupB.Items[0].ID || updated.Items[0].ChannelGrantID != grantB {
		t.Fatalf("refresh channel A changed channel B group: %+v", updated)
	}
	if _, err := ChannelDetailGet(channelB); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshChannelModelsPreservesExistingPrioritiesWhenAppending(t *testing.T) {
	newModelName := fmt.Sprintf("new-priority-%d", channelRefreshFixtureID.Add(1))
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, fmt.Sprintf(`{"data":[{"id":"old-model"},{"id":%q}]}`, newModelName)
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "key-a", Key: "a", Enabled: true},
		{Name: "key-b", Key: "b", Enabled: true},
	})
	oldA := refreshGrantID(t, channelID, keyIDs["key-a"], "old-model")
	oldB := refreshGrantID(t, channelID, keyIDs["key-b"], "old-model")
	groupName := newModelName
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  groupName,
		Items: []model.GroupItemInput{{ChannelGrantID: oldA}, {ChannelGrantID: oldB}},
	}, t.Context())
	if err != nil {
		t.Fatalf("create priority group: %v", err)
	}
	if err := db.GetDB().Model(&model.GroupItem{}).Where("id = ?", group.Items[0].ID).Update("priority", 3).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Model(&model.GroupItem{}).Where("id = ?", group.Items[1].ID).Update("priority", 8).Error; err != nil {
		t.Fatal(err)
	}
	if err := groupRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{AutoAddGroups: true}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	updated, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 4 {
		t.Fatalf("priority group items = %+v", updated.Items)
	}
	if updated.Items[0].ID != group.Items[0].ID || updated.Items[0].Priority != 3 || updated.Items[1].ID != group.Items[1].ID || updated.Items[1].Priority != 8 {
		t.Fatalf("existing priorities or IDs changed: %+v", updated.Items)
	}
	if updated.Items[2].Priority != 9 || updated.Items[3].Priority != 10 {
		t.Fatalf("new members were not appended after max priority: %+v", updated.Items)
	}
}

func TestRefreshChannelModelsRollsBackWhenGroupAppendFails(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key"])
	oldItemID := group.Items[0].ID
	groupName := "new-model"
	matchMode := model.GroupModelMatchModeExact
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{Name: &groupName, ModelMatchMode: &matchMode}, t.Context()); err != nil {
		t.Fatalf("update rollback fixture group: %v", err)
	}
	if err := db.GetDB().Exec(`CREATE TRIGGER fail_refresh_group_item BEFORE INSERT ON group_items WHEN NEW.channel_grant_id IN (SELECT cg.id FROM channel_grants cg JOIN channel_models cm ON cm.id = cg.channel_model_id WHERE cm.name = 'new-model') BEGIN SELECT RAISE(ABORT, 'forced group sync failure'); END`).Error; err != nil {
		t.Fatalf("create group sync failure trigger: %v", err)
	}
	t.Cleanup(func() { _ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_refresh_group_item").Error })

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{AutoAddGroups: true}, newRefreshHTTPClient(t))
	if err == nil || result.Success {
		t.Fatalf("expected group sync failure, result=%+v err=%v", result, err)
	}
	detail, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(detail.Models, []string{"old-model"}) || len(detail.Grants) != 1 {
		t.Fatalf("group sync failure changed channel: detail=%+v err=%v", detail, err)
	}
	if _, ok := channelGrantCache.Get(oldGrantID); !ok {
		t.Fatal("group sync failure removed old grant from cache")
	}
	updated, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 1 || updated.Items[0].ID != oldItemID || updated.Items[0].ChannelGrantID != oldGrantID || updated.ActiveItemID != oldItemID {
		t.Fatalf("group sync failure changed group: %+v", updated)
	}
}

func TestRefreshChannelModelsAcceptsSourceAndFilteredEmptyResults(t *testing.T) {
	tests := []struct {
		name     string
		response string
		filter   string
	}{
		{name: "empty upstream list", response: `{"data":[]}`},
		{name: "global filter removes all", response: `{"data":[{"id":"discard"}]}`, filter: "^keep$"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			previous, err := SettingGetString(model.SettingKeyModelFilter)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = SettingSetString(model.SettingKeyModelFilter, previous) })
			if err := SettingSetString(model.SettingKeyModelFilter, test.filter); err != nil {
				t.Fatal(err)
			}
			server := newModelListServer(t, func(_ string, _ bool) (int, string) {
				return http.StatusOK, test.response
			})
			channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
			group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key"])

			result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
			if err != nil {
				t.Fatalf("refresh channel models: %v", err)
			}
			if !result.Success || !result.ZeroModels || !result.KeyResults[0].Success || result.KeyResults[0].ModelCount != 0 {
				t.Fatalf("empty refresh result = %+v", result)
			}
			updated, err := ChannelDetailGet(channelID)
			if err != nil || len(updated.Models) != 0 || len(updated.Grants) != 0 {
				t.Fatalf("empty channel detail = %+v, err = %v", updated, err)
			}
			if _, ok := channelGrantCache.Get(oldGrantID); ok {
				t.Fatal("stale grant remained in channel grant cache after zero-model refresh")
			}
			updatedGroup, err := GroupGet(group.ID)
			if err != nil || updatedGroup.ActiveItemID != 0 || len(updatedGroup.Items) != 0 {
				t.Fatalf("empty group after refresh = %+v, err = %v", updatedGroup, err)
			}
		})
	}
}

func TestRefreshChannelModelsFailurePreservesOldStateAndReportsEveryKey(t *testing.T) {
	server := newModelListServer(t, func(key string, anthropic bool) (int, string) {
		if key == "good" && !anthropic {
			return http.StatusOK, `{"data":[{"id":"new-model"}]}`
		}
		return http.StatusUnauthorized, `{"error":"denied"}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "a-good", Key: "good", Enabled: true},
		{Name: "b-bad", Key: "bad", Enabled: true},
	})
	group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["a-good"])

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil {
		t.Fatalf("refresh channel models: %v", err)
	}
	if result.Success || len(result.KeyResults) != 2 || !result.KeyResults[0].Success || result.KeyResults[1].Success || !strings.Contains(result.KeyResults[1].Error, "upstream 401") {
		t.Fatalf("unexpected failed refresh result: %+v", result)
	}
	for _, keyResult := range result.KeyResults {
		assertRefreshKeyChanges(t, keyResult, 0, 0)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(updated.Models, []string{"old-model"}) || len(updated.Grants) != 2 {
		t.Fatalf("failed refresh changed channel state: detail=%+v err=%v", updated, err)
	}
	if _, ok := channelGrantCache.Get(oldGrantID); !ok {
		t.Fatal("failed refresh removed old grant from cache")
	}
	updatedGroup, err := GroupGet(group.ID)
	if err != nil || updatedGroup.ActiveItemID != group.ActiveItemID || len(updatedGroup.Items) != 1 {
		t.Fatalf("failed refresh changed group state: group=%+v err=%v", updatedGroup, err)
	}
}

func TestRefreshChannelModelsPreservesExistingChatProtocolWhenBothSidesReturnModel(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusOK, `{"data":[{"id":"old-model"}]}`
		}
		return http.StatusOK, `{"data":[{"id":"old-model"},{"id":"new-model"}]}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	setRefreshGrantProtocols(t, channelID, model.ProtocolOpenAIChatCompletion)

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	protocols := make(map[string]model.Protocol, len(updated.Grants))
	for _, grant := range updated.Grants {
		if grant.KeyName == "key" {
			protocols[grant.ModelName] = grant.Protocols
		}
	}
	if protocols["old-model"] != model.ProtocolOpenAIChatCompletion {
		t.Fatalf("existing OpenAI grant changed protocol: %v", protocols)
	}
	if protocols["new-model"] != model.ProtocolOpenAIResponse {
		t.Fatalf("new OpenAI grant did not use the probe default: %v", protocols)
	}
	if _, ok := channelGrantCache.Get(refreshGrantID(t, channelID, keyIDs["key"], "old-model")); !ok {
		t.Fatal("existing Chat grant missing from cache")
	}
}

func TestRefreshChannelModelsDoesNotAddOpenAIToExistingAnthropicOnlyGrant(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusOK, `{"data":[{"id":"old-model"}]}`
		}
		return http.StatusOK, `{"data":[{"id":"old-model"},{"id":"new-model"}]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	setRefreshGrantProtocols(t, channelID, model.ProtocolAnthropicMessage)

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	protocols := make(map[string]model.Protocol, len(updated.Grants))
	for _, grant := range updated.Grants {
		protocols[grant.ModelName] = grant.Protocols
	}
	if protocols["old-model"] != model.ProtocolAnthropicMessage {
		t.Fatalf("OpenAI probe added protocols to Anthropic-only grant: %v", protocols)
	}
	if protocols["new-model"] != model.ProtocolOpenAIResponse {
		t.Fatalf("new OpenAI grant protocol = %d, want %d", protocols["new-model"], model.ProtocolOpenAIResponse)
	}
}

func TestRefreshChannelModelsRemovesOpenAIWhenModelOnlyRemainsOnAnthropic(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusOK, `{"data":[{"id":"old-model"}]}`
		}
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	setRefreshGrantProtocols(t, channelID, model.ProtocolOpenAIResponse|model.ProtocolAnthropicMessage)

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	protocols := make(map[string]model.Protocol, len(updated.Grants))
	for _, grant := range updated.Grants {
		protocols[grant.ModelName] = grant.Protocols
	}
	if protocols["old-model"] != model.ProtocolAnthropicMessage {
		t.Fatalf("stale OpenAI protocol was retained: %v", protocols)
	}
	if protocols["new-model"] != model.ProtocolOpenAIResponse {
		t.Fatalf("new OpenAI grant protocol = %d, want %d", protocols["new-model"], model.ProtocolOpenAIResponse)
	}
}

func TestRefreshChannelModelsAnthropicFailureWithExistingGrantPreservesState(t *testing.T) {
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"denied"}`
		}
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	setRefreshGrantProtocols(t, channelID, model.ProtocolAnthropicMessage)
	group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key"])
	oldItemID := group.Items[0].ID

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil {
		t.Fatalf("refresh channel models: %v", err)
	}
	if result.Success || len(result.KeyResults) != 1 || result.KeyResults[0].Success || !strings.Contains(result.KeyResults[0].Error, "anthropic:") {
		t.Fatalf("Anthropic failure was not reported for the existing grant: %+v", result)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(updated.Models, []string{"old-model"}) || len(updated.Grants) != 1 || updated.Grants[0].Protocols != model.ProtocolAnthropicMessage {
		t.Fatalf("failed refresh changed channel detail: detail=%+v err=%v", updated, err)
	}
	if _, ok := channelGrantCache.Get(oldGrantID); !ok {
		t.Fatal("failed refresh removed the old grant from cache")
	}
	var storedModels []model.ChannelModel
	if err := db.GetDB().Where("channel_id = ?", channelID).Find(&storedModels).Error; err != nil || len(storedModels) != 1 || storedModels[0].Name != "old-model" {
		t.Fatalf("failed refresh changed database models: models=%+v err=%v", storedModels, err)
	}
	var storedGrants []model.ChannelGrant
	if err := db.GetDB().Where("channel_key_id = ?", keyIDs["key"]).Find(&storedGrants).Error; err != nil || len(storedGrants) != 1 || storedGrants[0].Protocols != model.ProtocolAnthropicMessage {
		t.Fatalf("failed refresh changed database grants: grants=%+v err=%v", storedGrants, err)
	}
	updatedGroup, err := GroupGet(group.ID)
	if err != nil || len(updatedGroup.Items) != 1 || updatedGroup.Items[0].ID != oldItemID || updatedGroup.Items[0].ChannelGrantID != oldGrantID || updatedGroup.ActiveItemID != oldItemID {
		t.Fatalf("failed refresh changed group state: group=%+v err=%v", updatedGroup, err)
	}
}

func TestRefreshChannelModelsToleratesFailedSideWithoutExistingGrant(t *testing.T) {
	tests := []struct {
		name          string
		existing      model.Protocol
		failedSide    bool
		response      string
		wantProtocol  model.Protocol
		failureStatus int
	}{
		{
			name:          "OpenAI failure without OpenAI grant",
			existing:      model.ProtocolAnthropicMessage,
			failedSide:    false,
			response:      `{"data":[{"id":"old-model"},{"id":"new-model"}]}`,
			wantProtocol:  model.ProtocolAnthropicMessage,
			failureStatus: http.StatusUnauthorized,
		},
		{
			name:          "Anthropic failure without Anthropic grant",
			existing:      model.ProtocolOpenAIResponse,
			failedSide:    true,
			response:      `{"data":[{"id":"old-model"},{"id":"new-model"}]}`,
			wantProtocol:  model.ProtocolOpenAIResponse,
			failureStatus: http.StatusUnauthorized,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
				if anthropic == test.failedSide {
					return test.failureStatus, `{"error":"unsupported endpoint"}`
				}
				return http.StatusOK, test.response
			})
			channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
			setRefreshGrantProtocols(t, channelID, test.existing)

			result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
			if err != nil || !result.Success || len(result.KeyResults) != 1 || !result.KeyResults[0].Success {
				t.Fatalf("refresh should accept unsupported side: result=%+v err=%v", result, err)
			}
			wantDiagnostic := "openai:"
			if test.failedSide {
				wantDiagnostic = "anthropic:"
			}
			if !strings.Contains(result.KeyResults[0].Error, wantDiagnostic) {
				t.Fatalf("per-key tolerated-side diagnostic missing %q: %+v", wantDiagnostic, result.KeyResults[0])
			}
			updated, err := ChannelDetailGet(channelID)
			if err != nil {
				t.Fatal(err)
			}
			protocols := make(map[string]model.Protocol, len(updated.Grants))
			for _, grant := range updated.Grants {
				if grant.KeyName == "key" {
					protocols[grant.ModelName] = grant.Protocols
				}
			}
			if protocols["new-model"] != test.wantProtocol {
				t.Fatalf("new grant protocols = %d, want %d; all = %v", protocols["new-model"], test.wantProtocol, protocols)
			}
			if _, ok := channelGrantCache.Get(refreshGrantID(t, channelID, keyIDs["key"], "new-model")); !ok {
				t.Fatal("successful side grant missing from cache")
			}
		})
	}
}

func TestRefreshChannelModelsWithoutEnabledKeysPreservesExistingState(t *testing.T) {
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		return http.StatusOK, `{"data":[]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "disabled", Key: "secret", Enabled: false}})
	detail, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	detail.Keys[0].Enabled = false
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("disable fixture key through update path: %v", err)
	}

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil {
		t.Fatalf("refresh without enabled keys: %v", err)
	}
	if result.Success || result.Error != "channel has no enabled keys" || len(result.KeyResults) != 0 {
		t.Fatalf("missing enabled-key failure result: %+v", result)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(updated.Models, []string{"old-model"}) || len(updated.Grants) != 1 {
		t.Fatalf("refresh without keys changed channel state: detail=%+v err=%v", updated, err)
	}
}

func TestRefreshChannelModelsRemovesDisabledKeyGrantsAndModels(t *testing.T) {
	var disabledRequests atomic.Int32
	server := newModelListServer(t, func(key string, anthropic bool) (int, string) {
		if key == "disabled-secret" {
			disabledRequests.Add(1)
			return http.StatusUnauthorized, `{"error":"disabled key was probed"}`
		}
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, `{"data":[{"id":"old-model"},{"id":"enabled-only-model"}]}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{
		{Name: "enabled", Key: "enabled-secret", Enabled: true},
		{Name: "disabled", Key: "disabled-secret", Enabled: true},
	})
	detail, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range detail.Keys {
		if detail.Keys[i].Name == "disabled" {
			detail.Keys[i].Enabled = false
		}
	}
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("disable fixture key: %v", err)
	}
	detail, err = ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	const disabledOnlyModel = "disabled-only-model"
	detail.Models = append(detail.Models, disabledOnlyModel)
	detail.Grants = append(detail.Grants, model.ChannelGrantConfig{
		ModelName: disabledOnlyModel,
		KeyName:   "disabled",
		Protocols: model.ProtocolAnthropicMessage,
	})
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("add disabled-only model grant: %v", err)
	}
	disabledOldGrantID := refreshGrantID(t, channelID, keyIDs["disabled"], "old-model")
	disabledOnlyGrantID := refreshGrantID(t, channelID, keyIDs["disabled"], disabledOnlyModel)
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  "refresh-disabled-only-group",
		Items: []model.GroupItemInput{{ChannelGrantID: disabledOnlyGrantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("create disabled grant group: %v", err)
	}
	activeItemID := group.Items[0].ID
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &activeItemID}, t.Context()); err != nil {
		t.Fatalf("select disabled grant group member: %v", err)
	}

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh channel models: result=%+v err=%v", result, err)
	}
	if disabledRequests.Load() != 0 {
		t.Fatalf("disabled key was probed %d times", disabledRequests.Load())
	}
	if len(result.KeyResults) != 1 || result.KeyResults[0].KeyName != "enabled" || !result.KeyResults[0].Success || !strings.Contains(result.KeyResults[0].Error, "anthropic:") {
		t.Fatalf("enabled key result did not include tolerated-side diagnostic: %+v", result.KeyResults)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	models := make(map[string]struct{}, len(updated.Models))
	for _, name := range updated.Models {
		models[name] = struct{}{}
	}
	if _, exists := models[disabledOnlyModel]; exists {
		t.Fatalf("disabled-only model was retained: %v", updated.Models)
	}
	grants := make(map[string]model.Protocol, len(updated.Grants))
	for _, grant := range updated.Grants {
		grants[grant.ModelName+":"+grant.KeyName] = grant.Protocols
	}
	if grants["old-model:disabled"] != 0 || grants[disabledOnlyModel+":disabled"] != 0 || grants["old-model:enabled"] == 0 {
		t.Fatalf("refresh did not replace grants with enabled key results: %v", grants)
	}
	if _, ok := channelGrantCache.Get(disabledOldGrantID); ok {
		t.Fatal("disabled key's existing grant remains cached")
	}
	if _, ok := channelGrantCache.Get(disabledOnlyGrantID); ok {
		t.Fatal("disabled-only model grant remains cached")
	}
	updatedGroup, err := GroupGet(group.ID)
	if err != nil || len(updatedGroup.Items) != 0 || updatedGroup.ActiveItemID != 0 {
		t.Fatalf("disabled-only group state was not cleared: group=%+v err=%v", updatedGroup, err)
	}
}

func TestRefreshChannelModelsDisabledChannelCleansStaleWithoutAutoAdding(t *testing.T) {
	newModelName := fmt.Sprintf("disabled-new-model-%d", channelRefreshFixtureID.Add(1))
	server := newModelListServer(t, func(_ string, anthropic bool) (int, string) {
		if anthropic {
			return http.StatusUnauthorized, `{"error":"unsupported endpoint"}`
		}
		return http.StatusOK, fmt.Sprintf(`{"data":[{"id":%q}]}`, newModelName)
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	oldGrantID := refreshGrantID(t, channelID, keyIDs["key"], "old-model")
	group, err := GroupCreate(&model.GroupCreateRequest{
		Name:  newModelName,
		Items: []model.GroupItemInput{{ChannelGrantID: oldGrantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("create disabled refresh group: %v", err)
	}
	activeItemID := group.Items[0].ID
	if _, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &activeItemID}, t.Context()); err != nil {
		t.Fatalf("select disabled refresh group member: %v", err)
	}
	if err := ChannelEnabled(channelID, false, t.Context()); err != nil {
		t.Fatalf("disable refresh fixture channel: %v", err)
	}

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{AutoAddGroups: true}, newRefreshHTTPClient(t))
	if err != nil || !result.Success {
		t.Fatalf("refresh disabled channel: result=%+v err=%v", result, err)
	}
	updated, err := GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 0 || updated.ActiveItemID != 0 {
		t.Fatalf("stale member was not cleaned from disabled channel: %+v", updated)
	}
	newGrantID := refreshGrantID(t, channelID, keyIDs["key"], newModelName)
	if _, ok := channelGrantCache.Get(newGrantID); !ok {
		t.Fatal("refreshed grant missing for disabled channel")
	}
	updated, err = GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range updated.Items {
		if item.ChannelGrantID == newGrantID {
			t.Fatal("disabled channel auto-added a new group member")
		}
	}
}

func TestRefreshChannelModelsRollsBackTransactionFailure(t *testing.T) {
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelID, keyIDs := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	group, oldGrantID := addRefreshGroup(t, channelID, keyIDs["key"])
	if err := db.GetDB().Exec(`CREATE TRIGGER fail_refresh_grant BEFORE INSERT ON channel_grants WHEN NEW.channel_model_id IN (SELECT id FROM channel_models WHERE name = 'new-model') BEGIN SELECT RAISE(ABORT, 'forced refresh failure'); END`).Error; err != nil {
		t.Fatalf("create transaction failure trigger: %v", err)
	}
	t.Cleanup(func() { _ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_refresh_grant").Error })

	result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
	if err == nil || result.Success {
		t.Fatalf("expected mid-transaction failure, result=%+v err=%v", result, err)
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(updated.Models, []string{"old-model"}) || len(updated.Grants) != 1 {
		t.Fatalf("transaction failure left partial channel state: detail=%+v err=%v", updated, err)
	}
	if _, ok := channelGrantCache.Get(oldGrantID); !ok {
		t.Fatal("transaction failure removed cached grant")
	}
	updatedGroup, err := GroupGet(group.ID)
	if err != nil || updatedGroup.ActiveItemID != group.ActiveItemID || len(updatedGroup.Items) != 1 {
		t.Fatalf("transaction failure changed group state: group=%+v err=%v", updatedGroup, err)
	}
}

func TestRefreshChannelModelsRejectsConcurrentRequest(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		once.Do(func() { started <- struct{}{} })
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			return http.StatusGatewayTimeout, `{"error":"test timeout"}`
		}
		return http.StatusOK, `{"data":[]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	client := newRefreshHTTPClient(t)
	firstResult := make(chan model.ChannelModelRefreshResult, 1)
	firstError := make(chan error, 1)
	go func() {
		result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, client)
		firstResult <- result
		firstError <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach upstream")
	}
	second, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, client)
	if err != nil || second.Success || second.Error != "channel refresh already in progress" {
		t.Fatalf("concurrent refresh result = %+v, err = %v", second, err)
	}
	close(release)
	select {
	case err := <-firstError:
		if err != nil {
			t.Fatal(err)
		}
		result := <-firstResult
		if !result.Success || !result.ZeroModels {
			t.Fatalf("first refresh result = %+v", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first refresh did not finish")
	}
}

func TestRefreshChannelModelsRejectsProbeAgainstChangedChannel(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		once.Do(func() { started <- struct{}{} })
		<-release
		return http.StatusOK, `{"data":[{"id":"new-model"}]}`
	})
	channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "secret", Enabled: true}})
	client := newRefreshHTTPClient(t)
	resultCh := make(chan model.ChannelModelRefreshResult, 1)
	errorCh := make(chan error, 1)
	go func() {
		result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{}, client)
		resultCh <- result
		errorCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not reach upstream")
	}
	detail, err := ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	detail.BaseURL += "/changed"
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatalf("update channel during refresh: %v", err)
	}
	close(release)
	select {
	case err := <-errorCh:
		result := <-resultCh
		if !errors.Is(err, ErrChannelRefreshStale) || result.Success {
			t.Fatalf("stale refresh result=%+v err=%v", result, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stale refresh did not finish")
	}
	updated, err := ChannelDetailGet(channelID)
	if err != nil || !slices.Equal(updated.Models, []string{"old-model"}) {
		t.Fatalf("stale refresh changed models: detail=%+v err=%v", updated, err)
	}
}
