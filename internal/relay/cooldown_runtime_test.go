package relay

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/looplj/axonhub/llm/httpclient"
)

func useCooldownRuntimeClock(t *testing.T) (*CooldownEngine, *cooldownTestClock) {
	t.Helper()
	clock := &cooldownTestClock{now: time.Now()}
	previous := ruleCooldownEngine.Load()
	engine := NewCooldownEngine(clock, notifyChannelRuleCooldown)
	ruleCooldownEngine.Store(engine)
	if err := op.RegisterChannelCooldownPublisher(engine); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ruleCooldownEngine.Store(previous)
		if err := op.RegisterChannelCooldownPublisher(previous); err != nil {
			t.Error(err)
		}
		engine.Close()
	})
	return engine, clock
}

func setRuntimeCooldownRules(t *testing.T, id int, rules ...model.CooldownRule) {
	t.Helper()
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		t.Fatal(err)
	}
	detail.CooldownRules = model.CloneCooldownRules(rules)
	if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
		t.Fatal(err)
	}
}

func runtimeCooldownToken(t *testing.T, engine *CooldownEngine, grantID int) CooldownToken {
	t.Helper()
	config, err := op.ChannelRelaySnapshot(grantID)
	if err != nil {
		t.Fatal(err)
	}
	return captureCooldownTest(t, engine, config.Channel.ID, config.Generation, config.Grant.ChannelModel.Name)
}

func recordRuntimeCooldown(t *testing.T, engine *CooldownEngine, grantID int) CooldownToken {
	t.Helper()
	token := runtimeCooldownToken(t, engine, grantID)
	if !engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
		t.Fatal("fixture did not establish a runtime rule")
	}
	return token
}

func TestCooldownRuntimeNotificationsPreserveRoutes(t *testing.T) {
	engine, clock := useCooldownRuntimeClock(t)
	grantID := newTestChannel(t, testName(t, "rule-notices"), "http://127.0.0.1", "fixture-model-a")
	id := channelIDOfGrant(t, grantID)
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel})
	name := newTestGroup(t, model.GroupModeFailover, testRelayConfig(3), grantID)
	group := manualWaitGroup(t, name)
	unvisited := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grantID))
	selection := pickGroupItemAt(group, 0, clock.Now())
	attempt := admitHealthAt(group, selection, clock.Now())
	settleHealthAt(group, selection, attempt, false, "fixture previous failure", 0, clock.Now())
	clock.advance(2 * time.Second)
	selection = pickGroupItemAt(group, 0, clock.Now())
	probe := admitHealthAt(group, selection, clock.Now())
	if probe == nil || !probe.probe {
		t.Fatal("fixture did not reserve member recovery")
	}
	before, beforeUnvisited := RouteStateOf(group), RouteStateOf(unvisited)
	healthBefore := probe.member.MemberHealth
	routeStream := OpenRouteStream()
	defer CloseRouteStream(routeStream)
	channelStream := OpenChannelCooldownStream()
	defer CloseChannelCooldownStream(channelStream)
	token := recordRuntimeCooldown(t, engine, grantID)
	after, afterUnvisited := RouteStateOf(group), RouteStateOf(unvisited)
	if len(after.RuleCooldowns) != 1 || len(afterUnvisited.RuleCooldowns) != 1 || after.Version == before.Version || afterUnvisited.Version == beforeUnvisited.Version {
		t.Fatal("shared rule was not projected to active and unvisited groups")
	}
	if after.ConfigVersion != before.ConfigVersion || after.SelectionVersion != before.SelectionVersion ||
		afterUnvisited.ConfigVersion != beforeUnvisited.ConfigVersion || afterUnvisited.SelectionVersion != beforeUnvisited.SelectionVersion ||
		!reflect.DeepEqual(before.CooldownVersions, after.CooldownVersions) || probe.member.probe != probe || probe.member.MemberHealth != healthBefore {
		t.Fatal("rule notice changed original route/health/probe identity")
	}
	routeMu.Lock()
	created := routes[unvisited.ID] != nil
	routeMu.Unlock()
	if created {
		t.Fatal("rule projection created an unvisited route")
	}
	seen := map[int]bool{}
	for len(seen) < 2 {
		select {
		case state := <-routeStream:
			seen[state.GroupID] = len(state.RuleCooldowns) == 1
		case <-time.After(time.Second):
			t.Fatal("missing cross-group runtime notice")
		}
	}
	select {
	case state := <-channelStream:
		if !slices.Equal(state.RuleCooldowns, after.RuleCooldowns) || state.Version != ChannelCooldownStateOf(id).Version {
			t.Fatal("channel stream/current snapshot mismatch")
		}
	case <-time.After(time.Second):
		t.Fatal("missing channel notice")
	}
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 31, Scope: model.CooldownScopeModel})
	cleared := RouteStateOf(group)
	if len(cleared.RuleCooldowns) != 0 || cleared.SelectionVersion != after.SelectionVersion || cleared.ConfigVersion != after.ConfigVersion || probe.member.probe != probe {
		t.Fatal("rule edit did not independently clear custom state")
	}
	if engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
		t.Fatal("late old configuration rebuilt state")
	}
	settleHealthAt(group, selection, probe, true, "", 0, clock.Now())
	if probe.member.FailureStreak != 0 {
		t.Fatal("rule edit invalidated the original member success settlement")
	}
}

