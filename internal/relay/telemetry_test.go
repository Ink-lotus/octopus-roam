package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func TestRequestTelemetryTracksRetryLifecycle(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "telemetry", log, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 2
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	request := chatRequest(t, groupName, false)
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, request)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("遥测重试请求未在 10 秒内完成")
	}

	var states []RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 在完成前关闭")
			}
			if state.GroupID == group.ID {
				states = append(states, state)
			}
		default:
			goto drained
		}
	}

drained:
	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var sawSelected, sawFirstAttempt, sawFailure, sawBackoff, sawSecondAttempt bool
	var final RequestState
	for _, state := range states {
		if state.TargetChannel != "" && state.ChannelStatus == ChannelStatusNormal && state.Attempt == 0 {
			sawSelected = true
		}
		if state.Attempt == 1 && state.RetryCount == 0 && state.AttemptLimit == 2 && state.Sending {
			sawFirstAttempt = true
		}
		if state.ErrorCount == 1 && state.LastError != "" && state.ChannelStatus == ChannelStatusError {
			sawFailure = true
		}
		if state.Retrying && state.ChannelStatus == ChannelStatusWaiting && state.NextRetryAt > state.StartedAt.UnixMilli() {
			sawBackoff = true
		}
		if state.Attempt == 2 && state.RetryCount == 1 && state.Sending {
			sawSecondAttempt = true
		}
		if state.Status == StatusSuccess {
			final = state
		}
	}
	if !sawSelected || !sawFirstAttempt || !sawFailure || !sawBackoff || !sawSecondAttempt {
		t.Fatalf("状态流缺少完整重试迁移: selected=%t first=%t failure=%t backoff=%t second=%t; states=%+v",
			sawSelected, sawFirstAttempt, sawFailure, sawBackoff, sawSecondAttempt, states)
	}
	if final.Status != StatusSuccess || final.Attempt != 2 || final.RetryCount != 1 || final.ErrorCount != 1 {
		t.Fatalf("终态遥测不一致: %+v", final)
	}
	if final.ChannelStatus != ChannelStatusNormal || final.Retrying || final.NextRetryAt != 0 || final.LastError == "" {
		t.Fatalf("成功终态应清除等待并保留最近错误诊断: %+v", final)
	}

	encoded, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("编码请求状态失败: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("解析请求状态 JSON 失败: %v", err)
	}
	for _, field := range []string{"id", "status", "started_at", "model", "round", "target_channel", "target_channel_key_name", "api_key_name", "sending"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("兼容性字段 %q 缺失", field)
		}
	}
	for _, field := range []string{"retry_count", "attempt", "attempt_limit", "retrying", "error_count", "last_error", "next_retry_at", "channel_status"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("遥测字段 %q 缺失", field)
		}
	}
}

func TestRequestTelemetryFinalFailure(t *testing.T) {
	upstream := newFakeUpstream(t, "telemetry-failure", &upstreamCallLog{}, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(1)
	config.ManualMaxAttempts = 1
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("最终状态码 = %d, 期望 503; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var final RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 提前关闭")
			}
			if state.GroupID == group.ID && state.Status == StatusFailed {
				final = state
			}
		default:
			if final.Status != StatusFailed {
				t.Fatal("状态流中未找到最终失败快照")
			}
			if final.Attempt != 1 || final.RetryCount != 0 || final.AttemptLimit != 1 || final.ErrorCount != 1 {
				t.Fatalf("最终失败计数不一致: %+v", final)
			}
			if final.ChannelStatus != ChannelStatusError || final.Retrying || final.NextRetryAt != 0 || final.LastError == "" {
				t.Fatalf("最终失败状态不一致: %+v", final)
			}
			return
		}
	}
}

func TestRequestTelemetryResetsAttemptsWhenMemberChanges(t *testing.T) {
	first := newFakeUpstream(t, "telemetry-first", &upstreamCallLog{}, http.StatusInternalServerError)
	second := newFakeUpstream(t, "telemetry-second", &upstreamCallLog{})
	firstGrant := newTestChannel(t, testName(t, "channel-first"), first.server.URL, "first-model")
	secondChannel := testName(t, "channel-second")
	secondGrant := newTestChannel(t, secondChannel, second.server.URL, "second-model")
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), firstGrant, secondGrant)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var switched, final RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 提前关闭")
			}
			if state.GroupID != group.ID {
				continue
			}
			if state.TargetChannel == secondChannel && state.Attempt == 0 && state.ChannelStatus == ChannelStatusNormal {
				switched = state
			}
			if state.Status == StatusSuccess {
				final = state
			}
		default:
			if switched.TargetChannel == "" {
				t.Fatal("状态流没有发布切换到第二个成员的快照")
			}
			if switched.ErrorCount != 1 || switched.AttemptLimit != 1 {
				t.Fatalf("成员切换快照应保留累计错误并重置成员尝试: %+v", switched)
			}
			if final.Status != StatusSuccess || final.TargetChannel != secondChannel || final.Attempt != 1 || final.RetryCount != 0 {
				t.Fatalf("切换成员后的终态计数不一致: %+v", final)
			}
			return
		}
	}
}

