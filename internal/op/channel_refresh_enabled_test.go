package op

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func assertEnabledOnlySkipped(t *testing.T, result model.ChannelModelRefreshResult, reason model.ChannelModelRefreshSkipReason) {
	t.Helper()
	if result.ChannelID <= 0 || result.Success || !result.Skipped || result.SkipReason != reason || result.Error != "" || result.ZeroModels {
		t.Fatalf("unexpected skip: %+v, want %s", result, reason)
	}
	if result.AddedModels == nil || result.RemovedModels == nil || result.KeyResults == nil ||
		len(result.AddedModels)+len(result.RemovedModels)+len(result.KeyResults)+len(result.Warnings) != 0 {
		t.Fatalf("skip exposed partial probe results: %+v", result)
	}
}

func setEnabledOnlyProtocols(t *testing.T, id int, protocols model.Protocol) {
	t.Helper()
	detail, err := ChannelDetailGet(id)
	if err != nil {
		t.Fatal(err)
	}
	detail.AllowedProtocols = protocols
	for i := range detail.Grants {
		detail.Grants[i].Protocols = protocols
	}
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
}

func seedEnabledOnlyDiagnostic(t *testing.T, id int) *model.ChannelRefreshStatus {
	t.Helper()
	record := model.ChannelRefreshRecord{
		ChannelID: id, StartedAt: time.Unix(1, 0), FinishedAt: time.Unix(2, 0),
		Source: "manual", Status: "success", BeforeCount: 1, AfterCount: 1,
		Result: model.ChannelModelRefreshResult{
			ChannelID: id, Success: true, AddedModels: []string{}, RemovedModels: []string{},
			KeyResults: []model.ChannelModelRefreshKeyResult{},
		},
	}
	if err := saveChannelRefreshRecord(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return ChannelRefreshStatus(id)
}

func TestRefreshChannelModelsEnabledOnlyMixedAndInactive(t *testing.T) {
	var sends atomic.Int64
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		sends.Add(1)
		return http.StatusOK, `{"data":[{"id":"fixture-model-a"}]}`
	})
	newChannel := func() int {
		id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "primary", Key: "fixture-key", Enabled: true}})
		return id
	}
	enabled, disabled, deleted := newChannel(), newChannel(), newChannel()
	previous := seedEnabledOnlyDiagnostic(t, disabled)
	if err := ChannelEnabled(disabled, false, t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := ChannelDel(deleted, t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		id     int
		reason model.ChannelModelRefreshSkipReason
	}{{enabled, ""}, {disabled, model.ChannelRefreshSkipDisabled}, {deleted, model.ChannelRefreshSkipDeleted}} {
		result, err := RefreshChannelModelsWithClient(t.Context(), target.id, model.ChannelModelRefreshOptions{EnabledOnly: true}, newRefreshHTTPClient(t))
		if err != nil {
			t.Fatal(err)
		}
		if target.reason == "" {
			if !result.Success || result.Skipped {
				t.Fatalf("enabled channel failed: %+v", result)
			}
		} else {
			assertEnabledOnlySkipped(t, result, target.reason)
		}
	}
	if sends.Load() != 2 {
		t.Fatalf("mixed batch sent %d probes, want two protocols for the enabled channel only", sends.Load())
	}
	if !reflect.DeepEqual(previous, ChannelRefreshStatus(disabled)) {
		t.Fatal("disabled skip replaced the existing diagnostic")
	}
	sends.Store(0)
	for range 2 {
		result, err := RefreshChannelModelsWithClient(t.Context(), disabled, model.ChannelModelRefreshOptions{EnabledOnly: true}, newRefreshHTTPClient(t))
		if err != nil {
			t.Fatal(err)
		}
		assertEnabledOnlySkipped(t, result, model.ChannelRefreshSkipDisabled)
	}
	if sends.Load() != 0 {
		t.Fatal("all-disabled work sent a probe")
	}
	t.Log("actual sends: mixed=2 (enabled channel only), all-disabled=0, deleted=0")
}

