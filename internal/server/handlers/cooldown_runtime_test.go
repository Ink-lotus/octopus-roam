package handlers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

type cooldownHTTPFixture struct {
	server  *httptest.Server
	token   string
	channel int
	grants  []int
	groups  []model.Group
}

func newCooldownHTTPFixture(t *testing.T, seconds int64) cooldownHTTPFixture {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"fixture-quota","message":"Fixture quota reached"}}`)
	}))
	t.Cleanup(upstream.Close)
	detail, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("w06-http-%d", channelEventTestID.Add(1)), Enabled: true, BaseURL: upstream.URL,
		CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: seconds, Scope: model.CooldownScopeModel}}},
		Keys: []model.ChannelKeyConfig{{Name: "a", Key: "fixture-credential-a", Enabled: true}, {Name: "b", Key: "fixture-credential-b", Enabled: true}}, Models: []string{"fixture-model-a"},
		Grants: []model.ChannelGrantConfig{{ModelName: "fixture-model-a", KeyName: "a", Protocols: model.ProtocolAll}, {ModelName: "fixture-model-a", KeyName: "b", Protocols: model.ProtocolAll}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	fixture := cooldownHTTPFixture{channel: detail.ID}
	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ChannelID == detail.ID {
			fixture.grants = append(fixture.grants, candidate.ID)
		}
	}
	for range 2 {
		fixture.groups = append(fixture.groups, fixture.newGroup(t))
	}
	engine := gin.New()
	// RegisterAll consumes the global registry; the existing runtime endpoint
	// test owns that check. Mount the production handlers and middleware here.
	channels := engine.Group("/api/v1/channel", middleware.Auth(), middleware.RequireJSON())
	channels.GET("/stats", listChannelStats)
	channels.POST("/update", updateChannel)
	groups := engine.Group("/api/v1/group", middleware.Auth(), middleware.RequireJSON())
	groups.GET("/list", getGroupList)
	groups.GET("/get/:id", getGroup)
	groups.GET("/events", streamGroupEvents)
	engine.POST("/fixture-forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	fixture.server = httptest.NewServer(engine)
	t.Cleanup(fixture.server.Close)
	fixture.token, _, err = serverauth.GenerateJWTToken(60)
	if err != nil {
		t.Fatal("could not create fixture authentication")
	}
	return fixture
}

func (f cooldownHTTPFixture) newGroup(t *testing.T) model.Group {
	t.Helper()
	config := model.DefaultGroupRelayConfig()
	config.ManualMaxAttempts = 1
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: fmt.Sprintf("w06-http-group-%d", channelEventTestID.Add(1)), Mode: model.GroupModeManual, RelayConfig: config,
		Items: []model.GroupItemInput{{ChannelGrantID: f.grants[0]}, {ChannelGrantID: f.grants[1]}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	selected := group.Items[0].ID
	group, err = op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &selected}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	relay.SetRouteConfig(*group)
	return *group
}

func (f cooldownHTTPFixture) request(t *testing.T, method, path string, value any) []byte {
	t.Helper()
	var body []byte
	var err error
	if value != nil {
		body, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, _ := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "auth", Value: f.token})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("fixture API status=%d error=%v", response.StatusCode, err)
	}
	return raw
}

func (f cooldownHTTPFixture) fail(t *testing.T, group model.Group) {
	t.Helper()
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"fixture"}]}`, group.Name)
	request, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, f.server.URL+"/fixture-forward", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("trigger returned %d", response.StatusCode)
	}
}

type cooldownHTTPEvent struct {
	name string
	data json.RawMessage
}

func groupSubscriberCount() int {
	groupEventMu.Lock()
	defer groupEventMu.Unlock()
	return len(groupEventStreams)
}

func (f cooldownHTTPFixture) subscribe(t *testing.T) (<-chan cooldownHTTPEvent, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	events := make(chan cooldownHTTPEvent, 128)
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(events)
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.server.URL+"/api/v1/group/events", nil)
		request.AddCookie(&http.Cookie{Name: "auth", Value: f.token})
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			if ctx.Err() == nil {
				t.Error("SSE fixture connection failed")
			}
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("authenticated SSE connection returned %d", response.StatusCode)
			return
		}
		if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
			t.Error("SSE fixture received a non-stream response")
			return
		}
		close(ready)
		scanner := bufio.NewScanner(response.Body)
		var name, data string
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			case line == "" && name != "":
				select {
				case events <- cooldownHTTPEvent{name: name, data: json.RawMessage(data)}:
				case <-ctx.Done():
					return
				}
				name, data = "", ""
			}
		}
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("SSE cancellation left a reader")
		}
	}
	t.Cleanup(stop)
	select {
	case <-ready:
	case <-done:
		t.Fatal("SSE connection ended before registration")
	case <-time.After(3 * time.Second):
		t.Fatal("SSE registration did not complete")
	}
	return events, stop
}

func awaitCooldownHTTPState(t *testing.T, events <-chan cooldownHTTPEvent, channelID, hits int, groups []model.Group) relay.ChannelCooldownState {
	t.Helper()
	seen := make(map[int]bool)
	var channel relay.ChannelCooldownState
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	for channel.ChannelID == 0 || len(seen) < len(groups) {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("SSE ended before the required state")
			}
			switch event.name {
			case "channel-cooldown":
				var value relay.ChannelCooldownState
				if json.Unmarshal(event.data, &value) == nil && value.ChannelID == channelID && len(value.RuleCooldowns) == hits {
					channel = value
				}
			case "runtime":
				var value relay.RouteState
				if json.Unmarshal(event.data, &value) == nil && len(value.RuleCooldowns) == hits {
					for _, group := range groups {
						if value.GroupID == group.ID {
							seen[value.GroupID] = true
						}
					}
				}
			}
		case <-timer.C:
			t.Fatal("channel/group SSE state was incomplete")
		}
	}
	return channel
}

