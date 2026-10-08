package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func healthOf(t *testing.T, groupID int) HealthSnapshot {
	t.Helper()
	snapshot, stream := OpenHealthStream()
	CloseHealthStream(stream)
	for _, state := range snapshot {
		if state.GroupID == groupID {
			return state
		}
	}
	t.Fatalf("missing health for group %d", groupID)
	return HealthSnapshot{}
}

func TestHealthEpochsAndExclusiveRecovery(t *testing.T) {
	for _, mode := range []model.GroupMode{model.GroupModeManual, model.GroupModeFailover} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			group := routeTestGroup(t, 7101)
			group.Mode, group.ActiveItemID = mode, group.Items[0].ID
			group.RelayConfig.MemberMaxAttempts = 10
			now := time.Now()
			s := pickGroupItemAt(group, 0, now)
			a, b, late := admitHealthAt(group, s, now), admitHealthAt(group, s, now), admitHealthAt(group, s, now)
			if a == nil || b == nil || late == nil {
				t.Fatal("healthy traffic serialized")
			}
			settleHealthAt(group, s, a, false, "same error", 0, now)
			settleHealthAt(group, s, a, false, "same error", 0, now)
			settleHealthAt(group, s, b, true, "", 0, now)
			if h := healthOf(t, group.ID).Members[s.Item.ID]; h.FailureStreak != 1 {
				t.Fatalf("late success cleared failure: %+v", h)
			}
			if admitHealthAt(group, s, now) != nil {
				t.Fatal("queued selection bypassed new backoff")
			}
			wait := pickGroupItemAt(group, 0, now)
			if !wait.Wait || wait.WaitUntil != now.Add(time.Second) {
				t.Fatalf("bad backoff: %+v", wait)
			}
			now = now.Add(time.Second)
			probeSelection := pickGroupItemAt(group, 0, now)
			probe := admitHealthAt(group, probeSelection, now)
			if probe == nil || !probe.probe {
				t.Fatal("missing recovery ownership")
			}
			if admitHealthAt(group, probeSelection, now) != nil {
				t.Fatal("multiple recovery owners")
			}
			// Unknown outcome returns ownership without healing or counting an error.
			settleHealthAt(group, probeSelection, probe, false, "", 0, now)
			probe = admitHealthAt(group, probeSelection, now)
			if probe == nil {
				t.Fatal("canceled probe leaked ownership")
			}
			settleHealthAt(group, probeSelection, probe, true, "", 0, now)
			settleHealthAt(group, s, late, false, "late error", 0, now)
			h := healthOf(t, group.ID).Members[s.Item.ID]
			if h.FailureStreak != 0 || h.Epoch != 1 || !h.RetryNotBefore.IsZero() {
				t.Fatalf("old epoch polluted recovery: %+v", h)
			}
		})
	}
}

func TestHealthConcurrentFailuresAndReset(t *testing.T) {
	group := routeTestGroup(t, 7201)
	group.Mode, group.ActiveItemID = model.GroupModeManual, group.Items[0].ID
	group.RelayConfig.RetryMode = model.RetryModeExponential
	group.RelayConfig.RetryInitialIntervalSeconds = 1
	group.RelayConfig.RetryMaxIntervalSeconds = 4
	now := time.Now()
	s := pickGroupItemAt(group, 0, now)
	var attempts []*healthAttempt
	for range 8 {
		attempts = append(attempts, admitHealthAt(group, s, now))
	}
	var wg sync.WaitGroup
	for _, a := range attempts {
		wg.Go(func() { settleHealthAt(group, s, a, false, "identical", 0, now) })
	}
	wg.Wait()
	h := healthOf(t, group.ID).Members[s.Item.ID]
	if h.FailureStreak != 8 || h.RetryNotBefore != now.Add(4*time.Second) {
		t.Fatalf("concurrent failure accounting: %+v", h)
	}
	Clear()
	if healthOf(t, group.ID).Members[s.Item.ID].FailureStreak != 8 {
		t.Fatal("history clear removed health")
	}
	NotifyRouteStateChanged(group)
	if healthOf(t, group.ID).Members[s.Item.ID].FailureStreak != 8 {
		t.Fatal("notification removed health")
	}
	ResetRouteStateForGroup(group)
	s = pickGroupItemAt(group, 0, now)
	if h := healthOf(t, group.ID).Members[s.Item.ID]; h.FailureStreak != 0 {
		t.Fatal("reset retained health")
	}
}