func TestRefreshChannelModelsEnabledOnlyStopsAfterInFlightProbe(t *testing.T) {
	for _, remove := range []bool{false, true} {
		for _, keyCount := range []int{1, 2} {
			t.Run(fmt.Sprintf("delete=%t/keys=%d", remove, keyCount), func(t *testing.T) {
				var id atomic.Int64
				var sends atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					sends.Add(1)
					var err error
					if remove {
						err = ChannelDel(int(id.Load()), t.Context())
					} else {
						err = ChannelEnabled(int(id.Load()), false, t.Context())
					}
					if err != nil {
						t.Errorf("change in-flight channel: %v", err)
					}
					_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-a"}]}`)
				}))
				defer server.Close()
				keys := []model.ChannelKeyConfig{{Name: "a", Key: "fixture-key-a", Enabled: true}}
				if keyCount == 2 {
					keys = append(keys, model.ChannelKeyConfig{Name: "b", Key: "fixture-key-b", Enabled: true})
				}
				channelID, keyIDs := newRefreshChannel(t, server.URL, keys)
				id.Store(int64(channelID))
				setEnabledOnlyProtocols(t, channelID, model.ProtocolOpenAIResponse)
				group, _ := addRefreshGroup(t, channelID, keyIDs["a"])
				before, _ := ChannelDetailGet(channelID)
				previous := seedEnabledOnlyDiagnostic(t, channelID)
				result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{EnabledOnly: true, AutoAddGroups: true}, newRefreshHTTPClient(t))
				if err != nil {
					t.Fatal(err)
				}
				reason := model.ChannelRefreshSkipDisabled
				if remove {
					reason = model.ChannelRefreshSkipDeleted
				}
				assertEnabledOnlySkipped(t, result, reason)
				if sends.Load() != 1 {
					t.Fatalf("sent %d requests; later keys must not be probed", sends.Load())
				}
				status := ChannelRefreshStatus(channelID)
				if status.Running {
					t.Fatal("skip leaked the running flag")
				}
				afterGroup, err := GroupGet(group.ID)
				if err != nil {
					t.Fatal("channel refresh deleted a group entity", err)
				}
				if remove {
					if status.Last != nil || len(afterGroup.Items) != 0 {
						t.Fatal("deleted channel left an orphan diagnostic or group member")
					}
				} else {
					after, err := ChannelDetailGet(channelID)
					if err != nil || !reflect.DeepEqual(before.Models, after.Models) || !reflect.DeepEqual(before.Grants, after.Grants) {
						t.Fatal("disabled in-flight refresh committed partial models or grants", err)
					}
					expectedItems := append([]model.GroupItem(nil), group.Items...)
					for i := range expectedItems {
						expectedItems[i].Available = false
					}
					if !reflect.DeepEqual(previous, status) || !reflect.DeepEqual(expectedItems, afterGroup.Items) || group.ActiveItemID != afterGroup.ActiveItemID {
						t.Fatal("skip changed the existing diagnostic, group members, or selection")
					}
				}
				var partial int64
				if err := db.GetDB().Model(&model.ChannelModel{}).Where("channel_id = ? AND name = ?", channelID, "fixture-model-a").Count(&partial).Error; err != nil || partial != 0 {
					t.Fatal("partial model reached the database", err)
				}
				t.Logf("actual sends=1, skip=%s, later-key sends=0, partial model rows=0", reason)
			})
		}
	}
}