// telemetryChannel creates named synthetic credentials without changing the shared fixtures.
func telemetryChannel(t *testing.T, url string, keys ...model.ChannelKeyConfig) (int, []int) {
	t.Helper()
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: testName(t, "channel"), Enabled: true, BaseURL: url},
		Keys:          keys, Models: []string{"upstream"},
	}
	for _, key := range keys {
		detail.Grants = append(detail.Grants, model.ChannelGrantConfig{ModelName: "upstream", KeyName: key.Name, Protocols: model.ProtocolOpenAIChatCompletion})
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	grants := make([]int, len(keys))
	for i, key := range keys {
		for _, candidate := range op.ChannelGrantCandidates() {
			if candidate.ChannelID == created.ID && candidate.KeyName == key.Name {
				grants[i] = candidate.ID
			}
		}
		if grants[i] == 0 {
			t.Fatal("synthetic key has no grant")
		}
	}
	return created.ID, grants
}

func telemetryStates(t *testing.T, stream <-chan RequestState, groupID int) []RequestState {
	t.Helper()
	var states []RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("telemetry stream overflowed")
			}
			if state.GroupID == groupID {
				states = append(states, state)
			}
		default:
			return states
		}
	}
}

func telemetrySnapshot(t *testing.T, groupID int) RequestState {
	t.Helper()
	snapshot, stream := OpenRequestStream()
	CloseRequestStream(stream)
	for _, state := range snapshot {
		if state.GroupID == groupID {
			return state
		}
	}
	t.Fatal("request missing from initial log snapshot")
	return RequestState{}
}

func assertTelemetryNames(t *testing.T, state RequestState, channel, keyName, clientName string, secrets ...string) {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{"target_channel": channel, "target_channel_key_name": keyName, "api_key_name": clientName} {
		var got string
		if err := json.Unmarshal(fields[field], &got); err != nil || got != want {
			t.Errorf("%s does not preserve the expected name snapshot", field)
		}
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(string(encoded), secret) {
			t.Error("public telemetry contains a synthetic credential value")
		}
	}
}

