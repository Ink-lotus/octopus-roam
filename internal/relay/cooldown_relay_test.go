package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestCooldownRelayFailureDirections(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, phase := range []string{"ordinary-http", "ordinary-200", "stream-http", "stream-first", "stream-startup", "stream-later"} {
			t.Run(direction.name()+"/"+phase, func(t *testing.T) {
				// Keep expiry callbacks inside this fixture; later tests replace the
				// runtime engine and subscribe to the same process-wide streams.
				useCooldownRuntimeClock(t)
				var calls atomic.Int32
				streaming := strings.HasPrefix(phase, "stream-")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var request struct{ Model string }
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "upstream-model" {
						t.Error("Relay did not send the actual upstream model")
					}
					if strings.HasSuffix(phase, "http") {
						w.Header().Set("Content-Type", "application/json")
						w.Header().Set("Retry-After", "600")
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, upstreamFailureTestBody)
						return
					}
					if phase == "ordinary-200" {
						w.Header().Set("Content-Type", "application/json")
						body := upstreamFailureEvent(direction.upstream.protocol)
						if direction.upstream.protocol == model.ProtocolOpenAIResponse {
							body = `{"id":"resp-fixture","object":"response","status":"failed","output":[],"error":{"code":"fixture_code","type":"fixture_type","message":"Fixture QUOTA reached"}}`
						}
						_, _ = io.WriteString(w, body)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					var events []string
					if phase != "stream-first" {
						events = slices.Clone(direction.upstream.prelude)
					}
					if phase == "stream-later" {
						events = append(events, direction.upstream.content)
					}
					events = append(events, upstreamFailureEvent(direction.upstream.protocol))
					_, _ = io.WriteString(w, compatSSE(events...))
				}))
				t.Cleanup(server.Close)
				config := testRelayConfig(3)
				config.ManualMaxAttempts = 1
				group := startupGroup(t, direction.upstream, model.GroupModeManual, config, server.URL)
				id := group.Items[0].ChannelID
				setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "fixture_code", Keyword: "quota", CooldownSeconds: 17, Scope: model.CooldownScopeModel})
				response := httptest.NewRecorder()
				serveWithTimeout(t, startupEngine(direction.client), response, compatRequest(t, group, direction.client.format, streaming, nil))
				hits := ChannelCooldownStateOf(id).RuleCooldowns
				state := compatState(t, group)
				if calls.Load() != 1 || len(hits) != 1 || hits[0].ModelName != "upstream-model" || hits[0].Rule.CooldownSeconds != 17 || state.Status != StatusFailed || state.Attempt != 1 {
					t.Fatalf("failure not integrated: calls=%d hits=%d status=%s attempts=%d", calls.Load(), len(hits), state.Status, state.Attempt)
				}
				if phase == "stream-later" {
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pong") {
						t.Fatal("committed output was replaced/replayed")
					}
				} else if response.Code != http.StatusServiceUnavailable {
					t.Fatalf("pre-commit error was treated as success: status=%d", response.Code)
				}
				awaitCapacity(t, id, 0, 0)
			})
		}
	}
}

func TestCooldownRelayNormalOutputDoesNotMatch(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", direction.name(), streaming), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						events := append(slices.Clone(direction.upstream.prelude), strings.ReplaceAll(direction.upstream.content, "pong", "Fixture QUOTA reached"))
						events = append(events, direction.upstream.ending...)
						_, _ = io.WriteString(w, compatSSE(events...))
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, upstreamFailureNormalBody(direction.upstream.protocol))
					}
				}))
				t.Cleanup(server.Close)
				group := startupGroup(t, direction.upstream, model.GroupModeManual, testRelayConfig(1), server.URL)
				setRuntimeCooldownRules(t, group.Items[0].ChannelID, model.CooldownRule{Keyword: "quota", CooldownSeconds: 17, Scope: model.CooldownScopeChannel})
				response := httptest.NewRecorder()
				serveWithTimeout(t, startupEngine(direction.client), response, compatRequest(t, group, direction.client.format, streaming, nil))
				if response.Code != http.StatusOK || compatState(t, group).Status != StatusSuccess || len(ChannelCooldownStateOf(group.Items[0].ChannelID).RuleCooldowns) != 0 {
					t.Fatal("normal output/prompt matched an error rule")
				}
			})
		}
	}
}

