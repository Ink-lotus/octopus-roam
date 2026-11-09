package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	serverauth "github.com/Ink-lotus/octopus-roam/internal/server/auth"
	"github.com/Ink-lotus/octopus-roam/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func newCategoryHTTPFixture(t *testing.T) cooldownHTTPFixture {
	t.Helper()
	engine := gin.New()
	// 使用实际登记的分类路由及中间件，不消耗由既有 runtime 测试核对的全局 registry。
	for _, route := range groupCategoryRoutes.Routes {
		handlers := append([]gin.HandlerFunc{}, groupCategoryRoutes.Middlewares...)
		handlers = append(handlers, route.Middlewares...)
		handlers = append(handlers, route.Handlers...)
		engine.Handle(route.Method, groupCategoryRoutes.Path+route.Path, handlers...)
	}
	groups := engine.Group("/api/v1/group", middleware.Auth(), middleware.RequireJSON())
	groups.GET("/list", getGroupList)
	groups.GET("/get/:id", getGroup)
	groups.GET("/events", streamGroupEvents)
	groups.POST("/create", createGroup)
	groups.POST("/update/:id", updateGroup)
	groups.DELETE("/delete/:id", deleteGroup)
	groups.POST("/route/:id/select", selectGroupRouteItem)
	groups.POST("/route/:id/release-cooldown", releaseGroupRouteCooldown)
	engine.GET("/api/v1/channel/stats", middleware.Auth(), listChannelStats)
	engine.POST("/api/v1/setting/import", middleware.Auth(), importDB)
	engine.POST("/fixture-forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	fixture := cooldownHTTPFixture{server: httptest.NewServer(engine)}
	t.Cleanup(fixture.server.Close)
	var err error
	fixture.token, _, err = serverauth.GenerateJWTToken(60)
	if err != nil {
		t.Fatal("could not create fixture authentication")
	}
	return fixture
}

func categoryHTTPCall(t *testing.T, fixture cooldownHTTPFixture, method, path, raw string, status int, authenticated bool) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, fixture.server.URL+path, strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "auth", Value: fixture.token})
	} else {
		request.Header.Set("x-api-key", "fixture-is-not-an-admin-cookie")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status {
		t.Fatalf("%s %s returned %d, want %d; error=%v body=%s", method, path, response.StatusCode, status, err, body)
	}
	return body
}