func TestRequestTelemetryChannelKeySnapshots(t *testing.T) {
	for _, sameChannel := range []bool{true, false} {
		for _, streaming := range []bool{false, true} {
			name := "cross-channel"
			if sameChannel {
				name = "same-channel"
			}
			if streaming {
				name += "/stream"
			} else {
				name += "/non-stream"
			}
			t.Run(name, func(t *testing.T) {
				keys := []model.ChannelKeyConfig{
					{Name: `主 "Key" & <备用> · : 🔑`, Key: "s4-synthetic-upstream-a", Enabled: true},
					{Name: strings.Repeat("备用-long-name-", 30), Key: "s4-synthetic-upstream-b", Enabled: true},
				}
				calls := &upstreamCallLog{}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Header.Get("Authorization") {
					case "Bearer " + keys[0].Key:
						calls.add("first")
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic key failure"}}`)
					case "Bearer " + keys[1].Key:
						calls.add("second")
						if streaming {
							w.Header().Set("Content-Type", "text/event-stream")
							_, _ = io.WriteString(w, chatCompletionStreamBody)
						} else {
							_, _ = io.WriteString(w, chatCompletionBody)
						}
					default:
						calls.add("unexpected")
						w.WriteHeader(http.StatusUnauthorized)
					}
				}))
				defer upstream.Close()
				var channelIDs, grants []int
				if sameChannel {
					id, ids := telemetryChannel(t, upstream.URL, keys...)
					channelIDs, grants = []int{id}, ids
				} else {
					for _, key := range keys {
						id, ids := telemetryChannel(t, upstream.URL, key)
						channelIDs = append(channelIDs, id)
						grants = append(grants, ids...)
					}
				}
				groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), grants...)
				group, err := op.GroupGetByName(groupName)
				if err != nil {
					t.Fatal(err)
				}
				client := model.APIKey{Name: testName(t, "client-key"), APIKey: "s4-synthetic-client-credential", Enabled: true}
				if err := op.APIKeyCreate(&client, t.Context()); err != nil {
					t.Fatal(err)
				}
				clientName := client.Name
				_, stream := OpenRequestStream()
				defer CloseRequestStream(stream)
				engine := gin.New()
				engine.POST("/v1/chat/completions", func(c *gin.Context) {
					c.Set("api_key_id", client.ID)
					Forward(llm.APIFormatOpenAIChatCompletion)(c)
				})
				response := httptest.NewRecorder()
				serveWithTimeout(t, engine, response, chatRequest(t, groupName, streaming))
				if response.Code != http.StatusOK || !slices.Equal(calls.snapshot(), []string{"first", "second"}) {
					t.Fatal("request did not fail over between the two actual credentials")
				}
				states := telemetryStates(t, stream, group.ID)
				var first, failure, selected, second, final RequestState
				for _, state := range states {
					assertTelemetryNames(t, state, state.TargetChannel, state.TargetChannelKeyName, clientName, keys[0].Key, keys[1].Key, client.APIKey)
					if state.Round == 1 && state.Sending {
						first = state
					}
					if state.Round == 1 && state.Error != "" {
						failure = state
					}
					if state.Round == 1 && state.Attempt == 0 && state.ChannelStatus == ChannelStatusNormal {
						selected = state
					}
					if state.Round == 2 && state.Sending {
						second = state
					}
					if state.Status == StatusSuccess {
						final = state
					}
				}
				firstChannel, lastChannel := group.Items[0].ChannelName, group.Items[1].ChannelName
				if first.Round != 1 || failure.Error == "" || selected.Round != 1 || second.Round != 2 || final.Status != StatusSuccess {
					t.Fatal("missing a sent, failed, selected, retried, or final lifecycle event")
				}
				assertTelemetryNames(t, first, firstChannel, keys[0].Name, clientName)
				assertTelemetryNames(t, failure, firstChannel, keys[0].Name, clientName)
				assertTelemetryNames(t, selected, lastChannel, "", clientName)
				assertTelemetryNames(t, second, lastChannel, keys[1].Name, clientName)
				assertTelemetryNames(t, final, lastChannel, keys[1].Name, clientName)
				if final.Round != 2 || final.Attempt != 1 || final.RetryCount != 0 || final.ErrorCount != 1 {
					t.Fatal("key names changed the existing attempt accounting")
				}

				// Renaming/deleting configuration after completion must not rewrite log history.
				client.Name += "-renamed"
				if err := op.APIKeyUpdate(&client, t.Context()); err != nil {
					t.Fatal(err)
				}
				for _, id := range channelIDs {
					detail, err := op.ChannelDetailGet(id)
					if err != nil {
						t.Fatal(err)
					}
					detail.Name += "-renamed"
					for i := range detail.Keys {
						detail.Keys[i].Name += "-renamed"
					}
					for i := range detail.Grants {
						detail.Grants[i].KeyName += "-renamed"
					}
					if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
						t.Fatal(err)
					}
					assertTelemetryNames(t, telemetrySnapshot(t, group.ID), lastChannel, keys[1].Name, clientName)
					if err := op.ChannelDel(id, t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				assertTelemetryNames(t, telemetrySnapshot(t, group.ID), lastChannel, keys[1].Name, clientName)
				assertTelemetryNames(t, first, firstChannel, keys[0].Name, clientName)
				assertTelemetryNames(t, failure, firstChannel, keys[0].Name, clientName)
			})
		}
	}
}

