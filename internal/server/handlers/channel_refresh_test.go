package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

var channelHandlerFixtureID atomic.Int64

func TestRefreshChannelModelsHandlerReturnsPerKeyResult(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") == "" && r.Header.Get("Authorization") != "Bearer handler-refresh-key" {
			t.Errorf("refresh credential changed: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Api-Key") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = fmt.Fprintf(w, `{"error":"unsupported endpoint %s"}`, r.Header.Get("X-Api-Key"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"handler-new-model"}]}`)
	}))
	defer upstream.Close()

	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    fmt.Sprintf("handler-refresh-%d", channelHandlerFixtureID.Add(1)),
			Enabled: true,
			BaseURL: upstream.URL,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "primary", Key: "handler-refresh-key", Enabled: true}},
		Models: []string{"handler-old-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "handler-old-model", KeyName: "primary", Protocols: model.ProtocolOpenAIResponse}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("create refresh handler fixture: %v", err)
	}

	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/refresh-model/%d", created.ID), strings.NewReader(`{"auto_add_groups":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("refresh handler returned %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Code int                             `json:"code"`
		Data model.ChannelModelRefreshResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	if envelope.Code != http.StatusOK || !envelope.Data.Success || len(envelope.Data.KeyResults) != 1 {
		t.Fatalf("unexpected refresh response: %+v", envelope)
	}
	keyResult := envelope.Data.KeyResults[0]
	if keyResult.KeyName != "primary" || !keyResult.Success || keyResult.ModelCount != 1 {
		t.Fatalf("unexpected per-key refresh result: %+v", keyResult)
	}
	if !strings.Contains(keyResult.Error, "anthropic:") || strings.Contains(keyResult.Error, "handler-refresh-key") {
		t.Fatalf("tolerated-side diagnostic missing or secret was not redacted: %+v", keyResult)
	}
	if envelope.Data.AddedModels == nil || len(envelope.Data.AddedModels) != 1 || envelope.Data.AddedModels[0] != "handler-new-model" {
		t.Fatalf("unexpected refresh delta: %+v", envelope.Data)
	}
	engine.GET("/stats", listChannelStats)
	stats := httptest.NewRecorder()
	engine.ServeHTTP(stats, httptest.NewRequest(http.MethodGet, "/stats", nil))
	for _, field := range []string{`"added_models"`, `"removed_models"`, `"key_results"`, `"warnings"`} {
		if strings.Contains(stats.Body.String(), field) {
			t.Errorf("stats contains full refresh diagnostic field %s", field)
		}
	}
	var statsEnvelope struct {
		Data []struct {
			ChannelID int `json:"channel_id"`
			Refresh   struct {
				Last struct {
					Status       string `json:"status"`
					AddedCount   int    `json:"added_count"`
					RemovedCount int    `json:"removed_count"`
				} `json:"last"`
			} `json:"refresh"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stats.Body.Bytes(), &statsEnvelope); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range statsEnvelope.Data {
		if item.ChannelID == created.ID {
			found = true
			if item.Refresh.Last.Status != "success" || item.Refresh.Last.AddedCount != 1 || item.Refresh.Last.RemovedCount != 1 {
				t.Errorf("refresh summary lost counts: %+v", item.Refresh)
			}
		}
	}
	if !found {
		t.Fatal("channel missing from stats")
	}
	engine.GET("/refresh-status/:id", getChannelRefreshStatus)
	diagnostic := httptest.NewRecorder()
	engine.ServeHTTP(diagnostic, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/refresh-status/%d", created.ID), nil))
	var diagnosticEnvelope struct {
		Data model.ChannelRefreshStatus `json:"data"`
	}
	if err := json.Unmarshal(diagnostic.Body.Bytes(), &diagnosticEnvelope); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Code != http.StatusOK || diagnosticEnvelope.Data.Last == nil ||
		len(diagnosticEnvelope.Data.Last.Result.KeyResults) != 1 || len(diagnosticEnvelope.Data.Last.Result.AddedModels) != 1 {
		t.Fatalf("full diagnostic unavailable: %s", diagnostic.Body.String())
	}
	for _, target := range []struct {
		id   string
		code int
	}{{"bad", 400}, {"0", 400}, {"999999999", 404}} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/refresh-status/"+target.id, nil))
		if response.Code != target.code {
			t.Errorf("diagnostic status=%d want=%d", response.Code, target.code)
		}
	}
}

func TestRefreshChannelModelsHandlerReportsPriceMaintenanceWarnings(t *testing.T) {
	oldModel := fmt.Sprintf("handler-refresh-warning-old-%d", channelHandlerFixtureID.Add(1))
	newModel := fmt.Sprintf("handler-refresh-warning-new-%d", channelHandlerFixtureID.Add(1))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") != "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"unsupported endpoint"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":[{"id":%q}]}`, newModel)
	}))
	defer upstream.Close()

	if err := op.LLMCreate(model.LLMInfo{Name: oldModel}, t.Context()); err != nil {
		t.Fatalf("create existing model price: %v", err)
	}
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    fmt.Sprintf("handler-refresh-warning-%d", channelHandlerFixtureID.Add(1)),
			Enabled: true,
			BaseURL: upstream.URL,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "primary", Key: "warning-key", Enabled: true}},
		Models: []string{oldModel},
		Grants: []model.ChannelGrantConfig{{ModelName: oldModel, KeyName: "primary", Protocols: model.ProtocolOpenAIResponse}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("create price warning fixture: %v", err)
	}
	if err := db.GetDB().Exec(`CREATE TRIGGER fail_refresh_llm_insert BEFORE INSERT ON llm_infos BEGIN SELECT RAISE(ABORT, 'forced price insert failure'); END`).Error; err != nil {
		t.Fatalf("create price insert trigger: %v", err)
	}
	if err := db.GetDB().Exec(`CREATE TRIGGER fail_refresh_llm_delete BEFORE DELETE ON llm_infos BEGIN SELECT RAISE(ABORT, 'forced price cleanup failure'); END`).Error; err != nil {
		_ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_refresh_llm_insert").Error
		t.Fatalf("create price cleanup trigger: %v", err)
	}
	t.Cleanup(func() {
		_ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_refresh_llm_insert").Error
		_ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_refresh_llm_delete").Error
		_ = op.LLMCleanupGhosts(t.Context())
	})

	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/refresh-model/%d", created.ID), strings.NewReader(`{"auto_add_groups":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("warning refresh returned %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data model.ChannelModelRefreshResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode warning refresh response: %v", err)
	}
	if !envelope.Data.Success || envelope.Data.Error != "" {
		t.Fatalf("price maintenance changed refresh success/error: %+v", envelope.Data)
	}
	if len(envelope.Data.Warnings) != 2 || !strings.Contains(envelope.Data.Warnings[0], "price update:") || !strings.Contains(envelope.Data.Warnings[1], "price cleanup:") {
		t.Fatalf("price maintenance warnings missing: %+v", envelope.Data.Warnings)
	}
}

