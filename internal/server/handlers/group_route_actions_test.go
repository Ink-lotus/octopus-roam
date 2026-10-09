package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

func requestRouteAction(groupID, itemID int, version string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	body, _ := json.Marshal(groupRouteRequest{ItemID: itemID, Version: version})
	request := httptest.NewRequest(http.MethodPost, "/route/"+strconv.Itoa(groupID), bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine := gin.New()
	engine.POST("/route/:id", handler)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func routeActionSnapshot(t *testing.T, response *httptest.ResponseRecorder, status int) groupResponse {
	t.Helper()
	if response.Code != status {
		t.Fatalf("route action status=%d, want %d: %s", response.Code, status, response.Body.String())
	}
	var envelope struct{ Data groupResponse }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func newRouteActionGroup(t *testing.T) (model.Group, int) {
	t.Helper()
	firstChannel, first, _ := newChannelRouteFixture(t)
	_, second, _ := newChannelRouteFixture(t)
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: fmt.Sprintf("route-action-%d", channelEventTestID.Add(1)), Mode: model.GroupModeFailover,
		RelayConfig: first.RelayConfig,
		Items:       []model.GroupItemInput{{ChannelGrantID: first.Items[0].ChannelGrantID}, {ChannelGrantID: second.Items[0].ChannelGrantID}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	relay.SetRouteConfig(*group)
	t.Cleanup(func() { relay.ResetRouteState(group.ID) })
	return *group, firstChannel
}

func TestGroupRouteSelectionUsesRuntimeAndRejectsOutdatedOperations(t *testing.T) {
	group, _ := newRouteActionGroup(t)
	initial := relay.RouteStateOf(group)
	selected := routeActionSnapshot(t, requestRouteAction(group.ID, group.Items[1].ID, initial.SelectionVersion, selectGroupRouteItem), http.StatusOK)
	if selected.Runtime.CurrentItemID != group.Items[1].ID || selected.Mode != model.GroupModeFailover {
		t.Fatal("selection did not remain in failover mode")
	}
	persisted, _ := op.GroupGet(group.ID)
	if persisted.ActiveItemID != 0 {
		t.Fatal("failover selection was persisted as a manual selection")
	}
	repeated := routeActionSnapshot(t, requestRouteAction(group.ID, group.Items[1].ID, selected.Runtime.SelectionVersion, selectGroupRouteItem), http.StatusOK)
	if repeated.Runtime.Version != selected.Runtime.Version {
		t.Fatal("repeat selection mutated runtime")
	}
	conflict := routeActionSnapshot(t, requestRouteAction(group.ID, group.Items[0].ID, initial.SelectionVersion, selectGroupRouteItem), http.StatusConflict)
	if conflict.Runtime.CurrentItemID != selected.Runtime.CurrentItemID || conflict.Runtime.Version != selected.Runtime.Version {
		t.Fatal("conflict did not return the latest group snapshot")
	}
	relay.ResetRouteStateForGroup(group)
	reset := routeActionSnapshot(t, requestRouteAction(group.ID, group.Items[0].ID, selected.Runtime.SelectionVersion, selectGroupRouteItem), http.StatusConflict)
	if reset.Runtime.CurrentItemID != 0 || reset.Runtime.ConfigVersion == selected.Runtime.ConfigVersion {
		t.Fatal("old selection survived a route reset")
	}
}

func TestConcurrentGroupRouteSelectionsUseOneExpectedVersion(t *testing.T) {
	group, _ := newRouteActionGroup(t)
	version := relay.RouteStateOf(group).SelectionVersion
	responses := make(chan int, 2)
	var workers sync.WaitGroup
	for _, item := range group.Items {
		workers.Go(func() { responses <- requestRouteAction(group.ID, item.ID, version, selectGroupRouteItem).Code })
	}
	workers.Wait()
	first, second := <-responses, <-responses
	if !((first == http.StatusOK && second == http.StatusConflict) || (second == http.StatusOK && first == http.StatusConflict)) {
		t.Fatalf("concurrent statuses = %d, %d", first, second)
	}
}

func TestGroupCooldownReleaseDoesNotSelectOrSendAndCannotReleaseNewCycle(t *testing.T) {
	group, firstChannel := newRouteActionGroup(t)
	var calls atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "fixture failure")
	}))
	defer failing.Close()
	detail, _ := op.ChannelDetailGet(firstChannel)
	detail.BaseURL = failing.URL
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	group, _ = op.GroupGet(group.ID)
	relay.NotifyRouteStateChanged(group)
	forward := func() {
		t.Helper()
		engine := gin.New()
		engine.POST("/forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
		request := httptest.NewRequest(http.MethodPost, "/forward", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}]}`, group.Name))).WithContext(t.Context())
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("local upstream flow failed: %s", response.Body.String())
		}
	}
	forward()
	cooling := relay.RouteStateOf(group)
	first := group.Items[0].ID
	if cooling.Cooldowns[first] <= time.Now().UnixMilli() || cooling.CurrentItemID != group.Items[1].ID {
		t.Fatal("fixture did not fail over from a cooling member")
	}
	routeActionSnapshot(t, requestRouteAction(group.ID, first, cooling.SelectionVersion, selectGroupRouteItem), http.StatusConflict)
	released := routeActionSnapshot(t, requestRouteAction(group.ID, first, cooling.CooldownVersions[first], releaseGroupRouteCooldown), http.StatusOK)
	if released.Runtime.CurrentItemID != cooling.CurrentItemID || released.Runtime.Cooldowns[first] != 0 || calls.Load() != 1 {
		t.Fatal("release selected a member or sent an upstream probe")
	}
	routeActionSnapshot(t, requestRouteAction(group.ID, first, cooling.CooldownVersions[first], releaseGroupRouteCooldown), http.StatusConflict)
	routeActionSnapshot(t, requestRouteAction(group.ID, first, released.Runtime.SelectionVersion, selectGroupRouteItem), http.StatusOK)
	forward()
	newCycle := relay.RouteStateOf(group)
	routeActionSnapshot(t, requestRouteAction(group.ID, first, cooling.CooldownVersions[first], releaseGroupRouteCooldown), http.StatusConflict)
	if after := relay.RouteStateOf(group); after.Cooldowns[first] != newCycle.Cooldowns[first] || calls.Load() != 2 {
		t.Fatal("old confirmation affected a new cooldown cycle")
	}
}

func TestGroupRouteActionsCheckLatestModeMembershipAndAvailability(t *testing.T) {
	for _, change := range []string{"disabled channel", "disabled credential", "removed member", "manual mode", "deleted group"} {
		t.Run(change, func(t *testing.T) {
			group, channelID := newRouteActionGroup(t)
			version := relay.RouteStateOf(group).SelectionVersion
			itemID := group.Items[0].ID
			switch change {
			case "disabled channel", "disabled credential":
				detail, _ := op.ChannelDetailGet(channelID)
				if change == "disabled channel" {
					detail.Enabled = false
				} else {
					detail.Keys[0].Enabled = false
				}
				body, _ := json.Marshal(detail)
				req := httptest.NewRequest(http.MethodPost, "/update", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				serveHandler(t, http.MethodPost, "/update", updateChannel, req)
			case "removed member", "manual mode":
				body := `{"mode":"manual"}`
				if change == "removed member" {
					body = `{"items":[]}`
				}
				req := httptest.NewRequest(http.MethodPost, "/update/"+strconv.Itoa(group.ID), strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				serveHandler(t, http.MethodPost, "/update/:id", updateGroup, req)
			case "deleted group":
				req := httptest.NewRequest(http.MethodDelete, "/delete/"+strconv.Itoa(group.ID), nil)
				serveHandler(t, http.MethodDelete, "/delete/:id", deleteGroup, req)
			}
			want := http.StatusConflict
			if change == "deleted group" {
				want = http.StatusNotFound
			}
			for _, action := range []gin.HandlerFunc{selectGroupRouteItem, releaseGroupRouteCooldown} {
				if response := requestRouteAction(group.ID, itemID, version, action); response.Code != want {
					t.Fatalf("%s action status=%d, want %d", change, response.Code, want)
				}
			}
		})
	}
}
