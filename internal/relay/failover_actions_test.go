package relay

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestFailoverReleaseWakesSameRequestWithFreshAttemptBudget(t *testing.T) {
	upstream := newFakeUpstream(t, "release-budget", &upstreamCallLog{}, 503, 503, 503)
	grant := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream")
	name := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), grant)
	group, _ := op.GroupGetByName(name)
	stream := OpenRouteStream()
	defer CloseRouteStream(stream)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	response := httptest.NewRecorder()
	engine := newRelayEngine()
	go func() {
		defer close(done)
		engine.ServeHTTP(response, chatRequest(t, name, false).WithContext(ctx))
	}()
	var cooling RouteState
	for cooling.Cooldowns[group.Items[0].ID] == 0 {
		select {
		case state := <-stream:
			if state.GroupID == group.ID {
				cooling = state
			}
		case <-ctx.Done():
			t.Fatal("request never waited on cooldown")
		}
	}
	if stats := ChannelConcurrencyStats(group.Items[0].ChannelID); stats.Active != 0 {
		// The health event precedes the current attempt's deferred capacity release.
		awaitCapacity(t, group.Items[0].ChannelID, 0, 0)
	}
	released, err := ReleaseRouteCooldown(group, group.Items[0].ID, cooling.CooldownVersions[group.Items[0].ID])
	if err != nil || released.CurrentItemID != 0 {
		t.Fatalf("release actively selected a member: %+v, %v", released, err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("release did not wake the waiting request")
	}
	if response.Code != http.StatusOK || upstream.requestCount() != 4 {
		t.Fatalf("renewed request status=%d calls=%d", response.Code, upstream.requestCount())
	}
	states, requests := OpenRequestStream()
	CloseRequestStream(requests)
	for _, state := range states {
		if state.GroupID == group.ID {
			if state.Attempt != 2 || state.Round != 4 || state.ErrorCount != 3 {
				t.Fatalf("request budget/history did not survive cooldown: %+v", state)
			}
			return
		}
	}
	t.Fatal("missing completed request")
}

func TestFailoverCardSelectionPreservesSentRequestAndReroutesCapacityWaiter(t *testing.T) {
	arrived := make(chan struct{}, 1)
	finish := make(chan struct{})
	interrupted := make(chan struct{}, 1)
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-finish:
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, chatCompletionBody)
		case <-r.Context().Done():
			interrupted <- struct{}{}
		}
	}))
	defer blocked.Close()
	spare := newFakeUpstream(t, "spare", &upstreamCallLog{})
	a := newTestChannel(t, testName(t, "a"), blocked.URL, "a-model")
	b := newTestChannel(t, testName(t, "b"), spare.server.URL, "b-model")
	name := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), a, b)
	group, _ := op.GroupGetByName(name)
	setConcurrency(t, group.Items[0].ChannelID, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	engine := newRelayEngine()
	firstDone, secondDone := make(chan struct{}), make(chan struct{})
	firstResponse, secondResponse := httptest.NewRecorder(), httptest.NewRecorder()
	go func() {
		defer close(firstDone)
		engine.ServeHTTP(firstResponse, chatRequest(t, name, false).WithContext(ctx))
	}()
	select {
	case <-arrived:
	case <-ctx.Done():
		t.Fatal("first request did not send")
	}
	go func() {
		defer close(secondDone)
		engine.ServeHTTP(secondResponse, chatRequest(t, name, false).WithContext(ctx))
	}()
	awaitCapacity(t, group.Items[0].ChannelID, 1, 1)
	state := RouteStateOf(group)
	if _, err := SelectRouteItem(group, group.Items[1].ID, state.SelectionVersion); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondDone:
	case <-ctx.Done():
		t.Fatal("waiting request did not reselect")
	}
	select {
	case <-interrupted:
		t.Fatal("card selection interrupted a sent request")
	default:
	}
	close(finish)
	select {
	case <-firstDone:
	case <-ctx.Done():
		t.Fatal("original request did not finish")
	}
	if firstResponse.Code != http.StatusOK || secondResponse.Code != http.StatusOK || spare.requestCount() != 1 {
		t.Fatal("card selection did not preserve both requests")
	}
	if RouteStateOf(group).CurrentItemID != group.Items[1].ID || ChannelConcurrencyStats(group.Items[0].ChannelID).Active != 0 {
		t.Fatal("old result reclaimed selection or leaked capacity")
	}
}

func TestFailoverLogSelectionInterruptsOnlyOriginalWaitingRound(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer blocked.Close()
	spare := newFakeUpstream(t, "log-spare", &upstreamCallLog{})
	a := newTestChannel(t, testName(t, "a"), blocked.URL, "a-model")
	b := newTestChannel(t, testName(t, "b"), spare.server.URL, "b-model")
	name := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), a, b)
	group, _ := op.GroupGetByName(name)
	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	done := make(chan struct{})
	response := httptest.NewRecorder()
	engine := newRelayEngine()
	go func() {
		defer close(done)
		engine.ServeHTTP(response, chatRequest(t, name, false).WithContext(ctx))
	}()
	var waiting RequestState
	for !waiting.Sending {
		select {
		case state := <-stream:
			if state.GroupID == group.ID {
				waiting = state
			}
		case <-ctx.Done():
			t.Fatal("request did not enter the first round")
		}
	}
	state := RouteStateOf(group)
	if _, err := SelectRouteItem(group, group.Items[1].ID, state.SelectionVersion); err != nil {
		t.Fatal(err)
	}
	Interrupt(waiting.ID, waiting.Round)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("interrupted request did not reselect")
	}
	Interrupt(waiting.ID, waiting.Round)
	if response.Code != http.StatusOK || spare.requestCount() != 1 {
		t.Fatal("log round interruption did not finish on the selected member")
	}
	if h := healthOf(t, group.ID).Members[group.Items[0].ID]; h.FailureStreak != 0 {
		t.Fatal("manual round interruption counted as an upstream failure")
	}
}