func TestRefreshChannelModelsHandlerReturnsConflictWhileRefreshIsRunning(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		once.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer upstream.Close()

	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    fmt.Sprintf("handler-refresh-conflict-%d", channelHandlerFixtureID.Add(1)),
			Enabled: true,
			BaseURL: upstream.URL,
		},
		Keys:   []model.ChannelKeyConfig{{Name: "primary", Key: "conflict-key", Enabled: true}},
		Models: []string{"handler-old-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "handler-old-model", KeyName: "primary", Protocols: model.ProtocolOpenAIResponse}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("create concurrent refresh fixture: %v", err)
	}

	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	path := fmt.Sprintf("/refresh-model/%d", created.ID)
	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"auto_add_groups":false}`))
		request.Header.Set("Content-Type", "application/json")
		return request
	}
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, newRequest())
		firstDone <- response
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach upstream")
	}

	second := httptest.NewRecorder()
	engine.ServeHTTP(second, newRequest())
	if second.Code != http.StatusConflict {
		t.Fatalf("concurrent refresh returned %d: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), op.ErrChannelRefreshInProgress.Error()) {
		t.Fatalf("concurrent refresh diagnostic missing: %s", second.Body.String())
	}

	close(release)
	select {
	case first := <-firstDone:
		if first.Code != http.StatusOK {
			t.Fatalf("first refresh returned %d: %s", first.Code, first.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first refresh did not finish")
	}
}

func TestRefreshChannelModelsHandlerRedactsEachKeyError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		credential := r.Header.Get("Authorization")
		if credential == "" {
			credential = r.Header.Get("X-Api-Key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":"denied %s"}`, credential)
	}))
	defer upstream.Close()

	keys := []model.ChannelKeyConfig{
		{Name: "first", Key: "first-secret", Enabled: true},
		{Name: "second", Key: "second-secret", Enabled: true},
	}
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:    fmt.Sprintf("handler-refresh-redact-%d", channelHandlerFixtureID.Add(1)),
			Enabled: true,
			BaseURL: upstream.URL,
		},
		Keys:   keys,
		Models: []string{"handler-old-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "handler-old-model", KeyName: "first", Protocols: model.ProtocolOpenAIResponse}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("create redaction fixture: %v", err)
	}

	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/refresh-model/%d", created.ID), strings.NewReader(`{"auto_add_groups":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("redaction refresh returned %d: %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"first-secret", "second-secret", "Bearer first-secret", "Bearer second-secret"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("refresh response leaked %q: %s", secret, response.Body.String())
		}
	}
	var envelope struct {
		Data model.ChannelModelRefreshResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode redacted refresh response: %v", err)
	}
	if len(envelope.Data.KeyResults) != len(keys) {
		t.Fatalf("unexpected key results: %+v", envelope.Data)
	}
	for _, keyResult := range envelope.Data.KeyResults {
		if keyResult.Success || keyResult.Error == "" {
			t.Fatalf("missing failed key diagnostic: %+v", keyResult)
		}
	}
}

