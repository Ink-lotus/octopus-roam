package relay

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func setConcurrency(t *testing.T, id, limit int) {
	t.Helper()
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		t.Fatal(err)
	}
	detail.MaxConcurrency = limit
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
}

func awaitCapacity(t *testing.T, id, active, waiting int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := ChannelConcurrencyStats(id)
		if status.Active == active && status.Waiting == waiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("capacity = %+v, want active=%d waiting=%d", ChannelConcurrencyStats(id), active, waiting)
}

func TestChannelCapacityCancelResizeDisableAndDelete(t *testing.T) {
	for _, change := range []string{"resize", "unlimited", "disable", "delete", "route", "cancel"} {
		t.Run(change, func(t *testing.T) {
			grant := newTestChannel(t, testName(t, "capacity"), "http://127.0.0.1", "upstream")
			id := channelIDOfGrant(t, grant)
			setConcurrency(t, id, 1)
			release, err := acquireChannelCapacity(t.Context(), id, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			route := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				release, err := acquireChannelCapacity(ctx, id, route)
				if release != nil {
					release()
				}
				done <- err
			}()
			awaitCapacity(t, id, 1, 1)
			switch change {
			case "resize":
				setConcurrency(t, id, 2)
			case "unlimited":
				setConcurrency(t, id, 0)
			case "disable":
				if err := op.ChannelEnabled(id, false, t.Context()); err != nil {
					t.Fatal(err)
				}
			case "delete":
				if err := op.ChannelDel(id, t.Context()); err != nil {
					t.Fatal(err)
				}
			case "route":
				close(route)
			case "cancel":
				cancel()
			}
			select {
			case err := <-done:
				if (change == "resize" || change == "unlimited") != (err == nil) {
					t.Fatal("unexpected wait result", err)
				}
			case <-time.After(time.Second):
				t.Fatal("waiter not woken")
			}
			release()
			release()
			awaitCapacity(t, id, 0, 0)
		})
	}
}

func TestChannelCapacityLowerLimitCountsUnlimitedRequests(t *testing.T) {
	grant := newTestChannel(t, testName(t, "lower-limit"), "http://127.0.0.1", "upstream")
	id := channelIDOfGrant(t, grant)
	a, _ := acquireChannelCapacity(t.Context(), id, nil)
	defer a()
	b, _ := acquireChannelCapacity(t.Context(), id, nil)
	defer b()
	setConcurrency(t, id, 1)
	done := make(chan struct{})
	go func() {
		release, err := acquireChannelCapacity(t.Context(), id, nil)
		if err == nil {
			release()
		}
		close(done)
	}()
	awaitCapacity(t, id, 2, 1)
	a()
	awaitCapacity(t, id, 1, 1)
	select {
	case <-done:
		t.Fatal("lowered limit admitted while full")
	default:
	}
	b()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiter did not resume")
	}
	awaitCapacity(t, id, 0, 0)
}

func TestRelayChannelConcurrencyAcrossGroupsAndFailure(t *testing.T) {
	var active, peak, calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		defer active.Add(-1)
		time.Sleep(15 * time.Millisecond)
		if calls.Add(1)%3 == 1 {
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary failure"}}`)
			return
		}
		_, _ = io.WriteString(w, chatCompletionBody)
	}))
	defer upstream.Close()
	grant := newTestChannel(t, testName(t, "limited"), upstream.URL, "upstream")
	id := channelIDOfGrant(t, grant)
	setConcurrency(t, id, 2)
	config := testRelayConfig(1)
	config.ManualMaxAttempts = 1
	groups := []string{newTestGroup(t, model.GroupModeManual, config, grant), newTestGroup(t, model.GroupModeManual, config, grant)}
	engine := newRelayEngine()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, groups[i%2])))
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			engine.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
		}(i)
	}
	wg.Wait()
	if peak.Load() > 2 || calls.Load() != 10 {
		t.Fatalf("peak=%d calls=%d", peak.Load(), calls.Load())
	}
	awaitCapacity(t, id, 0, 0)
}