func protocolHealthGroup(t *testing.T, serverURL string, protocol model.Protocol, config model.GroupRelayConfig) model.Group {
	t.Helper()
	grant := newTestChannel(t, testName(t, "channel"), serverURL, "upstream-model")
	detail, err := op.ChannelDetailGet(channelIDOfGrant(t, grant))
	if err != nil {
		t.Fatal(err)
	}
	detail.Grants[0].Protocols = protocol
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	name := newTestGroup(t, model.GroupModeManual, config, grant)
	group, err := op.GroupGetByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func responseHealthRequest(t *testing.T, group model.Group) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"input":"ping"}`, group.Name)))
	req.Header.Set("Content-Type", "application/json")
	return req.WithContext(t.Context())
}

const responsePrelude = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-test\",\"status\":\"in_progress\"}}\n\ndata: {\"type\":\"response.in_progress\"}\n\n"
const responseFailure = "data: {\"type\":\"error\",\"message\":\"same upstream failure\"}\n\n"

func TestResponsesCrossRequestBackoffAndRecovery(t *testing.T) {
	var calls atomic.Int32
	arrivals := make(chan time.Time, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrivals <- time.Now()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsePrelude)
		if calls.Add(1) <= 3 {
			io.WriteString(w, responseFailure)
		} else {
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"status\":\"completed\",\"output\":[]}}\n\n")
		}
	}))
	defer upstream.Close()
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 1
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIResponse, config)
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	var previousID uint64
	for i := 1; i <= 4; i++ {
		before := healthOfOptional(group.ID)
		recorder := httptest.NewRecorder()
		serveWithTimeout(t, engine, recorder, responseHealthRequest(t, group))
		arrival := <-arrivals
		if deadline := before.Members[group.ActiveItemID].RetryNotBefore; !deadline.IsZero() && arrival.Before(deadline) {
			t.Fatal("new HTTP request bypassed shared backoff")
		}
		states, stream := OpenRequestStream()
		CloseRequestStream(stream)
		var request RequestState
		for _, state := range states {
			if state.GroupID == group.ID {
				request = state
				break
			}
		}
		if request.ID == previousID || request.Attempt != 1 {
			t.Fatalf("request identity/budget: %+v", request)
		}
		previousID = request.ID
		h := healthOf(t, group.ID).Members[group.ActiveItemID]
		if i <= 3 {
			if request.Status != StatusFailed || request.ErrorCount != 1 || h.FailureStreak != i {
				t.Fatalf("request=%+v health=%+v", request, h)
			}
		} else if request.Status != StatusSuccess || h.FailureStreak != 0 || h.Epoch != 1 {
			t.Fatalf("empty successful terminal did not recover: %+v %+v", request, h)
		}
	}
}

func healthOfOptional(groupID int) HealthSnapshot {
	snapshot, stream := OpenHealthStream()
	CloseHealthStream(stream)
	for _, s := range snapshot {
		if s.GroupID == groupID {
			return s
		}
	}
	return HealthSnapshot{}
}

func TestIdenticalErrorsInDifferentRoundsCountSeparately(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) > 1 {
			io.WriteString(w, responsePrelude)
		}
		io.WriteString(w, responseFailure)
	}))
	defer upstream.Close()
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIResponse, testRelayConfig(3))
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	serveWithTimeout(t, engine, httptest.NewRecorder(), responseHealthRequest(t, group))
	states, stream := OpenRequestStream()
	CloseRequestStream(stream)
	for _, state := range states {
		if state.GroupID == group.ID {
			if state.ErrorCount != 2 || state.Attempt != 2 || healthOf(t, group.ID).Members[group.ActiveItemID].FailureStreak != 2 {
				t.Fatalf("two rounds counted incorrectly: %+v", state)
			}
			return
		}
	}
	t.Fatal("missing request")
}

func TestHealthSingleProbeHoldsThroughFirstFrame(t *testing.T) {
	arrived := make(chan int32, 16)
	finish := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsePrelude)
		w.(http.Flusher).Flush()
		arrived <- n
		if n > 1 {
			select {
			case <-finish:
			case <-r.Context().Done():
				return
			}
		}
		io.WriteString(w, responseFailure)
	}))
	defer upstream.Close()
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIResponse, testRelayConfig(2))
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	serveWithTimeout(t, engine, httptest.NewRecorder(), responseHealthRequest(t, group))
	<-arrived
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { engine.ServeHTTP(httptest.NewRecorder(), responseHealthRequest(t, group).WithContext(ctx)) })
	}
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("probe never sent")
	}
	// All competitors have enough time to consume the first-frame notifications;
	// none can send until the held probe settles. The channel is the send barrier.
	select {
	case n := <-arrived:
		t.Fatalf("extra probe after first frame: %d", n)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	wg.Wait()
	close(finish)
	h := healthOf(t, group.ID).Members[group.ActiveItemID]
	if h.FailureStreak != 1 {
		t.Fatalf("cancellation changed failures: %+v", h)
	}
	if ChannelConcurrencyStats(group.Items[0].ChannelID).Active != 0 {
		t.Fatal("cancellation leaked capacity")
	}
}

func TestHealthIdentityAndSlowSubscriber(t *testing.T) {
	grantID := newTestChannel(t, testName(t, "identity"), "http://127.0.0.1", "upstream-model")
	name := newTestGroup(t, model.GroupModeManual, testRelayConfig(2), grantID)
	group, _ := op.GroupGetByName(name)
	now := time.Now()
	s := pickGroupItemAt(group, 0, now)
	a := admitHealthAt(group, s, now)
	late := admitHealthAt(group, s, now)
	settleHealthAt(group, s, a, false, "failure", 0, now)
	version := healthOf(t, group.ID).Members[s.Item.ID].Version
	setConcurrency(t, s.Item.ChannelID, 3)
	NotifyRouteStateChanged(group)
	if h := healthOf(t, group.ID).Members[s.Item.ID]; h.Version != version || h.FailureStreak != 1 {
		t.Fatalf("capacity reset health: %+v", h)
	}
	_, updates := OpenHealthStream()
	defer CloseHealthStream(updates)
	for range routeStreamBuffer + 1 {
		routeMu.Lock()
		publishHealthLocked(routes[group.ID])
		routeMu.Unlock()
	}
	for range updates {
	}
	if healthOf(t, group.ID).Members[s.Item.ID].FailureStreak != 1 {
		t.Fatal("slow subscription lost authoritative health")
	}
	detail, _ := op.ChannelDetailGet(s.Item.ChannelID)
	detail.Keys[0].Key = "replacement-test-credential"
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	NotifyRouteStateChanged(group)
	settleHealthAt(group, s, late, false, "old credential failure", 0, now)
	h := healthOf(t, group.ID).Members[s.Item.ID]
	if h.Version == version || h.FailureStreak != 0 {
		t.Fatalf("old credential result polluted replacement: %+v", h)
	}
	s = pickGroupItemAt(group, 0, now)
	a = admitHealthAt(group, s, now)
	ResetRouteState(group.ID)
	settleHealthAt(group, s, a, false, "deleted group failure", 0, now)
	if healthOfOptional(group.ID).GroupID != 0 {
		t.Fatal("late result recreated deleted group")
	}
}

func TestRequestFailureDeduplicatesByRound(t *testing.T) {
	r := newRequestState(t.Context(), "round-test", "", 0, model.ProtocolOpenAIChatCompletion, "{}", 0)
	r.startRound(func() {}, "provider", "model", model.ProtocolOpenAIChatCompletion, true)
	r.finishRound("identical", true)
	r.startRound(func() {}, "provider", "model", model.ProtocolOpenAIChatCompletion, true)
	r.finishRound("", false)
	r.markFailed(errors.New("identical"), "", nil)
	if r.ErrorCount != 2 {
		t.Fatalf("different rounds with same error counted %d", r.ErrorCount)
	}
}

func TestStreamEOFAndConvertedErrorsAffectSharedHealth(t *testing.T) {
	chatPrefix := strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n")
	for _, tc := range []struct {
		name             string
		protocol         model.Protocol
		format           llm.APIFormat
		prefix, terminal string
	}{
		{"chat", model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, chatPrefix, "data: [DONE]\n\n"},
		{"responses", model.ProtocolOpenAIResponse, llm.APIFormatOpenAIResponse, responsePrelude, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n"},
		{"anthropic", model.ProtocolAnthropicMessage, llm.APIFormatAnthropicMessage, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"upstream\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
		{"chat-to-responses", model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIResponse, chatPrefix, "data: [DONE]\n\n"},
	} {
		for _, ending := range []string{"eof", "success", "error"} {
			t.Run(tc.name+"/"+ending, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, tc.prefix)
					w.(http.Flusher).Flush()
					switch ending {
					case "success":
						io.WriteString(w, tc.terminal)
					case "error":
						if tc.protocol == model.ProtocolOpenAIResponse {
							io.WriteString(w, responseFailure)
						} else if tc.protocol == model.ProtocolAnthropicMessage {
							io.WriteString(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"failed\"}}\n\n")
						} else {
							io.WriteString(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"failed\"}}\n\n")
						}
					}
				}))
				defer upstream.Close()
				config := testRelayConfig(2)
				config.ManualMaxAttempts = 1
				group := protocolHealthGroup(t, upstream.URL, tc.protocol, config)
				engine := gin.New()
				engine.POST("/v1/chat/completions", Forward(tc.format))
				engine.POST("/v1/responses", Forward(tc.format))
				req := chatRequest(t, group.Name, true).WithContext(t.Context())
				if tc.format == llm.APIFormatOpenAIResponse {
					req = responseHealthRequest(t, group)
				}
				if tc.format == llm.APIFormatAnthropicMessage {
					req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"max_tokens":16,"messages":[{"role":"user","content":"ping"}]}`, group.Name))).WithContext(t.Context())
					req.Header.Set("Content-Type", "application/json")
				}
				serveWithTimeout(t, engine, httptest.NewRecorder(), req)
				h := healthOf(t, group.ID).Members[group.ActiveItemID]
				want := 1
				if ending == "success" {
					want = 0
				}
				if h.FailureStreak != want {
					t.Fatalf("health=%+v, want %d failures", h, want)
				}
			})
		}
	}
}