func TestCooldownRuntimeMaximumWaitAndMemberRelease(t *testing.T) {
	engine, clock := useCooldownRuntimeClock(t)
	grant := newTestChannel(t, testName(t, "rule-max"), "http://127.0.0.1", "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	setRuntimeCooldownRules(t, id,
		model.CooldownRule{ErrorCode: "429", CooldownSeconds: 10, Scope: model.CooldownScopeModel},
		model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeChannel})
	config := testRelayConfig(3)
	config.MemberRetryIntervalSeconds = 40
	group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, config, grant))
	selected := pickGroupItemAt(group, 0, clock.Now())
	attempt := admitHealthAt(group, selected, clock.Now())
	settleHealthAt(group, selected, attempt, false, "fixture failure", 0, clock.Now())
	recordRuntimeCooldown(t, engine, grant)
	if next := pickGroupItemAt(group, 0, clock.Now()); !next.Wait || !next.WaitUntil.Equal(clock.Now().Add(40*time.Second)) {
		t.Fatalf("multiple waits did not take maximum: until=%v", next.WaitUntil)
	}
	clock.advance(30 * time.Second)
	if len(engine.Snapshot(id)) != 0 || !pickGroupItemAt(group, 0, clock.Now()).Wait {
		t.Fatal("custom expiry ended the longer member backoff")
	}
	clock.advance(10 * time.Second)
	if next := pickGroupItemAt(group, 0, clock.Now()); next.Wait || next.Item.ID == 0 {
		t.Fatal("maximum deadline did not restore admission")
	}
	failover := manualWaitGroup(t, newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), grant))
	selected = pickGroupItemAt(failover, 0, clock.Now())
	if !recordRouteFailureAt(failover, selected.Item.ID, 1, selected.Generation, 0, clock.Now()) {
		t.Fatal("member cooldown not established")
	}
	recordRuntimeCooldown(t, engine, grant)
	before := RouteStateOf(failover)
	if _, err := releaseRouteCooldownAt(failover, selected.Item.ID, before.CooldownVersions[selected.Item.ID], clock.Now()); err != nil {
		t.Fatal(err)
	}
	if after := RouteStateOf(failover); len(after.Cooldowns) != 0 || len(after.RuleCooldowns) != 2 || !pickGroupItemAt(failover, 0, clock.Now()).Wait {
		t.Fatal("member release cleared/bypassed shared custom cooldown")
	}
}