func categoryHTTPData[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var envelope struct {
		Data T `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func createCategoryHTTP(t *testing.T, fixture cooldownHTTPFixture, name string) model.GroupCategory {
	t.Helper()
	value := categoryHTTPData[model.GroupCategory](t, fixture.request(t, http.MethodPost, "/api/v1/group-category/create", map[string]string{"name": name}))
	t.Cleanup(func() { _, _ = op.GroupCategoryDelete(value.ID, context.Background()) })
	return value
}

func createCategoryHTTPGroup(t *testing.T, fixture cooldownHTTPFixture, category *int) groupResponse {
	t.Helper()
	value := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, "/api/v1/group/create", map[string]any{
		"name": fmt.Sprintf("w08-http-group-%d", channelEventTestID.Add(1)), "category_id": category,
	}))
	t.Cleanup(func() { _ = op.GroupDel(value.ID, context.Background()); relay.ResetRouteState(value.ID) })
	return value
}

func categoryVersionNewer(t *testing.T, after, before string) bool {
	t.Helper()
	aEpoch, aSequence, aOK := strings.Cut(after, ":")
	bEpoch, bSequence, bOK := strings.Cut(before, ":")
	a, aErr := strconv.ParseUint(aSequence, 10, 64)
	b, bErr := strconv.ParseUint(bSequence, 10, 64)
	if !aOK || !bOK || aErr != nil || bErr != nil || aEpoch != bEpoch {
		t.Fatalf("invalid metadata versions: %q / %q", after, before)
	}
	if _, err := strconv.ParseUint(aEpoch, 10, 64); err != nil {
		t.Fatal("metadata process identifier is not decimal")
	}
	return a > b
}

func awaitCategoryHTTPEvent(t *testing.T, events <-chan cooldownHTTPEvent, name string) json.RawMessage {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("SSE closed before category event")
			}
			if event.name == name {
				return event.data
			}
		case <-timer.C:
			t.Fatalf("missing %s event", name)
		}
	}
}

func TestGroupCategoryHTTPPermissionsAndValidation(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/list"}, {http.MethodPost, "/create"}, {http.MethodPost, "/update/1"}, {http.MethodDelete, "/delete/1"},
	} {
		categoryHTTPCall(t, fixture, route.method, "/api/v1/group-category"+route.path, `{}`, http.StatusUnauthorized, false)
	}
	first := createCategoryHTTP(t, fixture, "  W08 HTTP Alpha  ")
	if first.Name != "W08 HTTP Alpha" || first.ID <= 0 || first.NameKey != "" {
		t.Fatal("create did not normalize name or exposed internal key")
	}
	second := createCategoryHTTP(t, fixture, "W08 HTTP Beta")
	categoryHTTPCall(t, fixture, http.MethodPost, "/api/v1/group-category/create", `{"name":"w08 http alpha"}`, http.StatusConflict, true)
	categoryHTTPCall(t, fixture, http.MethodPost, fmt.Sprintf("/api/v1/group-category/update/%d", second.ID), `{"name":" W08 HTTP ALPHA "}`, http.StatusConflict, true)
	longName, _ := json.Marshal(map[string]string{"name": strings.Repeat("类", 65)})
	for _, raw := range []string{`{}`, `null`, `[]`, `{"name":null}`, `{"name":1}`, `{"name":""}`, `{"name":"  "}`, `{"name":"a\u0000b"}`, `{"name":"a\nb"}`, `{"name":"Fixture","name_key":"client"}`, `{"id":71,"name":"Fixture"}`, `{"name":"Fixture"} {}`, string(longName)} {
		categoryHTTPCall(t, fixture, http.MethodPost, "/api/v1/group-category/create", raw, http.StatusBadRequest, true)
	}
	for _, id := range []string{"0", "-1", "bad", "1.5"} {
		categoryHTTPCall(t, fixture, http.MethodPost, "/api/v1/group-category/update/"+id, `{"name":"Fixture"}`, http.StatusBadRequest, true)
		categoryHTTPCall(t, fixture, http.MethodDelete, "/api/v1/group-category/delete/"+id, ``, http.StatusBadRequest, true)
	}
	categoryHTTPCall(t, fixture, http.MethodPost, "/api/v1/group-category/update/999999999", `{"name":"Fixture"}`, http.StatusNotFound, true)
	categoryHTTPCall(t, fixture, http.MethodDelete, "/api/v1/group-category/delete/999999999", ``, http.StatusNotFound, true)
	virtualLabel := createCategoryHTTP(t, fixture, "未分类")
	group := createCategoryHTTPGroup(t, fixture, nil)
	if group.CategoryID != nil || virtualLabel.ID == 0 {
		t.Fatal("virtual uncategorized state was inferred from a category name")
	}
}

func TestGroupCategoryHTTPThreeStates(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	category := createCategoryHTTP(t, fixture, "W08 HTTP Three States")
	group := createCategoryHTTPGroup(t, fixture, &category.ID)
	path := fmt.Sprintf("/api/v1/group/update/%d", group.ID)
	omitted := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, path, map[string]any{}))
	if omitted.CategoryID == nil || *omitted.CategoryID != category.ID || omitted.MetadataVersion != group.MetadataVersion {
		t.Fatal("omitted category did not preserve assignment")
	}
	for _, rawID := range []string{"0", "-1", `"71"`, "1.0", "1e2", "true", "[]", "{}", "999999999"} {
		categoryHTTPCall(t, fixture, http.MethodPost, path, `{"category_id":`+rawID+`}`, http.StatusBadRequest, true)
		categoryHTTPCall(t, fixture, http.MethodPost, "/api/v1/group/create", `{"name":"w08-invalid-create","category_id":`+rawID+`}`, http.StatusBadRequest, true)
	}
	cleared := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, path, map[string]any{"category_id": nil}))
	if cleared.CategoryID != nil || !categoryVersionNewer(t, cleared.MetadataVersion, group.MetadataVersion) {
		t.Fatal("null did not produce versioned uncategorized state")
	}
	assigned := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, path, map[string]any{"category_id": category.ID,
		"name": cleared.Name, "mode": cleared.Mode, "model_match_mode": cleared.ModelMatchMode, "relay_config": cleared.RelayConfig, "items": []model.GroupItemInput{}, "active_item_id": 0,
	}))
	if assigned.CategoryID == nil || *assigned.CategoryID != category.ID || assigned.Runtime.ConfigVersion != cleared.Runtime.ConfigVersion || !categoryVersionNewer(t, assigned.MetadataVersion, cleared.MetadataVersion) {
		t.Fatal("category change with unchanged form fields changed route identity")
	}
}

func TestGroupCategoryHTTPSSEVersionsAndReconnect(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	category := createCategoryHTTP(t, fixture, "W08 SSE Category")
	group := createCategoryHTTPGroup(t, fixture, nil)
	getPath := fmt.Sprintf("/api/v1/group/get/%d", group.ID)
	oldGET := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodGet, getPath, nil))
	first, stopFirst := fixture.subscribe(t)
	second, _ := fixture.subscribe(t)
	move := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group/update/%d", group.ID), map[string]any{"category_id": category.ID}))
	for _, stream := range []<-chan cooldownHTTPEvent{first, second} {
		var changed groupResponse
		if err := json.Unmarshal(awaitCategoryHTTPEvent(t, stream, "changed"), &changed); err != nil || changed.ID != group.ID || changed.CategoryID == nil || *changed.CategoryID != category.ID || changed.MetadataVersion != move.MetadataVersion {
			t.Fatalf("changed event missing category assignment: %v", err)
		}
		if !categoryVersionNewer(t, changed.MetadataVersion, oldGET.MetadataVersion) || !reflect.DeepEqual(changed.Runtime, oldGET.Runtime) {
			t.Fatal("stale GET cannot be ordered independently from unchanged runtime")
		}
	}
	beforeList := op.GroupCategoryList()
	fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group-category/update/%d", category.ID), map[string]string{"name": "W08 SSE Renamed"})
	for _, stream := range []<-chan cooldownHTTPEvent{first, second} {
		var list op.GroupCategoryListSnapshot
		if err := json.Unmarshal(awaitCategoryHTTPEvent(t, stream, "categories"), &list); err != nil || !categoryVersionNewer(t, list.Version, beforeList.Version) || !slices.ContainsFunc(list.Items, func(c model.GroupCategory) bool { return c.ID == category.ID && c.Name == "W08 SSE Renamed" }) {
			t.Fatalf("rename did not publish versioned full list: %v", err)
		}
	}
	afterRename := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodGet, getPath, nil))
	if afterRename.MetadataVersion != move.MetadataVersion {
		t.Fatal("category rename advanced group assignment version")
	}
	deleted := categoryHTTPData[op.GroupCategoryDeletion](t, fixture.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/group-category/delete/%d", category.ID), nil))
	if !slices.Equal(deleted.UncategorizedGroupIDs, []int{group.ID}) {
		t.Fatal("delete response omitted affected group")
	}
	for _, stream := range []<-chan cooldownHTTPEvent{first, second} {
		var list op.GroupCategoryListSnapshot
		if err := json.Unmarshal(awaitCategoryHTTPEvent(t, stream, "categories"), &list); err != nil || slices.ContainsFunc(list.Items, func(c model.GroupCategory) bool { return c.ID == category.ID }) {
			t.Fatalf("deleted category remains in SSE list: %v", err)
		}
		var changed groupResponse
		if err := json.Unmarshal(awaitCategoryHTTPEvent(t, stream, "changed"), &changed); err != nil || changed.CategoryID != nil || !categoryVersionNewer(t, changed.MetadataVersion, move.MetadataVersion) || !reflect.DeepEqual(changed.Runtime, move.Runtime) {
			t.Fatalf("delete did not publish independent NULL assignment: %v", err)
		}
	}
	stopFirst()
	next := createCategoryHTTP(t, fixture, "W08 During Disconnect")
	latest := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group/update/%d", group.ID), map[string]any{"category_id": next.ID}))
	_, _ = fixture.subscribe(t) // Flush 在订阅全部来源后发生；重连 GET 取得最新全量。
	current := categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodGet, getPath, nil))
	list := categoryHTTPData[op.GroupCategoryListSnapshot](t, fixture.request(t, http.MethodGet, "/api/v1/group-category/list", nil))
	expectedList := op.GroupCategoryList()
	for i := range expectedList.Items {
		expectedList.Items[i].NameKey = ""
	}
	if current.MetadataVersion != latest.MetadataVersion || current.CategoryID == nil || *current.CategoryID != next.ID || !reflect.DeepEqual(list, expectedList) {
		t.Fatal("reconnect GET did not align latest group and categories")
	}
}

func categoryGroupHealth(t *testing.T, id int) relay.HealthSnapshot {
	t.Helper()
	states, stream := relay.OpenHealthStream()
	relay.CloseHealthStream(stream)
	for _, state := range states {
		if state.GroupID == id {
			return state
		}
	}
	t.Fatalf("group %d has no health state", id)
	return relay.HealthSnapshot{}
}

func TestGroupCategoryHTTPKeepsInFlightHealthAndCooldowns(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	arrived, respond := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(arrived)
			select {
			case <-respond:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"fixture-quota","message":"Fixture quota reached"}}`)
	}))
	t.Cleanup(upstream.Close)
	detail, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("w08-http-relay-%d", channelEventTestID.Add(1)), Enabled: true, BaseURL: upstream.URL,
		CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 300, Scope: model.CooldownScopeModel}}},
		Keys: []model.ChannelKeyConfig{{Name: "a", Key: "fixture-a", Enabled: true}, {Name: "b", Key: "fixture-b", Enabled: true}}, Models: []string{"fixture-model-a"},
		Grants: []model.ChannelGrantConfig{{ModelName: "fixture-model-a", KeyName: "a", Protocols: model.ProtocolAll}, {ModelName: "fixture-model-a", KeyName: "b", Protocols: model.ProtocolAll}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.ChannelDel(detail.ID, context.Background()) })
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == detail.ID {
			fixture.grants = append(fixture.grants, grant.ID)
		}
	}
	fixture.groups = []model.Group{fixture.newGroup(t), fixture.newGroup(t)}
	for _, group := range fixture.groups {
		t.Cleanup(func() { _ = op.GroupDel(group.ID, context.Background()); relay.ResetRouteState(group.ID) })
	}
	done := make(chan struct{})
	go func() { defer close(done); fixture.fail(t, fixture.groups[0]) }()
	t.Cleanup(func() {
		once.Do(func() { close(respond) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("in-flight fixture did not stop")
		}
	})
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream was not reached")
	}
	fixture.fail(t, fixture.groups[1]) // 同渠道第二个请求建立共享冷却，第一个仍在途。
	beforeHealth := []relay.HealthSnapshot{categoryGroupHealth(t, fixture.groups[0].ID), categoryGroupHealth(t, fixture.groups[1].ID)}
	beforeRoute := []relay.RouteState{relay.RouteStateOf(fixture.groups[0]), relay.RouteStateOf(fixture.groups[1])}
	beforeCooldown := relay.ChannelCooldownStateOf(detail.ID)
	beforeConfig, err := op.ChannelRelaySnapshot(fixture.grants[0])
	if err != nil || len(beforeCooldown.RuleCooldowns) != 1 {
		t.Fatalf("fixture did not establish shared cooldown: %v", err)
	}
	category := createCategoryHTTP(t, fixture, "W08 In Flight")
	for _, group := range fixture.groups {
		fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group/update/%d", group.ID), map[string]any{"category_id": category.ID})
	}
	fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group-category/update/%d", category.ID), map[string]string{"name": "W08 In Flight Renamed"})
	fixture.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/group-category/delete/%d", category.ID), nil)
	for i, group := range fixture.groups {
		current, err := op.GroupGet(group.ID)
		if err != nil || current.CategoryID != nil || current.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(current.Items, group.Items) || !reflect.DeepEqual(relay.RouteStateOf(current), beforeRoute[i]) || !reflect.DeepEqual(categoryGroupHealth(t, group.ID), beforeHealth[i]) {
			t.Fatalf("HTTP category operation changed route/health/selection: %v", err)
		}
	}
	afterConfig, _ := op.ChannelRelaySnapshot(fixture.grants[0])
	if afterConfig.Generation != beforeConfig.Generation || !reflect.DeepEqual(relay.ChannelCooldownStateOf(detail.ID), beforeCooldown) {
		t.Fatal("HTTP category operation changed shared cooldown generation or hits")
	}
	once.Do(func() { close(respond) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("late result did not settle")
	}
	afterHealth := categoryGroupHealth(t, fixture.groups[0].ID)
	if afterHealth.Members[fixture.groups[0].ActiveItemID].FailureStreak != 1 || calls.Load() != 2 || len(relay.ChannelCooldownStateOf(detail.ID).RuleCooldowns) != 1 {
		t.Fatal("category operation invalidated late failure or replayed request")
	}
}