func TestCooldownRelayUnmatchedRuleKeepsRetryBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, cooldownFailureBody)
	}))
	t.Cleanup(server.Close)
	grant := newTestChannel(t, testName(t, "unmatched-rule"), server.URL, "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "503", CooldownSeconds: 30, Scope: model.CooldownScopeChannel})
	group := serveCooldownChat(t, grant, 2)
	state := compatState(t, group)
	if calls.Load() != 2 || state.Attempt != 2 || state.ErrorCount != 2 || state.Status != StatusFailed || len(ChannelCooldownStateOf(id).RuleCooldowns) != 0 {
		t.Fatal("unmatched rule changed the original retry budget/failure accounting")
	}
	awaitCapacity(t, id, 0, 0)
}

type cooldownRelayFixture struct {
	id     int
	grants map[string]int
}

func newCooldownRelayFixture(t *testing.T, url string, scope model.CooldownScope) cooldownRelayFixture {
	t.Helper()
	detail := &model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: testName(t, "shared-rule"), Enabled: true, BaseURL: url,
		CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 10, Scope: scope}}},
		Keys: []model.ChannelKeyConfig{{Name: "a", Key: "fixture-key-a", Enabled: true}, {Name: "b", Key: "fixture-key-b", Enabled: true}}, Models: []string{"fixture-model-a", "fixture-model-b"},
		Grants: []model.ChannelGrantConfig{
			{ModelName: "fixture-model-a", KeyName: "a", Protocols: model.ProtocolOpenAIChatCompletion},
			{ModelName: "fixture-model-a", KeyName: "b", Protocols: model.ProtocolAnthropicMessage},
			{ModelName: "fixture-model-b", KeyName: "a", Protocols: model.ProtocolOpenAIChatCompletion},
		}}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixture := cooldownRelayFixture{id: created.ID, grants: make(map[string]int)}
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == fixture.id {
			fixture.grants[grant.ModelName+"/"+grant.KeyName] = grant.ID
		}
	}
	return fixture
}

func serveCooldownChat(t *testing.T, grant int, attempts int) model.Group {
	t.Helper()
	config := testRelayConfig(3)
	config.ManualMaxAttempts = attempts
	group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
	response := httptest.NewRecorder()
	serveWithTimeout(t, newRelayEngine(), response, chatRequest(t, group.Name, false))
	return group
}