func TestRefreshChannelModelsHandlerResetsRouteAndPublishesGroupSnapshot(t *testing.T) {
	const oldModel = "handler-route-old"
	const newModel = "handler-route-new"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			if r.Header.Get("X-Api-Key") != "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"unsupported endpoint"}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"handler-route-new"}]}`)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"chatcmpl-handler-route","object":"chat.completion","created":1,"model":"handler-route-old","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name:                     fmt.Sprintf("handler-refresh-route-%d", channelHandlerFixtureID.Add(1)),
			Enabled:                  true,
			BaseURL:                  upstream.URL,
			OpenAIChatCompletionPath: "/v1/chat/completions",
			OpenAIResponsePath:       "/v1/responses",
			AnthropicMessagePath:     "/v1/messages",
		},
		Keys:   []model.ChannelKeyConfig{{Name: "primary", Key: "route-key", Enabled: true}},
		Models: []string{oldModel},
		Grants: []model.ChannelGrantConfig{{ModelName: oldModel, KeyName: "primary", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	created, err := op.ChannelCreate(detail, t.Context())
	if err != nil {
		t.Fatalf("create route refresh fixture: %v", err)
	}
	grantID := 0
	for _, candidate := range op.ChannelGrantCandidates() {
		if candidate.ChannelID == created.ID && candidate.ModelName == oldModel {
			grantID = candidate.ID
			break
		}
	}
	if grantID == 0 {
		t.Fatal("route refresh grant missing")
	}
	matchMode := model.GroupModelMatchModeExact
	groupName := newModel
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name:           groupName,
		ModelMatchMode: matchMode,
		Items:          []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatalf("create route refresh group: %v", err)
	}
	t.Cleanup(func() { relay.ResetRouteState(group.ID) })
	activeItemID := group.Items[0].ID
	group, err = op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &activeItemID}, t.Context())
	if err != nil {
		t.Fatalf("select route refresh member: %v", err)
	}
	relay.SetRouteConfig(*group)
	forward := gin.New()
	forward.POST("/forward", relay.Forward(llm.APIFormatOpenAIChatCompletion))
	forwardRequest := httptest.NewRequest(http.MethodPost, "/forward", strings.NewReader(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"ping"}]}`, groupName)))
	forwardRequest.Header.Set("Content-Type", "application/json")
	forwardResponse := httptest.NewRecorder()
	forward.ServeHTTP(forwardResponse, forwardRequest)
	if forwardResponse.Code != http.StatusOK {
		t.Fatalf("establish route returned %d: %s", forwardResponse.Code, forwardResponse.Body.String())
	}
	if state := relay.RouteStateOf(*group); state.CurrentItemID != activeItemID {
		t.Fatalf("route was not established before refresh: %+v", state)
	}

	subscriber := subscribeGroupEventForTest(t)
	engine := gin.New()
	engine.POST("/refresh-model/:id", refreshChannelModels)
	request := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/refresh-model/%d", created.ID), strings.NewReader(`{"auto_add_groups":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("route refresh returned %d: %s", response.Code, response.Body.String())
	}
	select {
	case event := <-subscriber:
		if event.Name != "changed" {
			t.Fatalf("route refresh event = %q, want changed", event.Name)
		}
		snapshot, ok := event.Data.(groupResponse)
		if !ok {
			t.Fatalf("route refresh event data = %T, want groupResponse", event.Data)
		}
		if len(snapshot.Items) != 1 || snapshot.Items[0].ModelName != newModel {
			t.Fatalf("route refresh snapshot members = %+v", snapshot.Items)
		}
		if snapshot.Runtime.CurrentItemID != 0 {
			t.Fatalf("route refresh kept removed member active: %+v", snapshot.Runtime)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("route refresh did not publish group snapshot")
	}
	updated, err := op.GroupGet(group.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Items) != 1 || updated.Items[0].ModelName != newModel || updated.ActiveItemID != 0 {
		t.Fatalf("route refresh group state = %+v", updated)
	}
}