func TestGroupCategoryHTTPImportPublishesSnapshots(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	events, _ := fixture.subscribe(t)
	// 记录每条同步发布，避免把完整包中的其它分组造成的 SSE 拥塞误判为漏发。
	// 真实 HTTP 连接仍使用生产缓冲；下面单独验证事件或断线重连后的全量对齐。
	published := make(chan groupEvent, len(op.GroupList())+2)
	groupEventMu.Lock()
	groupEventStreams[published] = struct{}{}
	groupEventMu.Unlock()
	t.Cleanup(func() {
		groupEventMu.Lock()
		defer groupEventMu.Unlock()
		if _, exists := groupEventStreams[published]; exists {
			delete(groupEventStreams, published)
			close(published)
		}
	})
	const categoryID, groupID = 886071, 886501
	ref := categoryID
	dump := &model.DBDump{Product: "octopus-roam", Version: 1002, GroupCategories: []model.BackupGroupCategory{{ID: categoryID, Name: "W08 HTTP Imported"}}, Groups: []model.Group{{ID: groupID, Name: "w08-http-imported", CategoryID: &ref}}}
	t.Cleanup(func() {
		_ = op.GroupDel(groupID, context.Background())
		relay.ResetRouteState(groupID)
		_, _ = op.GroupCategoryDelete(categoryID, context.Background())
	})
	encrypted, err := op.EncryptBackup(dump, "w08-import-password")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/import", middleware.Auth(), importDB)
	request := backupRequest(t, encrypted, "w08-import-password")
	request.AddCookie(&http.Cookie{Name: "auth", Value: fixture.token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("encrypted import returned %d", response.Code)
	}
	wantCategories, wantGroups := op.GroupCategoryList(), op.GroupList()
	if len(published) != len(wantGroups)+1 {
		t.Fatalf("import published %d events, want categories and %d groups", len(published), len(wantGroups))
	}
	categoryEvent := <-published
	if categoryEvent.Name != "categories" || !reflect.DeepEqual(categoryEvent.Data, wantCategories) {
		t.Fatal("import omitted the complete versioned categories event")
	}
	for _, want := range wantGroups {
		event := <-published
		group, ok := event.Data.(groupResponse)
		if event.Name != "changed" || !ok || !reflect.DeepEqual(group.Group, want) || group.MetadataVersion != want.MetadataVersion || group.MetadataVersion == "" {
			t.Fatalf("import omitted the complete versioned snapshot for group %d", want.ID)
		}
	}
	wantGroup, err := op.GroupGet(groupID)
	if err != nil || wantGroup.CategoryID == nil || *wantGroup.CategoryID != categoryID {
		t.Fatalf("import did not restore the assignment: %v", err)
	}
	for i := range wantCategories.Items {
		wantCategories.Items[i].NameKey = "" // 内部自然键不出现在 HTTP JSON。
	}
	checkGroup := func(group groupResponse) {
		if group.ID != groupID || group.CategoryID == nil || *group.CategoryID != categoryID || group.MetadataVersion != wantGroup.MetadataVersion {
			t.Fatal("import snapshot lost assignment/version")
		}
	}
	checkCategories := func(categories op.GroupCategoryListSnapshot) {
		if !reflect.DeepEqual(categories, wantCategories) {
			t.Fatal("import snapshot lost the complete categories list/version")
		}
	}
	seenCategories, seenGroup := false, false
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !seenCategories || !seenGroup {
		select {
		case event, ok := <-events:
			if !ok {
				t.Logf("import SSE overflow with %d groups; verifying reconnect GET", len(wantGroups))
				_, _ = fixture.subscribe(t)
				checkCategories(categoryHTTPData[op.GroupCategoryListSnapshot](t, fixture.request(t, http.MethodGet, "/api/v1/group-category/list", nil)))
				checkGroup(categoryHTTPData[groupResponse](t, fixture.request(t, http.MethodGet, fmt.Sprintf("/api/v1/group/get/%d", groupID), nil)))
				return
			}
			switch event.name {
			case "categories":
				var categories op.GroupCategoryListSnapshot
				if err := json.Unmarshal(event.data, &categories); err != nil {
					t.Fatal(err)
				}
				checkCategories(categories)
				seenCategories = true
			case "changed":
				var group groupResponse
				if err := json.Unmarshal(event.data, &group); err != nil {
					t.Fatal(err)
				}
				if group.ID == groupID {
					checkGroup(group)
					seenGroup = true
				}
			}
		case <-deadline.C:
			t.Fatal("import SSE omitted snapshots without closing for reconnect")
		}
	}
}

func TestGroupCategorySlowSubscriberClosesWithVersionedEmptyList(t *testing.T) {
	fixture := newCategoryHTTPFixture(t)
	category := createCategoryHTTP(t, fixture, "W08 Slow Subscriber")
	stream := subscribeGroupEventForTest(t)
	for i := range groupEventBuffer + 1 {
		fixture.request(t, http.MethodPost, fmt.Sprintf("/api/v1/group-category/update/%d", category.ID), map[string]string{"name": fmt.Sprintf("W08 Slow Subscriber %d", i)})
	}
	groupEventMu.Lock()
	_, registered := groupEventStreams[stream]
	groupEventMu.Unlock()
	if registered {
		t.Fatal("slow category subscriber was not disconnected")
	}
	var first, last op.GroupCategoryListSnapshot
	for event := range stream {
		value, ok := event.Data.(op.GroupCategoryListSnapshot)
		if event.Name != "categories" || !ok {
			t.Fatal("category event shape changed")
		}
		if first.Version == "" {
			first = value
		}
		last = value
	}
	if !categoryVersionNewer(t, last.Version, first.Version) {
		t.Fatal("buffered events lost distinct immutable versions")
	}
	before := op.GroupCategoryList()
	fixture.request(t, http.MethodDelete, fmt.Sprintf("/api/v1/group-category/delete/%d", category.ID), nil)
	after := categoryHTTPData[op.GroupCategoryListSnapshot](t, fixture.request(t, http.MethodGet, "/api/v1/group-category/list", nil))
	if !categoryVersionNewer(t, after.Version, before.Version) || slices.ContainsFunc(after.Items, func(c model.GroupCategory) bool { return c.ID == category.ID }) {
		t.Fatal("GET did not recover deleted category after overflow")
	}
	// 本用例只清理自己的分类；若此时集合为空，空数组也必须有有效版本。
	encoded, _ := json.Marshal(after)
	if after.Items == nil || bytes.Contains(encoded, []byte(`"items":null`)) {
		t.Fatal("empty category list was not an array")
	}
}