func TestRefreshChannelModelsEnabledOnlyStopsPaginationAndRedirects(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(fmt.Sprintf("redirect=%t", redirect), func(t *testing.T) {
			var id atomic.Int64
			var sends atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sends.Add(1)
				if r.URL.Path != "/v1/models" || r.URL.Query().Get("after_id") != "" {
					t.Error("a disabled channel received a later-page or redirect probe")
				}
				if err := ChannelEnabled(int(id.Load()), false, t.Context()); err != nil {
					t.Error(err)
				}
				if redirect {
					http.Redirect(w, r, "/redirect-models", http.StatusFound)
				} else {
					_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-a"}],"has_more":true,"last_id":"fixture-page-one"}`)
				}
			}))
			defer server.Close()
			channelID, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "primary", Key: "fixture-key", Enabled: true}})
			id.Store(int64(channelID))
			protocols := model.ProtocolAnthropicMessage
			if redirect {
				protocols = model.ProtocolOpenAIResponse
			}
			setEnabledOnlyProtocols(t, channelID, protocols)
			result, err := RefreshChannelModelsWithClient(t.Context(), channelID, model.ChannelModelRefreshOptions{EnabledOnly: true}, newRefreshHTTPClient(t))
			if err != nil {
				t.Fatal(err)
			}
			assertEnabledOnlySkipped(t, result, model.ChannelRefreshSkipDisabled)
			if sends.Load() != 1 {
				t.Fatalf("actual sends=%d, want initial request only", sends.Load())
			}
			detail, err := ChannelDetailGet(channelID)
			if err != nil || len(detail.Models) != 1 || detail.Models[0] != "old-model" {
				t.Fatal("pagination/redirect skip committed partial models", err)
			}
			t.Log("actual sends=1; follow-up page/redirect sends=0; old model preserved")
		})
	}
}

func TestRefreshChannelModelsEnabledOnlyChecksBothProtocolsAndKeepsSkip(t *testing.T) {
	for _, firstAnthropic := range []bool{false, true} {
		t.Run(fmt.Sprintf("first_anthropic=%t", firstAnthropic), func(t *testing.T) {
			var sends atomic.Int64
			server := newModelListServer(t, func(_ string, _ bool) (int, string) {
				sends.Add(1)
				return http.StatusOK, `{"data":[]}`
			})
			id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "primary", Key: "fixture-key", Enabled: true}})
			detail, _ := ChannelDetailGet(id)
			client := newRefreshHTTPClient(t)
			guard := &channelRefreshEnabledGuard{channelID: id, next: client.Transport}
			client.Transport = guard
			probe := func(anthropic bool) error {
				var err error
				if anthropic {
					_, err = fetchAnthropicChannelModels(client, t.Context(), detail.ChannelConfig, "fixture-key", server.URL+"/v1/models")
				} else {
					_, err = fetchOpenAIChannelModels(client, t.Context(), detail.ChannelConfig, "fixture-key", server.URL+"/v1/models")
				}
				return err
			}
			if err := probe(firstAnthropic); err != nil {
				t.Fatal(err)
			}
			if err := ChannelEnabled(id, false, t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := probe(!firstAnthropic); err == nil {
				t.Fatal("second protocol ignored the latest disabled state")
			}
			if err := ChannelEnabled(id, true, t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := probe(firstAnthropic); err == nil || sends.Load() != 1 {
				t.Fatal("re-enable restarted a partially skipped round")
			}
			t.Log("actual sends=1; other protocol and re-enabled partial round sent 0")
		})
	}
}

func TestRefreshChannelModelsEnabledOnlyBusyKeepsDiagnostic(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	var sends atomic.Int64
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		sends.Add(1)
		close(started)
		<-release
		return http.StatusOK, `{"data":[]}`
	})
	defer finish()
	id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "primary", Key: "fixture-key", Enabled: true}})
	setEnabledOnlyProtocols(t, id, model.ProtocolOpenAIResponse)
	previous := seedEnabledOnlyDiagnostic(t, id)
	done := make(chan error, 1)
	go func() {
		result, err := RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{}, newRefreshHTTPClient(t))
		if err == nil && !result.Success {
			err = fmt.Errorf("first refresh failed: %s", result.Error)
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not reach the upstream")
	}
	result, err := RefreshChannelModelsWithClient(t.Context(), id, model.ChannelModelRefreshOptions{EnabledOnly: true}, newRefreshHTTPClient(t))
	if err != nil {
		t.Fatal(err)
	}
	assertEnabledOnlySkipped(t, result, model.ChannelRefreshSkipInProgress)
	status := ChannelRefreshStatus(id)
	if !status.Running || !reflect.DeepEqual(previous.Last, status.Last) || sends.Load() != 1 {
		t.Fatal("busy skip changed the running task or its diagnostic")
	}
	finish()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first refresh did not complete")
	}
	t.Log("actual sends=1; busy duplicate sends=0; original task completed")
}