func TestHTTPResultsCannotCrossRecoveryEpoch(t *testing.T) {
	arrived := make(chan int32, 4)
	release := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		arrived <- n
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, responsePrelude)
		w.(http.Flusher).Flush()
		if n <= 3 {
			select {
			case <-release[n-1]:
			case <-r.Context().Done():
				return
			}
		}
		if n == 1 || n == 3 {
			io.WriteString(w, responseFailure)
		} else {
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
		}
	}))
	defer upstream.Close()
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIResponse, testRelayConfig(2))
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan struct{}, 4)
	for range 3 {
		go func() {
			engine.ServeHTTP(httptest.NewRecorder(), responseHealthRequest(t, group).WithContext(ctx))
			done <- struct{}{}
		}()
	}
	await := func(ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("HTTP barrier timed out")
		}
	}
	for range 3 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("healthy requests did not run concurrently")
		}
	}
	close(release[0])
	await(done)
	close(release[1])
	await(done)
	if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != 1 {
		t.Fatalf("pre-fault success cleared protection: %+v", h)
	}
	engine.ServeHTTP(httptest.NewRecorder(), responseHealthRequest(t, group).WithContext(ctx))
	if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != 0 || h.Epoch != 1 {
		t.Fatalf("qualified probe did not recover: %+v", h)
	}
	close(release[2])
	await(done)
	if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != 0 {
		t.Fatalf("old HTTP failure reopened recovered epoch: %+v", h)
	}
}