func TestRelayStreamHoldsCapacityAndQueuedCancel(t *testing.T) {
	arrived := make(chan struct{}, 4)
	finish := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"upstream"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		arrived <- struct{}{}
		select {
		case <-finish:
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	grant := newTestChannel(t, testName(t, "stream-limit"), upstream.URL, "upstream")
	id := channelIDOfGrant(t, grant)
	setConcurrency(t, id, 1)
	group := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grant)
	engine := newRelayEngine()
	start := func(ctx context.Context) <-chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"hi"}]}`, group)))
			engine.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
		}()
		return done
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := start(ctx)
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("stream not started")
	}
	queuedCtx, cancelQueued := context.WithCancel(ctx)
	queued := start(queuedCtx)
	awaitCapacity(t, id, 1, 1)
	cancelQueued()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("queued cancellation blocked")
	}
	awaitCapacity(t, id, 1, 0)
	previous, _ := op.SettingGetString(model.SettingKeyChannelAutoUpdateEnabled)
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, previous) })
	if err := op.SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	result, err := op.RefreshChannelModels(ctx, id, model.ChannelModelRefreshOptions{Automatic: true})
	if err != nil || !result.Success {
		t.Fatalf("refresh blocked by occupied relay capacity: %+v %v", result, err)
	}
	awaitCapacity(t, id, 1, 0)
	finish <- struct{}{}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("stream did not finish")
	}
	awaitCapacity(t, id, 0, 0)
	select {
	case <-arrived:
		t.Fatal("canceled queued request reached upstream")
	default:
	}
	activeCtx, cancelActive := context.WithCancel(ctx)
	active := start(activeCtx)
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("second stream not started")
	}
	cancelActive()
	select {
	case <-active:
	case <-time.After(time.Second):
		t.Fatal("active stream cancellation blocked")
	}
	awaitCapacity(t, id, 0, 0)
}

func TestRelayLimitedFailoverReleasesBothChannels(t *testing.T) {
	log := &upstreamCallLog{}
	failed := newFakeUpstream(t, "failed", log, http.StatusServiceUnavailable)
	healthy := newFakeUpstream(t, "healthy", log)
	first := newTestChannel(t, testName(t, "failed"), failed.server.URL, "upstream")
	second := newTestChannel(t, testName(t, "healthy"), healthy.server.URL, "upstream")
	firstID, secondID := channelIDOfGrant(t, first), channelIDOfGrant(t, second)
	setConcurrency(t, firstID, 1)
	setConcurrency(t, secondID, 1)
	group := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), first, second)
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"hi"}]}`, group)))
	response := httptest.NewRecorder()
	serveWithTimeout(t, newRelayEngine(), response, request)
	if response.Code != http.StatusOK || healthy.requestCount() != 1 || failed.requestCount() != 1 {
		t.Fatal("failover did not reach healthy channel")
	}
	awaitCapacity(t, firstID, 0, 0)
	awaitCapacity(t, secondID, 0, 0)
}

// 恢复探测遇到别组占满额度时仍应及时使用健康备用成员。
func TestRelayBusyRecoveryProbeUsesHealthyFallback(t *testing.T) {
	started := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer upstream.Close()
	healthy := newFakeUpstream(t, "healthy", &upstreamCallLog{})
	first := newTestChannel(t, testName(t, "busy-probe"), upstream.URL, "upstream")
	second := newTestChannel(t, testName(t, "fallback"), healthy.server.URL, "upstream")
	id := channelIDOfGrant(t, first)
	setConcurrency(t, id, 1)
	groupA := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), first)
	groupB := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), first, second)
	engine := newRelayEngine()
	streamCtx, stopStream := context.WithCancel(t.Context())
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		engine.ServeHTTP(httptest.NewRecorder(), chatRequest(t, groupA, true).WithContext(streamCtx))
	}()
	defer func() { stopStream(); <-streamDone }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("occupying stream not started")
	}
	group, err := op.GroupGetByName(groupB)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	selection := pickGroupItemAt(group, 0, old)
	recordRouteFailureAt(group, selection.Item.ID, 1, selection.Generation, 0, old)
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, chatRequest(t, groupB, false).WithContext(ctx))
		cancel()
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "chat.completion") {
			t.Fatalf("healthy fallback blocked by occupied probe: status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if healthy.requestCount() != 3 || RouteStateOf(group).ProbeItemID != 0 {
		t.Fatal("fallback requests or probe release incorrect")
	}
	awaitCapacity(t, id, 1, 0)
}

type capacityWakeContext struct {
	context.Context
	waits atomic.Int32
}

func (c *capacityWakeContext) Done() <-chan struct{} {
	c.waits.Add(1)
	return c.Context.Done()
}

func TestChannelCapacityFIFOHasLinearWakeups(t *testing.T) {
	grant := newTestChannel(t, testName(t, "fifo-wakeups"), "http://127.0.0.1", "upstream")
	id := channelIDOfGrant(t, grant)
	setConcurrency(t, id, 1)
	release, err := acquireChannelCapacity(t.Context(), id, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	const count = 16
	contexts := make([]*capacityWakeContext, count)
	admitted := make(chan int, count)
	releases := make(chan func(), count)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	for i := range contexts {
		contexts[i] = &capacityWakeContext{Context: ctx}
		go func(i int) {
			free, err := acquireChannelCapacity(contexts[i], id, nil)
			if err == nil {
				admitted <- i
				releases <- free
			}
		}(i)
		awaitCapacity(t, id, 1, i+1)
	}
	release()
	for i := range contexts {
		select {
		case next := <-admitted:
			if next != i {
				t.Errorf("FIFO admission=%d want=%d", next, i)
			}
		case <-time.After(time.Second):
			t.Fatal("queue stalled")
		}
		// 留出在途调用时间，使广播唤醒的等待者有机会重新入睡。
		time.Sleep(10 * time.Millisecond)
		(<-releases)()
	}
	var waits int32
	for _, ctx := range contexts {
		waits += ctx.waits.Load()
	}
	if waits > 4*count {
		t.Fatalf("excess queue wakeups: %d for %d waiters", waits, count)
	}
	awaitCapacity(t, id, 0, 0)
}