func TestFetchModelHandlerRejectsInvalidAllowedProtocols(t *testing.T) {
	engine := gin.New()
	engine.POST("/fetch-model", fetchModel)
	payload, err := json.Marshal(model.ChannelFetchModelRequest{
		Channel: model.ChannelConfig{BaseURL: "http://127.0.0.1", AllowedProtocols: model.Protocol(1 << 7)},
		Key:     "handler-fetch-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/fetch-model", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid allowed protocols returned %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "invalid channel allowed protocols") {
		t.Fatalf("invalid protocol diagnostic missing: %s", response.Body.String())
	}
}

func TestGroupCandidatesHandlerOnlyReturnsAvailableGrants(t *testing.T) {
	create := func(t *testing.T, enabled, keyEnabled bool) *model.ChannelDetail {
		t.Helper()
		name := fmt.Sprintf("handler-candidate-%d", channelHandlerFixtureID.Add(1))
		detail, err := op.ChannelCreate(&model.ChannelDetail{
			ChannelConfig: model.ChannelConfig{Name: name, Enabled: enabled, BaseURL: "http://127.0.0.1"},
			Keys:          []model.ChannelKeyConfig{{Name: "primary", Key: name + "-key", Enabled: keyEnabled}},
			Models:        []string{"candidate-model"},
			Grants:        []model.ChannelGrantConfig{{ModelName: "candidate-model", KeyName: "primary", Protocols: model.ProtocolOpenAIResponse}},
		}, t.Context())
		if err != nil {
			t.Fatalf("create candidate fixture: %v", err)
		}
		if !enabled {
			if err := op.ChannelEnabled(detail.ID, false, t.Context()); err != nil {
				t.Fatalf("disable candidate fixture channel: %v", err)
			}
		}
		if !keyEnabled {
			detail.Keys[0].Enabled = false
			if _, err := op.ChannelUpdate(detail, t.Context()); err != nil {
				t.Fatalf("disable candidate fixture key: %v", err)
			}
		}
		return detail
	}
	available := create(t, true, true)
	disabledChannel := create(t, false, true)
	disabledKey := create(t, true, false)

	engine := gin.New()
	engine.GET("/candidates", getGroupCandidates)
	request := httptest.NewRequest(http.MethodGet, "/candidates", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("candidate handler returned %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Code int                           `json:"code"`
		Data []model.ChannelGrantCandidate `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode candidate response: %v", err)
	}
	if envelope.Code != http.StatusOK {
		t.Fatalf("unexpected candidate response: %+v", envelope)
	}
	seenAvailable := false
	for _, candidate := range envelope.Data {
		switch candidate.ChannelID {
		case available.ID:
			if !candidate.Available {
				t.Fatalf("available candidate marked unavailable: %+v", candidate)
			}
			seenAvailable = true
		case disabledChannel.ID, disabledKey.ID:
			t.Fatalf("unavailable candidate leaked into response: %+v", candidate)
		}
	}
	if !seenAvailable {
		t.Fatalf("available candidate missing from response: %+v", envelope.Data)
	}
}