func TestCooldownHTTPStateSSEAndReconnect(t *testing.T) {
	fixture := newCooldownHTTPFixture(t, 60)
	for _, path := range []string{"/api/v1/channel/stats", "/api/v1/group/list", "/api/v1/group/events"} {
		response, err := http.Get(fixture.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("runtime state bypassed management authentication")
		}
	}
	first, disconnect := fixture.subscribe(t)
	second, _ := fixture.subscribe(t)
	unvisitedBefore := relay.RouteStateOf(fixture.groups[1])
	fixture.fail(t, fixture.groups[0])
	for _, events := range []<-chan cooldownHTTPEvent{first, second} {
		state := awaitCooldownHTTPState(t, events, fixture.channel, 1, fixture.groups)
		if state.RuleCooldowns[0].ModelName != "fixture-model-a" || state.RuleCooldowns[0].RuleIndex != 0 {
			t.Fatal("state did not identify the actual rule/model")
		}
	}
	var stats struct{ Data []channelStatsResponse }
	raw := fixture.request(t, http.MethodGet, "/api/v1/channel/stats", nil)
	if err := json.Unmarshal(raw, &stats); err != nil || strings.Contains(string(raw), "fixture-credential") {
		t.Fatal("channel state exposed credentials or invalid JSON")
	}
	found := false
	for _, stat := range stats.Data {
		if stat.ChannelID == fixture.channel {
			state := relay.ChannelCooldownStateOf(fixture.channel)
			found = len(stat.RuleCooldowns) == 1 && reflect.DeepEqual(stat.RuleCooldowns, state.RuleCooldowns) && stat.RuleCooldownVersion == state.Version
		}
	}
	if !found {
		t.Fatal("channel stats did not carry current versioned state")
	}
	var list struct{ Data []groupResponse }
	if err := json.Unmarshal(fixture.request(t, http.MethodGet, "/api/v1/group/list", nil), &list); err != nil {
		t.Fatal(err)
	}
	for _, group := range fixture.groups {
		var detail struct{ Data groupResponse }
		if err := json.Unmarshal(fixture.request(t, http.MethodGet, fmt.Sprintf("/api/v1/group/get/%d", group.ID), nil), &detail); err != nil || len(detail.Data.Runtime.RuleCooldowns) != 1 {
			t.Fatal("group detail lost/doubled shared state across keys")
		}
		found := false
		for _, item := range list.Data {
			if item.ID == group.ID {
				found = reflect.DeepEqual(item.Runtime, detail.Data.Runtime)
			}
		}
		if !found {
			t.Fatal("group list/detail snapshots disagree")
		}
	}
	unvisitedAfter := relay.RouteStateOf(fixture.groups[1])
	if unvisitedBefore.SelectionVersion != unvisitedAfter.SelectionVersion || unvisitedBefore.ConfigVersion != unvisitedAfter.ConfigVersion {
		t.Fatal("unvisited rule notice changed selection/configuration versions")
	}
	before := relay.RouteStateOf(fixture.groups[0])
	disconnect()
	detail, _ := op.ChannelDetailGet(fixture.channel)
	detail.CooldownRules[0].CooldownSeconds = 61
	fixture.request(t, http.MethodPost, "/api/v1/channel/update", detail)
	awaitCooldownHTTPState(t, second, fixture.channel, 0, fixture.groups)
	after := relay.RouteStateOf(fixture.groups[0])
	if before.SelectionVersion != after.SelectionVersion || before.ConfigVersion != after.ConfigVersion || len(after.RuleCooldowns) != 0 {
		t.Fatal("pure rule HTTP update changed original route generations")
	}
	reconnected, _ := fixture.subscribe(t)
	var recovered struct{ Data []channelStatsResponse }
	_ = json.Unmarshal(fixture.request(t, http.MethodGet, "/api/v1/channel/stats", nil), &recovered)
	for _, stat := range recovered.Data {
		if stat.ChannelID == fixture.channel && (stat.RuleCooldowns == nil || len(stat.RuleCooldowns) != 0) {
			t.Fatal("reconnect GET did not recover the versioned empty state")
		}
	}
	freshGroup := fixture.newGroup(t)
	fixture.fail(t, freshGroup)
	awaitCooldownHTTPState(t, reconnected, fixture.channel, 1, fixture.groups)
}

func TestCooldownHTTPExpiryNotice(t *testing.T) {
	fixture := newCooldownHTTPFixture(t, 1)
	events, _ := fixture.subscribe(t)
	fixture.fail(t, fixture.groups[0])
	active := awaitCooldownHTTPState(t, events, fixture.channel, 1, fixture.groups)
	expired := awaitCooldownHTTPState(t, events, fixture.channel, 0, fixture.groups)
	if active.Version == expired.Version || len(relay.ChannelCooldownStateOf(fixture.channel).RuleCooldowns) != 0 {
		t.Fatal("expiry did not publish a newer empty state")
	}
}

func TestCooldownHTTPSubscriptionCancellation(t *testing.T) {
	fixture := newCooldownHTTPFixture(t, 60)
	before := groupSubscriberCount()
	_, stop := fixture.subscribe(t)
	if groupSubscriberCount() != before+1 {
		t.Fatal("SSE headers were flushed before registration completed")
	}
	stop()
	deadline := time.Now().Add(3 * time.Second)
	for groupSubscriberCount() != before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if groupSubscriberCount() != before {
		t.Fatal("canceled SSE subscription remained registered")
	}
}