func TestCooldownRelayScopesAcrossKeysProtocolsAndGroups(t *testing.T) {
	for _, scope := range []model.CooldownScope{model.CooldownScopeModel, model.CooldownScopeChannel} {
		t.Run(string(scope), func(t *testing.T) {
			_, clock := useCooldownRuntimeClock(t)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, cooldownFailureBody)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(r.URL.Path, "messages") {
					_, _ = io.WriteString(w, upstreamFailureNormalBody(model.ProtocolAnthropicMessage))
				} else {
					_, _ = io.WriteString(w, chatCompletionBody)
				}
			}))
			t.Cleanup(server.Close)
			fixture := newCooldownRelayFixture(t, server.URL, scope)
			first := serveCooldownChat(t, fixture.grants["fixture-model-a/a"], 1)
			if compatState(t, first).Status != StatusFailed {
				t.Fatal("trigger request did not fail")
			}
			groupB := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), fixture.grants["fixture-model-a/b"]))
			responseB, doneB, _, updatesB := startManualWaitForward(t, groupB.Name)
			waiting := awaitManualRequestState(t, updatesB, groupB.Name, isManualWaiting)
			if waiting.Attempt != 0 || waiting.ErrorCount != 0 || calls.Load() != 1 || len(RouteStateOf(groupB).RuleCooldowns) != 1 {
				t.Fatal("another key/protocol/group bypassed shared model cooldown")
			}
			awaitCapacity(t, fixture.id, 0, 0)
			other := newTestChannel(t, testName(t, "isolated-channel"), server.URL, "fixture-model-a")
			if group := serveCooldownChat(t, other, 1); compatState(t, group).Status != StatusSuccess {
				t.Fatal("same model on another channel was blocked")
			}
			if scope == model.CooldownScopeModel {
				if group := serveCooldownChat(t, fixture.grants["fixture-model-b/a"], 1); compatState(t, group).Status != StatusSuccess {
					t.Fatal("another model on same channel was blocked")
				}
			} else {
				groupC := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), fixture.grants["fixture-model-b/a"]))
				_, doneC, cancelC, updatesC := startManualWaitForward(t, groupC.Name)
				state := awaitManualRequestState(t, updatesC, groupC.Name, isManualWaiting)
				if state.Attempt != 0 || len(RouteStateOf(groupC).RuleCooldowns) != 1 {
					t.Fatal("channel rule did not cover another model")
				}
				cancelC()
				awaitManualForwardDone(t, doneC)
			}
			clock.advance(10 * time.Second)
			awaitManualForwardDone(t, doneB)
			if responseB.Code != http.StatusOK || compatState(t, groupB).Attempt != 1 || len(ChannelCooldownStateOf(fixture.id).RuleCooldowns) != 0 {
				t.Fatal("expiry failed to wake the waiting key/protocol/group")
			}
			awaitCapacity(t, fixture.id, 0, 0)
		})
	}
}

func TestCooldownRelayQueuedAdmissionAndCancellation(t *testing.T) {
	for _, finish := range []string{"expire", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			_, clock := useCooldownRuntimeClock(t)
			arrived, respond := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if calls.Add(1) == 1 {
					close(arrived)
					select {
					case <-respond:
					case <-r.Context().Done():
						return
					}
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, cooldownFailureBody)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatCompletionBody)
			}))
			t.Cleanup(server.Close)
			grant := newTestChannel(t, testName(t, "queued-rule"), server.URL, "fixture-model-a")
			id := channelIDOfGrant(t, grant)
			setConcurrency(t, id, 1)
			setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 10, Scope: model.CooldownScopeModel})
			config := testRelayConfig(3)
			config.ManualMaxAttempts = 1
			first := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
			_, doneA, _, _ := startManualWaitForward(t, first.Name)
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("first request did not arrive")
			}
			second := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
			responseB, doneB, cancelB, updatesB := startManualWaitForward(t, second.Name)
			awaitCapacity(t, id, 1, 1)
			close(respond)
			awaitManualForwardDone(t, doneA)
			waiting := awaitManualRequestState(t, updatesB, second.Name, func(s RequestState) bool { return s.Retrying && s.NextRetryAt > 0 })
			awaitCapacity(t, id, 0, 0)
			if calls.Load() != 1 || waiting.Attempt != 0 || waiting.ErrorCount != 0 {
				t.Fatal("queued cooldown issued a request or consumed an attempt")
			}
			if finish == "cancel" {
				cancelB()
			} else {
				clock.advance(10 * time.Second)
			}
			awaitManualForwardDone(t, doneB)
			if finish == "expire" && (calls.Load() != 2 || responseB.Code != http.StatusOK || compatState(t, second).Attempt != 1) {
				t.Fatal("expired queued request did not send exactly once")
			}
			if finish == "cancel" && (calls.Load() != 1 || compatState(t, second).Status != StatusCanceled) {
				t.Fatal("canceled queued request sent or retained a waiter")
			}
			awaitCapacity(t, id, 0, 0)
		})
	}
}

