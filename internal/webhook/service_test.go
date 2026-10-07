package webhook

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octopus-webhook-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create webhook test directory: %v\n", err)
		os.Exit(1)
	}
	if err := db.InitDB("sqlite", filepath.Join(dir, "octopus.db"), false); err != nil {
		fmt.Fprintf(os.Stderr, "initialize webhook test database: %v\n", err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := op.InitCache(); err != nil {
		fmt.Fprintf(os.Stderr, "initialize webhook test cache: %v\n", err)
		_ = db.Close()
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = db.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestManualFailureNotificationDeduplicatesUntilRecovery(t *testing.T) {
	groupID := createWebhookTestGroup(t, "manual", model.GroupModeManual)
	service := testService()
	request := relay.RequestState{
		ID: 10, GroupID: groupID, Status: relay.StatusRunning, ChannelStatus: relay.ChannelStatusError,
		TargetChannel: "provider-a", ErrorCount: 9, RetryCount: 8, LastError: "upstream unavailable",
	}
	service.observeRequest(request)
	if len(service.events) != 0 {
		t.Fatal("manual failure emitted before the tenth failure")
	}
	request.ErrorCount = 10
	request.RetryCount = 9
	service.observeRequest(request)
	failure := assertWebhookEvent(t, service.events, "failure", groupID)
	if failure.FailureStreak != 10 || failure.RetryCount != 9 || failure.Threshold != 10 {
		t.Fatalf("manual failure event fields = %+v", failure)
	}
	service.observeRequest(request)
	if len(service.events) != 0 {
		t.Fatal("same manual failure emitted more than once")
	}
	request.Status = relay.StatusSuccess
	request.ChannelStatus = relay.ChannelStatusNormal
	service.observeRequest(request)
	assertWebhookEvent(t, service.events, "recovery", groupID)
}

func TestFailoverNotificationUsesCooldownCountAndRecoversOnce(t *testing.T) {
	groupID := createWebhookTestGroup(t, "failover", model.GroupModeFailover)
	if err := op.SettingSetInt(model.SettingKeyWebhookFailoverChannelThreshold, 3); err != nil {
		t.Fatalf("set failover threshold: %v", err)
	}
	t.Cleanup(func() { _ = op.SettingSetInt(model.SettingKeyWebhookFailoverChannelThreshold, 3) })
	service := testService()
	now := time.Now()
	futureDeadline := now.Add(time.Minute).UnixMilli()
	expiredDeadline := now.Add(-time.Minute).UnixMilli()
	state := relay.RouteState{GroupID: groupID, Cooldowns: map[int]int64{
		1: futureDeadline, 2: futureDeadline, 3: futureDeadline, 4: expiredDeadline,
	}}
	service.observeRoute(state)
	failure := assertWebhookEvent(t, service.events, "failure", groupID)
	if failure.FailedChannels != 3 || failure.Threshold != 3 {
		t.Fatalf("failover failure event fields = %+v", failure)
	}
	service.observeRoute(state)
	if len(service.events) != 0 {
		t.Fatal("same failover condition emitted more than once")
	}
	state.Cooldowns = map[int]int64{1: futureDeadline, 2: futureDeadline, 3: expiredDeadline}
	service.observeRoute(state)
	recovery := assertWebhookEvent(t, service.events, "recovery", groupID)
	if recovery.FailedChannels != 2 {
		t.Fatalf("failover recovery event fields = %+v", recovery)
	}
	service.observeRoute(state)
	if len(service.events) != 0 {
		t.Fatal("same recovery emitted more than once")
	}
}

func TestSendTestRetriesTransientFailures(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) < 3 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	endpoint := model.WebhookEndpoint{
		Name: "test", Enabled: true, URL: server.URL, Method: http.MethodPost,
		ContentType: "application/json", Headers: map[string]string{},
		BodyTemplate: DefaultBodyTemplate, TimeoutSeconds: 2,
	}
	result := SendTest(t.Context(), endpoint)
	if !result.Success || result.StatusCode != http.StatusNoContent || requests.Load() != 3 {
		t.Fatalf("SendTest result = %+v, requests = %d", result, requests.Load())
	}
}

func TestWebhookEndpointHeadersPersistAndRoundTrip(t *testing.T) {
	endpoint := model.WebhookEndpoint{
		Name: "persist", Enabled: true, URL: "https://example.com/hook", Method: "POST",
		ContentType: "application/json", Headers: map[string]string{"Authorization": "Bearer private", "X-Region": "west"},
		BodyTemplate: DefaultBodyTemplate, TimeoutSeconds: 10,
	}
	if err := op.WebhookEndpointCreate(t.Context(), &endpoint); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	loaded, err := op.WebhookEndpointGet(t.Context(), endpoint.ID)
	if err != nil {
		t.Fatalf("read endpoint: %v", err)
	}
	if loaded.Headers["Authorization"] != "Bearer private" || loaded.Headers["X-Region"] != "west" {
		t.Fatalf("persisted headers = %#v", loaded.Headers)
	}
	loaded.Enabled = false
	loaded.Headers["X-Region"] = "east"
	if err := op.WebhookEndpointUpdate(t.Context(), endpoint.ID, &loaded); err != nil {
		t.Fatalf("update endpoint: %v", err)
	}
	updated, err := op.WebhookEndpointGet(t.Context(), endpoint.ID)
	if err != nil {
		t.Fatalf("read updated endpoint: %v", err)
	}
	if updated.Enabled || updated.Headers["Authorization"] != "Bearer private" || updated.Headers["X-Region"] != "east" {
		t.Fatalf("updated endpoint = %+v", updated)
	}
	dump, err := op.DBExportAll(t.Context())
	if err != nil {
		t.Fatalf("export database: %v", err)
	}
	if len(dump.WebhookEndpoints) == 0 || dump.WebhookEndpoints[0].Headers["Authorization"] != "Bearer private" {
		t.Fatalf("exported webhook endpoints = %+v", dump.WebhookEndpoints)
	}
	if _, err := op.DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatalf("import current backup with endpoints: %v", err)
	}
}

func TestNotificationDeliveryRunsOutsideRelayProducer(t *testing.T) {
	started := make(chan struct{}, 1)
	releaseHandler := make(chan struct{})
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		select {
		case <-request.Context().Done():
		case <-releaseHandler:
		}
	}))
	endpoint := model.WebhookEndpoint{
		Name: "slow target", Enabled: true, URL: target.URL, Method: "POST", ContentType: "application/json",
		Headers: map[string]string{}, BodyTemplate: DefaultBodyTemplate, TimeoutSeconds: 10,
	}
	if err := op.WebhookEndpointCreate(t.Context(), &endpoint); err != nil {
		t.Fatalf("create slow endpoint: %v", err)
	}
	service := Start()
	t.Cleanup(func() {
		service.Close()
		close(releaseHandler)
		target.Close()
	})
	groupID := createWebhookTestGroup(t, "slow", model.GroupModeManual)
	request := relay.RequestState{
		ID: 77, GroupID: groupID, Status: relay.StatusRunning, ChannelStatus: relay.ChannelStatusError,
		ErrorCount: 10, LastError: "upstream unavailable",
	}
	startedAt := time.Now()
	service.observeRequest(request)
	if elapsed := time.Since(startedAt); elapsed > 100*time.Millisecond {
		t.Fatalf("notification producer waited %s for the endpoint", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("asynchronous delivery did not reach the slow endpoint")
	}
}

