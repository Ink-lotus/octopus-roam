package webhook

import (
	"context"
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
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
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
	health := relay.MemberHealth{Version: 1, FailureStreak: 9, RetryCount: 0, LastError: "upstream unavailable"}
	state := relay.HealthSnapshot{GroupID: groupID, ConfigVersion: 1, Members: map[int]relay.MemberHealth{0: health}}
	service.observeHealth(state)
	if len(service.events) != 0 {
		t.Fatal("manual failure emitted before the tenth failure")
	}
	health.FailureStreak = 10
	state.Members[0] = health
	service.observeHealth(state)
	failure := assertWebhookEvent(t, service.events, "failure", groupID)
	if failure.FailureStreak != 10 || failure.RetryCount != 0 || failure.Threshold != 10 {
		t.Fatalf("manual failure event fields = %+v", failure)
	}
	service.observeHealth(state)
	if len(service.events) != 0 {
		t.Fatal("same manual failure emitted more than once")
	}
	health.FailureStreak = 0
	health.Epoch++
	state.Members[0] = health
	service.observeHealth(state)
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
	state := relay.HealthSnapshot{GroupID: groupID, ConfigVersion: 1, Cooldowns: map[int]int64{
		1: futureDeadline, 2: futureDeadline, 3: futureDeadline, 4: expiredDeadline,
	}}
	service.observeHealth(state)
	failure := assertWebhookEvent(t, service.events, "failure", groupID)
	if failure.FailedChannels != 3 || failure.Threshold != 3 {
		t.Fatalf("failover failure event fields = %+v", failure)
	}
	service.observeHealth(state)
	if len(service.events) != 0 {
		t.Fatal("same failover condition emitted more than once")
	}
	state.Cooldowns = map[int]int64{1: futureDeadline, 2: futureDeadline, 3: expiredDeadline}
	service.observeHealth(state)
	if len(service.events) != 0 {
		t.Fatal("cooldown expiry alone emitted recovery")
	}
	state.RecoveryVersion++
	service.observeHealth(state)
	recovery := assertWebhookEvent(t, service.events, "recovery", groupID)
	if recovery.FailedChannels != 2 {
		t.Fatalf("failover recovery event fields = %+v", recovery)
	}
	service.observeHealth(state)
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
	state := relay.HealthSnapshot{GroupID: groupID, ConfigVersion: 1, Members: map[int]relay.MemberHealth{0: {Version: 1, FailureStreak: 10, LastError: "upstream unavailable"}}}
	startedAt := time.Now()
	// Feed the observer channel rather than mutate its state from this goroutine.
	service.healthUpdates <- state
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
	previous, err := op.SettingGetString(model.SettingKeyPrivacyProtectionEnabled)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, previous) })
	if err := op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, "true"); err != nil {
		t.Fatal(err)
	}
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
	group, err = op.GroupGet(group.ID)
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
	if err := op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if redactConfiguredError(group, message) != message {
		t.Fatal("disabled privacy changed notification diagnostics")
	}
	body, err := Render(DefaultBodyTemplate, Event{LastError: "api_key=fixture-disabled-privacy"})
	if err != nil || !strings.Contains(string(body), "fixture-disabled-privacy") {
		t.Fatal("disabled privacy changed the rendered notification", err)
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

func TestIndependentHTTPFailuresNotifyAndRecoverFromSharedHealth(t *testing.T) {
	if err := op.SettingSetInt(model.SettingKeyWebhookManualFailureThreshold, 2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.SettingSetInt(model.SettingKeyWebhookManualFailureThreshold, 10) })
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-test\",\"status\":\"in_progress\"}}\n\n")
		if calls.Add(1) <= 2 {
			fmt.Fprint(w, "data: {\"type\":\"error\",\"message\":\"upstream unavailable\"}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")
		}
	}))
	defer upstream.Close()
	channel, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "webhook-http", Enabled: true, BaseURL: upstream.URL, Dialect: model.DialectGeneric}, Keys: []model.ChannelKeyConfig{{Name: "test", Key: "fixture-key", Enabled: true}}, Models: []string{"upstream"}, Grants: []model.ChannelGrantConfig{{ModelName: "upstream", KeyName: "test", Protocols: model.ProtocolOpenAIResponse}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	grantID := 0
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			grantID = grant.ID
		}
	}
	config := model.DefaultGroupRelayConfig()
	config.MemberRetryIntervalSeconds = 1
	config.ManualMaxAttempts = 1
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "webhook-http-group", Mode: model.GroupModeManual, RelayConfig: config, Items: []model.GroupItemInput{{ChannelGrantID: grantID}}}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	active := group.Items[0].ID
	if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &active}, t.Context()); err != nil {
		t.Fatal(err)
	}
	service := testService()
	service.ctx, service.cancel = context.WithCancel(t.Context())
	service.observeDone = make(chan struct{})
	service.initialHealth, service.healthUpdates = relay.OpenHealthStream()
	service.wg.Add(1)
	go service.observe()
	t.Cleanup(func() {
		service.cancel()
		<-service.observeDone
		relay.CloseHealthStream(service.healthUpdates)
		service.wg.Wait()
	})
	engine := gin.New()
	engine.POST("/v1/responses", relay.Forward(llm.APIFormatOpenAIResponse))
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"input":"ping"}`, group.Name))).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(httptest.NewRecorder(), req)
		cancel()
		if i == 2 {
			event := assertWebhookEvent(t, service.events, "failure", group.ID)
			if event.FailureStreak != 2 || event.RetryCount != 0 {
				t.Fatalf("wrong shared fields: %+v", event)
			}
			relay.Clear()
		}
		if i == 3 {
			assertWebhookEvent(t, service.events, "recovery", group.ID)
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("calls=%d", calls.Load())
	}
	if len(service.events) != 0 {
		t.Fatal("duplicate notification")
	}
}

func TestHealthNotificationReconnectAndIdentityChange(t *testing.T) {
	groupID := createWebhookTestGroup(t, "epochs", model.GroupModeManual)
	service := testService()
	h := relay.MemberHealth{Version: 1, FailureStreak: 10}
	s := relay.HealthSnapshot{GroupID: groupID, ConfigVersion: 1, Members: map[int]relay.MemberHealth{0: h}}
	service.observeHealth(s)
	assertWebhookEvent(t, service.events, "failure", groupID)
	service.observeHealth(s)
	if len(service.events) != 0 {
		t.Fatal("snapshot replay duplicated failure")
	}
	h.Epoch++
	s.Members[0] = h
	service.observeHealth(s)
	assertWebhookEvent(t, service.events, "recovery", groupID)
	assertWebhookEvent(t, service.events, "failure", groupID)
	h.Version++
	h.Epoch = 0
	s.Members[0] = h
	service.observeHealth(s)
	assertWebhookEvent(t, service.events, "failure", groupID)
	if len(service.events) != 0 {
		t.Fatal("configuration replacement fabricated recovery")
	}
}

func TestCommittedHTTPFailuresDriveFailoverNotifications(t *testing.T) {
	if err := op.SettingSetInt(model.SettingKeyWebhookFailoverChannelThreshold, 2); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.SettingSetInt(model.SettingKeyWebhookFailoverChannelThreshold, 3) })
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n")
		if calls.Add(1) <= 2 {
			fmt.Fprint(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"committed failure\"}}\n\n")
		} else {
			fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer upstream.Close()
	var items []model.GroupItemInput
	for i := 0; i < 3; i++ {
		channel, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("failover-http-%d", i), Enabled: true, BaseURL: upstream.URL, Dialect: model.DialectGeneric}, Keys: []model.ChannelKeyConfig{{Name: "test", Key: "fixture-key", Enabled: true}}, Models: []string{"upstream"}, Grants: []model.ChannelGrantConfig{{ModelName: "upstream", KeyName: "test", Protocols: model.ProtocolOpenAIChatCompletion}}}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, grant := range op.ChannelGrantCandidates() {
			if grant.ChannelID == channel.ID {
				items = append(items, model.GroupItemInput{ChannelGrantID: grant.ID})
			}
		}
	}
	config := model.DefaultGroupRelayConfig()
	config.MemberMaxAttempts = 1
	config.MemberCooldownSeconds = 60
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "failover-http-group", Mode: model.GroupModeFailover, RelayConfig: config, Items: items}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	service := testService()
	engine := gin.New()
	engine.POST("/v1/chat/completions", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(fmt.Sprintf(`{"model":%q,"stream":true,"messages":[{"role":"user","content":"ping"}]}`, group.Name))).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(httptest.NewRecorder(), req)
		cancel()
		snapshot, updates := relay.OpenHealthStream()
		relay.CloseHealthStream(updates)
		for _, state := range snapshot {
			if state.GroupID == group.ID {
				service.observeHealth(state)
			}
		}
		if calls.Load() != int32(i) {
			t.Fatal("committed request was replayed")
		}
		if i == 2 {
			event := assertWebhookEvent(t, service.events, "failure", group.ID)
			if event.FailedChannels != 2 || !strings.Contains(event.LastError, "committed failure") {
				t.Fatalf("failure fields: %+v", event)
			}
		}
	}
	// A successful spare member cannot reduce the still-active cooldown count.
	if len(service.events) != 0 {
		t.Fatal("recovery emitted while threshold still exceeded")
	}
	snapshot, updates := relay.OpenHealthStream()
	relay.CloseHealthStream(updates)
	for _, state := range snapshot {
		if state.GroupID == group.ID {
			// A success while the threshold was exceeded cannot license a later
			// timer/probe-start event to fabricate recovery.
			for id := range state.Cooldowns {
				state.Cooldowns[id] = time.Now().Add(-time.Second).UnixMilli()
			}
			service.observeHealth(state)
			if len(service.events) != 0 {
				t.Fatal("expiry reused an earlier success to fabricate recovery")
			}
			state.RecoveryVersion++
			state.FailedChannelsAtRecovery = 0
			service.observeHealth(state)
		}
	}
	assertWebhookEvent(t, service.events, "recovery", group.ID)
}