func TestRequestTelemetryQueuedKeyUpdate(t *testing.T) {
	for _, change := range []string{"credential", "name-and-credential"} {
		t.Run(change, func(t *testing.T) {
			const oldKey = "s4-synthetic-queued-old"
			const newKey = "s4-synthetic-queued-new"
			calls := &upstreamCallLog{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "Bearer "+newKey {
					calls.add("updated")
				} else {
					calls.add("stale")
				}
				_, _ = io.WriteString(w, chatCompletionBody)
			}))
			defer upstream.Close()
			id, grants := telemetryChannel(t, upstream.URL, model.ChannelKeyConfig{Name: "queued-before", Key: oldKey, Enabled: true})
			setConcurrency(t, id, 1)
			groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grants...)
			group, err := op.GroupGetByName(groupName)
			if err != nil {
				t.Fatal(err)
			}
			original, err := op.ChannelGrantGet(grants[0])
			if err != nil {
				t.Fatal(err)
			}
			release, err := acquireChannelCapacity(t.Context(), id, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			request := chatRequest(t, groupName, false).WithContext(ctx)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); newRelayEngine().ServeHTTP(response, request) }()
			defer func() { cancel(); release(); <-done }()
			awaitCapacity(t, id, 1, 1)
			queued := telemetrySnapshot(t, group.ID)
			if queued.Round != 0 || queued.Attempt != 0 || queued.TargetChannelKeyName != "" || len(calls.snapshot()) != 0 {
				t.Fatal("queued request prematurely reported a sent key")
			}
			wantName := original.ChannelKey.Name
			if change == "credential" {
				detail, err := op.ChannelDetailGet(id)
				if err != nil {
					t.Fatal(err)
				}
				detail.Keys[0].Key = newKey
				if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				// The edit API addresses keys by name and replaces renamed IDs. Preserve this
				// synthetic legacy ID so the queued grant specifically exercises the fresh read.
				wantName = `出队 "Key" & <名称> · 🔑`
				if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", original.ChannelKey.ID).
					Updates(map[string]any{"name": wantName, "key": newKey}).Error; err != nil {
					t.Fatal(err)
				}
				if err := op.InitCache(); err != nil {
					t.Fatal(err)
				}
			}
			release()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("queued request did not finish after its key update")
			}
			final := telemetrySnapshot(t, group.ID)
			if response.Code != http.StatusOK || final.Status != StatusSuccess || final.Round != 1 || !slices.Equal(calls.snapshot(), []string{"updated"}) {
				t.Fatal("queued request did not send exactly once with the fresh credential")
			}
			assertTelemetryNames(t, final, group.Items[0].ChannelName, wantName, "", oldKey, newKey)
			if original.ChannelKey.Name != "queued-before" || original.ChannelKey.Key != oldKey {
				t.Fatal("previously read grant was mutated instead of replaced")
			}
			awaitCapacity(t, id, 0, 0)
		})
	}
}

func TestRequestTelemetryEmptyKeyName(t *testing.T) {
	upstream := newFakeUpstream(t, "unnamed", &upstreamCallLog{})
	id, grants := telemetryChannel(t, upstream.server.URL, model.ChannelKeyConfig{Name: "legacy-name", Key: "s4-synthetic-unnamed", Enabled: true})
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grants...)
	// Normal writes require a name; an unnamed legacy row must still produce valid telemetry.
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("channel_id = ?", id).Update("name", "").Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	serveWithTimeout(t, newRelayEngine(), response, chatRequest(t, groupName, false))
	state := telemetrySnapshot(t, group.ID)
	if response.Code != http.StatusOK || state.Status != StatusSuccess || upstream.requestCount() != 1 {
		t.Fatal("unnamed legacy key could not complete its request")
	}
	assertTelemetryNames(t, state, group.Items[0].ChannelName, "", "", "s4-synthetic-unnamed")
}

func TestRequestTelemetryUnsentKeyName(t *testing.T) {
	for _, failure := range []string{"outbound", "request"} {
		t.Run(failure, func(t *testing.T) {
			bad := newFakeUpstream(t, "unsent", &upstreamCallLog{})
			good := newFakeUpstream(t, "sent", &upstreamCallLog{})
			first := newTestChannel(t, testName(t, "unsent"), bad.server.URL, "upstream")
			second := newTestChannel(t, testName(t, "sent"), good.server.URL, "upstream")
			if failure == "outbound" {
				breakChannelBaseURL(t, first)
			} else {
				setChannelParamOverride(t, first, "invalid-json")
			}
			name := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), first, second)
			group, err := op.GroupGetByName(name)
			if err != nil {
				t.Fatal(err)
			}
			_, stream := OpenRequestStream()
			defer CloseRequestStream(stream)
			response := httptest.NewRecorder()
			serveWithTimeout(t, newRelayEngine(), response, chatRequest(t, name, false))
			found := false
			for _, state := range telemetryStates(t, stream, group.ID) {
				if state.Round == 1 && state.Error != "" {
					found = true
					if state.TargetChannelKeyName != "" || state.Attempt != 0 {
						t.Error("local failure retained an unsent key name or attempt")
					}
				}
			}
			if !found || response.Code != http.StatusOK || bad.requestCount() != 0 || good.requestCount() != 1 {
				t.Fatal("local failure did not preserve the existing failover behavior")
			}
		})
	}
}