func TestNotificationErrorsRedactConfiguredCredentials(t *testing.T) {
	baseURL := "https://user:pass@upstream.example/v1?token=query-secret"
	headerSecret := "Bearer custom-secret"
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		Name: "redaction-channel", Dialect: model.DialectGeneric, Enabled: true, BaseURL: baseURL,
		CustomHeader: []model.CustomHeader{{HeaderKey: "Authorization", HeaderValue: headerSecret}},
	}}
	if err := db.GetDB().Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "default", Key: "upstream-api-secret", Enabled: false}}
	if err := db.GetDB().Create(&key).Error; err != nil {
		t.Fatalf("create channel key: %v", err)
	}
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", key.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel key: %v", err)
	}
	channelModel := model.ChannelModel{ChannelID: channel.ID, Name: "upstream-model"}
	if err := db.GetDB().Create(&channelModel).Error; err != nil {
		t.Fatalf("create channel model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: channelModel.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := db.GetDB().Create(&grant).Error; err != nil {
		t.Fatalf("create channel grant: %v", err)
	}
	group := model.Group{Name: "redaction-group", Mode: model.GroupModeManual, RelayConfig: model.DefaultGroupRelayConfig()}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	item := model.GroupItem{GroupID: group.ID, ChannelGrantID: grant.ID, Priority: 1}
	if err := db.GetDB().Create(&item).Error; err != nil {
		t.Fatalf("create group item: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}
	group, err := op.GroupGet(group.ID)
	if err != nil {
		t.Fatalf("read group: %v", err)
	}
	if _, err := op.ChannelGrantGet(grant.ID); err == nil {
		t.Fatal("disabled channel key should not be available for relay")
	}
	message := "request failed for upstream-api-secret at " + baseURL + " with " + headerSecret
	redacted := redactConfiguredError(group, message)
	for _, secret := range []string{"upstream-api-secret", baseURL, headerSecret, "query-secret", "pass"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("notification error leaked %q: %s", secret, redacted)
		}
	}
}

func TestNotificationEnqueueDoesNotWaitForDeliveryCapacity(t *testing.T) {
	service := testService()
	for range cap(service.events) {
		service.events <- Event{Event: "failure"}
	}
	started := time.Now()
	if service.enqueue(Event{Event: "recovery", GroupID: 1}) {
		t.Fatal("enqueue should report a full notification queue")
	}
	if time.Since(started) > 100*time.Millisecond {
		t.Fatal("enqueue waited for notification delivery")
	}
}

func createWebhookTestGroup(t *testing.T, name string, mode model.GroupMode) int {
	t.Helper()
	group := model.Group{Name: fmt.Sprintf("webhook-%s-%d", name, time.Now().UnixNano()), Mode: mode, RelayConfig: model.DefaultGroupRelayConfig()}
	if err := db.GetDB().Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("refresh cache: %v", err)
	}
	return group.ID
}

func testService() *Service {
	return &Service{
		events:         make(chan Event, 8),
		manualActive:   make(map[int]bool),
		failoverActive: make(map[int]bool),
	}
}

func assertWebhookEvent(t *testing.T, events <-chan Event, name string, groupID int) Event {
	t.Helper()
	select {
	case event := <-events:
		if event.Event != name || event.GroupID != groupID {
			t.Fatalf("event = %+v, want %s for group %d", event, name, groupID)
		}
		return event
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s event", name)
		return Event{}
	}
}