func TestCapacityQueuedRequestRechecksSharedDeadline(t *testing.T) {
	arrivals := make(chan time.Time, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		arrivals <- time.Now()
		w.Header().Set("Content-Type", "text/event-stream")
		if n == 1 {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			io.WriteString(w, responsePrelude+responseFailure)
		} else {
			io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
		}
	}))
	defer upstream.Close()
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIResponse, testRelayConfig(2))
	id := group.Items[0].ChannelID
	setConcurrency(t, id, 1)
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	go func() {
		engine.ServeHTTP(httptest.NewRecorder(), responseHealthRequest(t, group).WithContext(ctx))
		done <- struct{}{}
	}()
	select {
	case <-arrivals:
	case <-ctx.Done():
		t.Fatal("first request missing")
	}
	go func() {
		engine.ServeHTTP(httptest.NewRecorder(), responseHealthRequest(t, group).WithContext(ctx))
		done <- struct{}{}
	}()
	awaitCapacity(t, id, 1, 1)
	close(release)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("first request did not fail")
	}
	deadline := healthOf(t, group.ID).Members[group.ActiveItemID].RetryNotBefore
	select {
	case arrival := <-arrivals:
		if arrival.Before(deadline) {
			t.Fatal("capacity waiter bypassed failure backoff")
		}
	case <-ctx.Done():
		t.Fatal("queued request not resumed")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("queued request not completed")
	}
}