func TestCooldownRelayFailoverAndManualSwitch(t *testing.T) {
	for _, mode := range []model.GroupMode{model.GroupModeFailover, model.GroupModeManual} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			engine, _ := useCooldownRuntimeClock(t)
			var firstCalls, backupCalls atomic.Int32
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				firstCalls.Add(1)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, cooldownFailureBody)
			}))
			t.Cleanup(first.Close)
			backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				backupCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatCompletionBody)
			}))
			t.Cleanup(backup.Close)
			grantA := newTestChannel(t, testName(t, "switch-first"), first.URL, "fixture-model-a")
			grantB := newTestChannel(t, testName(t, "switch-backup"), backup.URL, "fixture-model-a")
			id := channelIDOfGrant(t, grantA)
			setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 60, Scope: model.CooldownScopeChannel})
			group := manualWaitGroup(t, newTestGroup(t, mode, testRelayConfig(5), grantA, grantB))
			response, done, _, updates := startManualWaitForward(t, group.Name)
			if mode == model.GroupModeManual {
				awaitManualRequestState(t, updates, group.Name, func(s RequestState) bool { return s.Retrying && s.ErrorCount == 1 })
				if backupCalls.Load() != 0 {
					t.Fatal("manual cooldown silently switched members")
				}
				selected := group.Items[1].ID
				group = updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{ActiveItemID: &selected})
			}
			awaitManualForwardDone(t, done)
			if response.Code != http.StatusOK || firstCalls.Load() != 1 || backupCalls.Load() != 1 || len(engine.Snapshot(id)) != 1 {
				t.Fatal("rule failover/manual switch failed before original budget exhaustion")
			}
		})
	}
}

func TestCooldownRelayLateResultAfterConfiguration(t *testing.T) {
	for _, change := range []string{"rule", "clear", "credential", "connection", "name", "delete", "aba"} {
		t.Run(change, func(t *testing.T) {
			engine, _ := useCooldownRuntimeClock(t)
			arrived, respond := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					close(arrived)
					select {
					case <-respond:
					case <-r.Context().Done():
						return
					}
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, cooldownFailureBody)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, chatCompletionBody)
			}))
			t.Cleanup(server.Close)
			grant := newTestChannel(t, testName(t, "late-rule"), server.URL, "fixture-model-a")
			id := channelIDOfGrant(t, grant)
			setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel})
			config := testRelayConfig(3)
			config.ManualMaxAttempts = 1
			group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
			_, done, cancel, updates := startManualWaitForward(t, group.Name)
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("late-result fixture did not send")
			}
			detail, _ := op.ChannelDetailGet(id)
			switch change {
			case "rule", "aba":
				detail.CooldownRules[0].CooldownSeconds = 31
			case "clear":
				detail.CooldownRules = []model.CooldownRule{}
			case "credential":
				detail.Keys[0].Key = "fixture-new-credential"
			case "connection":
				detail.OpenAIChatCompletionPath = "/changed"
			case "name":
				detail.Name += " renamed"
			}
			if change == "delete" {
				if err := op.ChannelDel(id, t.Context()); err != nil {
					t.Fatal(err)
				}
			} else if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
				t.Fatal(err)
			}
			if change == "aba" {
				detail.CooldownRules[0].CooldownSeconds = 30
				if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			close(respond)
			if change == "delete" {
				awaitManualRequestState(t, updates, group.Name, isManualWaiting)
				cancel()
			}
			awaitManualForwardDone(t, done)
			want := 0
			if change == "name" {
				want = 1
			}
			if len(engine.Snapshot(id)) != want {
				t.Fatal("late result ignored generation or a name edit cleared it")
			}
		})
	}
}