func TestCooldownRuntimeFinalSendGuard(t *testing.T) {
	for _, change := range []string{"rule", "credential", "grant", "disabled", "selection", "ordinary-notice"} {
		t.Run(change, func(t *testing.T) {
			engine, _ := useCooldownRuntimeClock(t)
			var sends atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				sends.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(chatCompletionBody))
			}))
			defer server.Close()
			grant := newTestChannel(t, testName(t, "final-guard"), server.URL, "fixture-model-a")
			id := channelIDOfGrant(t, grant)
			setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel})
			config, _ := op.ChannelRelaySnapshot(grant)
			token := runtimeCooldownToken(t, engine, grant)
			group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant))
			selection := pickGroupItem(group, 0)
			ctx := bindCooldownSend(t.Context(), config, token, group, selection)
			release, err := acquireChannelCapacity(ctx, id, selection.Changed)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			switch change {
			case "rule":
				recordRuntimeCooldown(t, engine, grant)
			case "credential", "grant":
				detail, _ := op.ChannelDetailGet(id)
				if change == "credential" {
					detail.Keys[0].Key = "fixture-new-value"
				} else {
					detail.Grants = nil
				}
				if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if err := op.ChannelEnabled(id, false, t.Context()); err != nil {
					t.Fatal(err)
				}
			case "selection":
				ResetRouteStateForGroup(group)
			case "ordinary-notice":
				notifyChannelRuleCooldown(id)
			}
			outbound, _, _, err := buildOutbound(config.Channel, config.Grant, *config.Grant.ChannelKey, model.ProtocolOpenAIChatCompletion)
			if err != nil {
				t.Fatal(err)
			}
			raw := &httpclient.Request{Method: http.MethodPost, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"model":"fixture-model-a","messages":[{"role":"user","content":"fixture"}]}`)}
			_, sent, err := sendPassthrough(ctx, startupProtocols()[0].format, raw, config.Channel, outbound, false, "fixture-model-a")
			if change == "ordinary-notice" {
				if !sent || sends.Load() != 1 || err != nil {
					t.Fatal("ordinary notification rejected a still-current send")
				}
				return
			}
			if sent || sends.Load() != 0 || !errors.Is(err, errCooldownSendChanged) {
				t.Fatalf("last guard failed: sent=%t count=%d error=%v", sent, sends.Load(), err)
			}
			release()
			awaitCapacity(t, id, 0, 0)
		})
	}
}

func TestCooldownRuntimeRestartAndBackup(t *testing.T) {
	engine, _ := useCooldownRuntimeClock(t)
	grant := newTestChannel(t, testName(t, "restart"), "http://127.0.0.1", "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	rule := model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel}
	setRuntimeCooldownRules(t, id, rule)
	token := recordRuntimeCooldown(t, engine, grant)
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	if len(engine.Snapshot(id)) != 1 {
		t.Fatal("unchanged cache reload cleared live state")
	}
	dump, err := op.DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(dump)
	if err != nil || strings.Contains(string(raw), "rule_cooldowns") || strings.Contains(string(raw), "rule_cooldown_version") || strings.Contains(string(raw), `"generation"`) {
		t.Fatal("runtime state entered backup")
	}
	fresh := NewCooldownEngine(nil, nil)
	defer fresh.Close()
	if err := op.RegisterChannelCooldownPublisher(fresh); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = op.RegisterChannelCooldownPublisher(engine) }()
	config, err := op.ChannelRelaySnapshot(grant)
	if err != nil || len(config.Channel.CooldownRules) != 1 || config.Channel.CooldownRules[0] != rule || len(fresh.Snapshot(id)) != 0 {
		t.Fatal("new process engine did not preserve configuration and clear runtime")
	}
	if fresh.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
		t.Fatal("new engine accepted an old process token")
	}
}

func TestCooldownRuntimeConcurrentConfigurationExpiryAndStreams(t *testing.T) {
	engine, clock := useCooldownRuntimeClock(t)
	fixture := newCooldownRelayFixture(t, "http://127.0.0.1", model.CooldownScopeModel)
	grant, id := fixture.grants["fixture-model-a/a"], fixture.id
	rule := model.CooldownRule{ErrorCode: "429", CooldownSeconds: 1, Scope: model.CooldownScopeModel}
	setRuntimeCooldownRules(t, id, rule)
	group := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant, fixture.grants["fixture-model-a/b"], fixture.grants["fixture-model-b/a"]))
	_ = pickGroupItem(group, 0)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 25 {
				switch worker {
				case 0:
					config, err := op.ChannelRelaySnapshot(grant)
					if err != nil {
						t.Error(err)
						return
					}
					token, ok := engine.Capture(id, config.Generation, "fixture-model-a")
					if ok {
						failure, _ := extractUpstreamFailure(UpstreamFailure{ChannelID: id, UpstreamModel: "fixture-model-a", Generation: token.generation, Protocol: model.ProtocolOpenAIChatCompletion, HTTPStatus: 429}, []byte(cooldownFailureBody), "", false, "application/json")
						engine.Record(token, failure)
					}
				case 1:
					clock.advance(time.Second)
				case 2:
					stream := OpenChannelCooldownStream()
					_ = ChannelCooldownStateOf(id)
					state := RouteStateOf(group)
					if len(state.RuleCooldowns) > 1 || (len(state.RuleCooldowns) == 1 && state.RuleCooldowns[0].ModelName != "fixture-model-a") {
						t.Error("concurrent projection mixed deadlines or models across Key references")
					}
					CloseChannelCooldownStream(stream)
				case 3:
					detail, err := op.ChannelDetailGet(id)
					if err == nil {
						detail.CooldownRules[0].CooldownSeconds = 3 - detail.CooldownRules[0].CooldownSeconds
						_, err = op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, context.Background())
					}
					if err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	setRuntimeCooldownRules(t, id)
	if len(engine.Snapshot(id)) != 0 || clock.activeTimers() != 0 || len(RouteStateOf(group).RuleCooldowns) != 0 {
		t.Fatal("clear left active runtime state or timers")
	}
}

func TestCooldownRuntimeProbeOwnershipDuringSharedCooldown(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsent-reservation", true: "in-flight-probe"}[inFlight], func(t *testing.T) {
			engine, clock := useCooldownRuntimeClock(t)
			first := newTestChannel(t, testName(t, "probe-rule"), "http://127.0.0.1", "fixture-model-a")
			backup := newTestChannel(t, testName(t, "probe-backup"), "http://127.0.0.1", "fixture-model-a")
			setRuntimeCooldownRules(t, channelIDOfGrant(t, first), model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeChannel})
			config := testRelayConfig(1)
			config.MemberCooldownSeconds = 1
			group := manualWaitGroup(t, newTestGroup(t, model.GroupModeFailover, config, first, backup))
			selection := pickGroupItemAt(group, 0, clock.Now())
			if !recordRouteFailureAt(group, selection.Item.ID, 1, selection.Generation, 0, clock.Now()) {
				t.Fatal("fixture did not enter member cooldown")
			}
			clock.advance(time.Second)
			selection = pickGroupItemAt(group, 0, clock.Now())
			if selection.ProbeToken == 0 || selection.Item.ID != group.Items[0].ID {
				t.Fatal("fixture did not reserve recovery")
			}
			var attempt *healthAttempt
			if inFlight {
				attempt = admitHealthAt(group, selection, clock.Now())
				if attempt == nil || !attempt.probe {
					t.Fatal("fixture did not admit its recovery attempt")
				}
			}
			recordRuntimeCooldown(t, engine, first)
			token := selection.ProbeToken
			if inFlight {
				token = 0 // another request must preserve the original owner
			}
			next := pickGroupItemAt(group, token, clock.Now())
			if next.Wait || next.Item.ID != group.Items[1].ID {
				t.Fatal("blocked recovery prevented healthy fallback")
			}
			state := RouteStateOf(group)
			if inFlight {
				if state.ProbeItemID != selection.Item.ID {
					t.Fatal("another request stole the in-flight probe")
				}
				settleHealthAt(group, selection, attempt, true, "", 0, clock.Now())
				recordRouteSuccess(group, selection.Item.ID, selection.Generation, selection.ProbeToken)
			} else if state.ProbeItemID != 0 {
				t.Fatal("unsent blocked reservation was retained")
			}
			if state = RouteStateOf(group); state.ProbeItemID != 0 || state.CurrentItemID != group.Items[1].ID || len(state.RuleCooldowns) != 1 {
				t.Fatal("old probe reclaimed selection or left a reservation")
			}
		})
	}
}

func TestCooldownRuntimeInvalidationNotifiesAllGroups(t *testing.T) {
	for _, change := range []string{"credential", "connection", "clear", "delete", "aba"} {
		t.Run(change, func(t *testing.T) {
			engine, _ := useCooldownRuntimeClock(t)
			grant := newTestChannel(t, testName(t, "invalidation"), "http://127.0.0.1", "fixture-model-a")
			id := channelIDOfGrant(t, grant)
			rule := model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel}
			setRuntimeCooldownRules(t, id, rule)
			active := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant))
			unvisited := manualWaitGroup(t, newTestGroup(t, model.GroupModeManual, testRelayConfig(3), grant))
			_ = pickGroupItem(active, 0)
			token := recordRuntimeCooldown(t, engine, grant)
			waiting := pickGroupItem(active, 0)
			if !waiting.Wait {
				t.Fatal("fixture did not block its waiting group")
			}
			routeStream := OpenRouteStream()
			defer CloseRouteStream(routeStream)
			channelStream := OpenChannelCooldownStream()
			defer CloseChannelCooldownStream(channelStream)
			detail, _ := op.ChannelDetailGet(id)
			if change == "delete" {
				if err := op.ChannelDel(id, t.Context()); err != nil {
					t.Fatal(err)
				}
			} else {
				switch change {
				case "credential":
					detail.Keys[0].Key = "fixture-replacement"
				case "connection":
					detail.OpenAIChatCompletionPath = "/fixture-changed"
				case "clear":
					detail.CooldownRules = nil
				case "aba":
					detail.CooldownRules[0].CooldownSeconds++
				}
				if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); err != nil {
					t.Fatal(err)
				}
				if change == "aba" {
					setRuntimeCooldownRules(t, id, rule)
				}
			}
			select {
			case <-waiting.Changed:
			default:
				t.Fatal("configuration invalidation did not wake the waiter")
			}
			seen := make(map[int]bool)
			for len(seen) < 2 {
				select {
				case state, ok := <-routeStream:
					if !ok {
						t.Fatal("invalidation stream closed early")
					}
					if state.GroupID == active.ID || state.GroupID == unvisited.ID {
						if len(state.RuleCooldowns) != 0 {
							t.Fatal("invalidation retained a group rule")
						}
						seen[state.GroupID] = true
					}
				case <-time.After(time.Second):
					t.Fatal("invalidation omitted an active or unvisited group")
				}
			}
			select {
			case state := <-channelStream:
				if state.ChannelID != id || len(state.RuleCooldowns) != 0 {
					t.Fatal("channel invalidation was not an empty versioned snapshot")
				}
			case <-time.After(time.Second):
				t.Fatal("channel invalidation notice missing")
			}
			if engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
				t.Fatal("invalidated/ABA token rebuilt state")
			}
		})
	}
}

func TestCooldownRuntimeDeletedIDReloadRejectsOldToken(t *testing.T) {
	engine, _ := useCooldownRuntimeClock(t)
	grant := newTestChannel(t, testName(t, "reused-id"), "http://127.0.0.1", "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeChannel})
	detail, _ := op.ChannelDetailGet(id)
	token := recordRuntimeCooldown(t, engine, grant)
	if err := op.ChannelDel(id, t.Context()); err != nil {
		t.Fatal(err)
	}
	// Synthetic restore reuses an old channel ID, as an imported backup can.
	if err := db.GetDB().Create(&model.Channel{ID: id, ChannelConfig: detail.ChannelConfig}).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	if current := engine.Observe(id, "fixture-model-a"); !current.Configured || current.Generation <= token.generation || len(current.Hits) != 0 {
		t.Fatal("reloaded ID reused its former runtime generation")
	}
	if engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
		t.Fatal("reused channel ID accepted the deleted token")
	}
}

func TestCooldownRuntimeSlowSubscriberAndReconnect(t *testing.T) {
	engine, clock := useCooldownRuntimeClock(t)
	grant := newTestChannel(t, testName(t, "slow-rule-stream"), "http://127.0.0.1", "fixture-model-a")
	id := channelIDOfGrant(t, grant)
	setRuntimeCooldownRules(t, id, model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel})
	token := runtimeCooldownToken(t, engine, grant)
	slow, fast := OpenChannelCooldownStream(), OpenChannelCooldownStream()
	defer CloseChannelCooldownStream(slow)
	defer CloseChannelCooldownStream(fast)
	var previous ChannelCooldownState
	for range routeStreamBuffer + 1 {
		clock.advance(time.Millisecond)
		if !engine.Record(token, failureForCooldownTest(t, token, model.ProtocolOpenAIChatCompletion, 429, cooldownFailureBody)) {
			t.Fatal("extension was not recorded")
		}
		select {
		case state := <-fast:
			if state.ChannelID != id || len(state.RuleCooldowns) != 1 || state.Version == previous.Version || (previous.ChannelID != 0 && state.RuleCooldowns[0].Until <= previous.RuleCooldowns[0].Until) {
				t.Fatal("fast subscriber missed the newest extension")
			}
			previous = state
		case <-time.After(time.Second):
			t.Fatal("slow subscriber blocked another subscriber")
		}
	}
	routeMu.Lock()
	_, registered := channelRuleStreams[slow]
	routeMu.Unlock()
	if registered {
		t.Fatal("congested subscriber remained registered")
	}
	for range routeStreamBuffer {
		<-slow
	}
	if _, ok := <-slow; ok {
		t.Fatal("congested stream did not close after buffered messages")
	}
	reconnected := OpenChannelCooldownStream()
	defer CloseChannelCooldownStream(reconnected)
	if current := ChannelCooldownStateOf(id); !reflect.DeepEqual(current, previous) {
		t.Fatal("reconnect snapshot failed to recover the newest state")
	}
	setRuntimeCooldownRules(t, id)
	select {
	case current := <-reconnected:
		if current.Version == previous.Version || len(current.RuleCooldowns) != 0 {
			t.Fatal("reconnected stream lost the versioned empty state")
		}
	case <-time.After(time.Second):
		t.Fatal("reconnected stream did not receive invalidation")
	}
}