type brokenHealthWriter struct{ *httptest.ResponseRecorder }

func (w brokenHealthWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestClientWriteFailureDoesNotCountAsUpstreamFailure(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch calls.Add(1) {
		case 1:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n")+"data: {\"error\":{\"message\":\"failed\"}}\n\n")
		case 2:
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, chatCompletionStreamBody)
		default:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, chatCompletionBody)
		}
	}))
	defer upstream.Close()
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, testRelayConfig(2))
	engine := newRelayEngine()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	engine.ServeHTTP(httptest.NewRecorder(), chatRequest(t, group.Name, true).WithContext(ctx))
	engine.ServeHTTP(brokenHealthWriter{httptest.NewRecorder()}, chatRequest(t, group.Name, true).WithContext(ctx))
	if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != 1 {
		t.Fatalf("client write counted as upstream error: %+v", h)
	}
	engine.ServeHTTP(brokenHealthWriter{httptest.NewRecorder()}, chatRequest(t, group.Name, false).WithContext(ctx))
	if h := healthOf(t, group.ID).Members[group.ActiveItemID]; h.FailureStreak != 0 {
		t.Fatalf("complete upstream success was lost to client write failure: %+v", h)
	}
}

func TestHealthWaitUsesLaterCooldownAndBackoff(t *testing.T) {
	group := routeTestGroup(t, 7301)
	now := time.Now()
	s := pickGroupItemAt(group, 0, now)
	a := admitHealthAt(group, s, now)
	settleHealthAt(group, s, a, false, "failed", 0, now)
	wait := pickGroupItemAt(group, 0, now)
	if !wait.Wait || wait.WaitUntil.UnixMilli() != now.Add(60*time.Second).UnixMilli() {
		t.Fatalf("wait does not reflect full cooldown: %+v", wait)
	}
}

func TestManualRetryAfterUsesSharedExponentialStreak(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "unavailable")
	}))
	defer upstream.Close()
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 1
	config.RetryMode = model.RetryModeExponential
	config.RetryInitialIntervalSeconds = 1
	config.RetryMaxIntervalSeconds = 4
	group := protocolHealthGroup(t, upstream.URL, model.ProtocolOpenAIChatCompletion, config)
	engine := newRelayEngine()
	for _, want := range []string{"1", "2"} {
		recorder := httptest.NewRecorder()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		engine.ServeHTTP(recorder, chatRequest(t, group.Name, false).WithContext(ctx))
		cancel()
		assertUpstreamFailure(t, recorder)
		assertRetryAfter(t, recorder, want)
	}
}

func TestManualHealthDoesNotPublishEmptyRoute(t *testing.T) {
	group := routeTestGroup(t, 7401)
	group.Mode = model.GroupModeManual
	group.ActiveItemID = group.Items[0].ID
	s := pickGroupItem(group, 0)
	updates := OpenRouteStream()
	defer CloseRouteStream(updates)
	a := admitHealthAt(group, s, time.Now())
	settleHealth(group, s, a, false, "failed", 0)
	select {
	case state := <-updates:
		t.Fatalf("manual health overwrote UI route: %+v", state)
	default:
	}
}

func TestLateMemberFailureDoesNotResetBackupBudget(t *testing.T) {
	group := routeTestGroup(t, 7501, 7502)
	now := time.Now()
	s := pickGroupItemAt(group, 0, now)
	a, b := admitHealthAt(group, s, now), admitHealthAt(group, s, now)
	settleHealthAt(group, s, a, false, "first", 0, now)
	backup := pickGroupItemAt(group, 0, now)
	if backup.Item.ID != 7502 {
		t.Fatal("backup not selected")
	}
	settleHealthAt(group, s, b, false, "late", 0, now)
	current := pickGroupItemAt(group, 0, now)
	if current.Generation != backup.Generation || current.Item.ID != backup.Item.ID {
		t.Fatal("old member failure reset active backup budget")
	}
}