func TestCooldownRelaySuccessAndModelRefreshKeepState(t *testing.T) {
	engine, clock := useCooldownRuntimeClock(t)
	arrived, finishSuccess := make(chan struct{}), make(chan struct{})
	var sends atomic.Int32
	var probeFails atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if probeFails.Load() {
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, cooldownFailureBody)
			} else {
				_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-a"}]}`)
			}
			return
		}
		if sends.Add(1) == 1 {
			close(arrived)
			select {
			case <-finishSuccess:
			case <-r.Context().Done():
				return
			}
			_, _ = io.WriteString(w, chatCompletionBody)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, cooldownFailureBody)
	}))
	t.Cleanup(server.Close)
	grant := newTestChannel(t, testName(t, "keep-rule"), server.URL, "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel})
	group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant))
	_, done, _, _ := startManualWaitForward(t, group.Name)
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("success fixture did not arrive")
	}
	_ = serveCooldownChat(t, grant, 1)
	before := engine.Snapshot(id)
	close(finishSuccess)
	awaitManualForwardDone(t, done)
	if len(before) != 1 || !slices.Equal(before, engine.Snapshot(id)) {
		t.Fatal("in-flight success cleared/shortened the rule")
	}
	detail, _ := op.ChannelDetailGet(id)
	detail.Name += " renamed"
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if err := op.ChannelEnabled(id, enabled, t.Context()); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(before, engine.Snapshot(id)) {
			t.Fatal("name or enabled change cleared/extended the rule")
		}
	}
	for _, fail := range []bool{true, false} {
		probeFails.Store(fail)
		result, err := op.RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
		if err != nil || result.Success == fail {
			t.Fatalf("unexpected model probe result: success=%t error=%v", result.Success, err)
		}
		if !slices.Equal(before, engine.Snapshot(id)) {
			t.Fatal("model probe triggered, extended or cleared a Relay rule")
		}
	}
	clock.advance(30 * time.Second)
	probeFails.Store(true)
	_, _ = op.RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if len(engine.Snapshot(id)) != 0 {
		t.Fatal("model probe established a fresh Relay cooldown")
	}
}

func TestCooldownRelayCancellationAndLocalErrorExcluded(t *testing.T) {
	for _, scenario := range []string{"cancel", "local"} {
		t.Run(scenario, func(t *testing.T) {
			arrived := make(chan struct{})
			disconnected, stopUpstream := make(chan struct{}), make(chan struct{})
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				sends.Add(1)
				close(arrived)
				select {
				case <-r.Context().Done():
					close(disconnected)
				case <-stopUpstream:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(stopUpstream) })
			grant := newTestChannel(t, testName(t, "exclude-rule"), server.URL, "fixture-model-a")
			id := channelIDOfGrant(t, grant)
			setRuntimeCooldownRules(t, id, model.CooldownRule{Keyword: "parameter", CooldownSeconds: 30, Scope: model.CooldownScopeModel}, model.CooldownRule{Keyword: "cancel", CooldownSeconds: 30, Scope: model.CooldownScopeChannel})
			if scenario == "local" {
				setChannelParamOverride(t, grant, "{invalid}")
			}
			group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant))
			_, done, cancel, updates := startManualWaitForward(t, group.Name)
			if scenario == "cancel" {
				select {
				case <-arrived:
				case <-time.After(3 * time.Second):
					t.Fatal("cancel fixture did not arrive")
				}
			} else {
				awaitManualRequestState(t, updates, group.Name, isManualWaiting)
			}
			cancel()
			awaitManualForwardDone(t, done)
			if scenario == "cancel" {
				select {
				case <-disconnected:
				case <-time.After(3 * time.Second):
					t.Fatal("cancellation did not reach the upstream connection")
				}
			}
			if len(ChannelCooldownStateOf(id).RuleCooldowns) != 0 || (scenario == "local" && sends.Load() != 0) {
				t.Fatal("cancel/local failure matched a custom rule")
			}
			awaitCapacity(t, id, 0, 0)
		})
	}
}

func TestCooldownRelayAllCandidatesCooled(t *testing.T) {
	for _, finish := range []string{"expire", "cancel"} {
		t.Run(finish, func(t *testing.T) {
			engine, clock := useCooldownRuntimeClock(t)
			var calls [2]atomic.Int32
			grants := make([]int, 2)
			for index := range grants {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/json")
					if calls[index].Add(1) == 1 {
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, cooldownFailureBody)
						return
					}
					_, _ = io.WriteString(w, chatCompletionBody)
				}))
				t.Cleanup(server.Close)
				grants[index] = newTestChannel(t, testName(t, fmt.Sprint("all-cooled-", index)), server.URL, "fixture-model-a")
				setRuntimeCooldownRules(t, channelIDOfGrant(t, grants[index]), model.CooldownRule{ErrorCode: "429", CooldownSeconds: int64(20 - 10*index), Scope: model.CooldownScopeChannel})
				if group := serveCooldownChat(t, grants[index], 1); compatState(t, group).Status != StatusFailed {
					t.Fatal("fixture did not establish both cooldowns through Relay")
				}
			}
			group := manualWaitGroup(t, newTestGroup(t, model.GroupModeFailover, testRelayConfig(5), grants...))
			response, done, cancel, updates := startManualWaitForward(t, group.Name)
			waiting := awaitManualRequestState(t, updates, group.Name, isManualWaiting)
			if waiting.Attempt != 0 || waiting.ErrorCount != 0 || waiting.NextRetryAt != clock.Now().Add(10*time.Second).UnixMilli() || RouteStateOf(group).ProbeItemID != 0 {
				t.Fatal("all-candidate wait consumed attempts/probe or missed the earliest candidate deadline")
			}
			for _, item := range group.Items {
				awaitCapacity(t, item.ChannelID, 0, 0)
			}
			if finish == "expire" {
				clock.advance(10 * time.Second)
			} else {
				cancel()
			}
			awaitManualForwardDone(t, done)
			if finish == "expire" && (response.Code != http.StatusOK || calls[0].Load() != 1 || calls[1].Load() != 2 || len(engine.Snapshot(group.Items[0].ChannelID)) != 1) {
				t.Fatal("earliest candidate expiry did not use the available fallback exactly once")
			}
			if finish == "cancel" && (calls[0].Load() != 1 || calls[1].Load() != 1 || compatState(t, group).Status != StatusCanceled) {
				t.Fatal("canceled all-candidate wait sent a request")
			}
			for _, item := range group.Items {
				awaitCapacity(t, item.ChannelID, 0, 0)
			}
		})
	}
}

func TestCooldownRelayGlobalProxyFencesLateResults(t *testing.T) {
	engine, _ := useCooldownRuntimeClock(t)
	arrived, respond := make(chan struct{}), make(chan struct{})
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Host != "fixture.invalid" {
			t.Error("proxy lost the captured outbound destination")
		}
		if firstCalls.Add(1) == 1 {
			close(arrived)
			select {
			case <-respond:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, cooldownFailureBody)
	}))
	t.Cleanup(first.Close)
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatCompletionBody)
	}))
	t.Cleanup(second.Close)
	original, _ := op.SettingGetString(model.SettingKeyProxyURL)
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyProxyURL, original) })
	if err := op.SettingSetString(model.SettingKeyProxyURL, first.URL); err != nil {
		t.Fatal(err)
	}
	grant := newTestChannel(t, testName(t, "proxy-fence"), "http://fixture.invalid", "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	detail, _ := op.ChannelDetailGet(id)
	detail.Proxy = true
	detail.CooldownRules = []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeChannel}}
	if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
		t.Fatal(err)
	}
	before, _ := op.ChannelRelaySnapshot(grant)
	config := testRelayConfig(3)
	config.ManualMaxAttempts = 1
	group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
	_, done, _, _ := startManualWaitForward(t, group.Name)
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("captured proxy did not receive the in-flight request")
	}
	_ = serveCooldownChat(t, grant, 1)
	if len(engine.Snapshot(id)) != 1 {
		t.Fatal("parallel failure did not establish proxy-channel state")
	}
	if err := op.SettingSetString(model.SettingKeyProxyURL, second.URL); err != nil {
		t.Fatal(err)
	}
	after, _ := op.ChannelRelaySnapshot(grant)
	if after.Generation <= before.Generation || after.ProxyURL != second.URL || len(engine.Snapshot(id)) != 0 {
		t.Fatal("effective proxy update did not atomically invalidate old state")
	}
	close(respond)
	awaitManualForwardDone(t, done)
	if len(engine.Snapshot(id)) != 0 || firstCalls.Load() != 2 {
		t.Fatal("late response from the previous proxy recreated state")
	}
	if next := serveCooldownChat(t, grant, 1); compatState(t, next).Status != StatusSuccess || secondCalls.Load() != 1 {
		t.Fatal("new generation did not send through the current proxy")
	}
}
