package handlers

import (
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

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

func configurationRequest(t *testing.T, engine *gin.Engine, method, path string, body any, cookie *http.Cookie, apiKey string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func configurationData[T any](t *testing.T, response *httptest.ResponseRecorder) T {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("configuration request failed: %d %s", response.Code, response.Body.String())
	}
	var envelope struct{ Data T }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

// The registered-route integration test owns the one RegisterAll call.
func testConfigurationValues(t *testing.T, engine *gin.Engine) {
	t.Helper()
	user := model.User{Username: "fixture-w03-admin", Password: "fixture-w03-password"}
	if err := user.HashPassword(); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := op.UserInit(); err != nil {
		t.Fatal(err)
	}
	for _, login := range []model.UserLogin{
		{Username: user.Username, Password: "wrong-fixture"},
		{Username: "wrong-fixture", Password: "fixture-w03-password"},
	} {
		if response := configurationRequest(t, engine, "POST", "/api/v1/user/login", login, nil, ""); response.Code != http.StatusUnauthorized {
			t.Fatal("invalid login was accepted")
		}
	}
	login := configurationRequest(t, engine, "POST", "/api/v1/user/login", model.UserLogin{Username: user.Username, Password: "fixture-w03-password", Expire: 60}, nil, "")
	if login.Code != http.StatusOK {
		t.Fatal("synthetic login failed")
	}
	var cookie *http.Cookie
	for _, candidate := range login.Result().Cookies() {
		if candidate.Name == "auth" {
			cookie = candidate
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("login did not issue a session")
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/v1/channel/detail/1"}, {"POST", "/api/v1/channel/create"},
		{"POST", "/api/v1/channel/update"}, {"GET", "/api/v1/webhook/list"},
		{"POST", "/api/v1/webhook/create"}, {"POST", "/api/v1/webhook/update/1"},
	} {
		for _, credential := range []*http.Cookie{nil, {Name: "auth", Value: "fixture-invalid-session"}} {
			if response := configurationRequest(t, engine, route.method, route.path, map[string]any{}, credential, ""); response.Code != http.StatusUnauthorized {
				t.Fatalf("configuration route bypassed authentication: %s", route.path)
			}
		}
	}
	t.Run("channel-values", func(t *testing.T) { testChannelConfigurationValues(t, engine, cookie) })
	t.Run("webhook-values", func(t *testing.T) { testWebhookConfigurationValues(t, engine, cookie) })
}

func testChannelConfigurationValues(t *testing.T, engine *gin.Engine, cookie *http.Cookie) {
	type receivedConfig struct {
		authorization, custom, path string
		body                        map[string]any
	}
	received := make(chan receivedConfig, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- receivedConfig{request.Header.Get("Authorization"), request.Header.Get("X-Config"), request.URL.Path, body}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "{\"id\":\"fixture\",\"object\":\"chat.completion\",\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}")
	}))
	defer upstream.Close()
	detail := model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name: "fixture-w03-channel", Enabled: true, BaseURL: upstream.URL,
			CustomHeader:  []model.CustomHeader{{HeaderKey: "X-Config", HeaderValue: "fixture-header-initial"}},
			ParamOverride: "{\"temperature\":0.1}",
		},
		Keys:   []model.ChannelKeyConfig{{Name: "fixture-key", Key: "fixture-key-initial", Enabled: true}},
		Models: []string{"fixture-model"},
		Grants: []model.ChannelGrantConfig{{ModelName: "fixture-model", KeyName: "fixture-key", Protocols: model.ProtocolOpenAIChatCompletion}},
	}
	detail = configurationData[model.ChannelDetail](t, configurationRequest(t, engine, "POST", "/api/v1/channel/create", detail, cookie, ""))
	read := func() model.ChannelDetail {
		return configurationData[model.ChannelDetail](t, configurationRequest(t, engine, "GET", fmt.Sprintf("/api/v1/channel/detail/%d", detail.ID), nil, cookie, ""))
	}
	grantID := 0
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == detail.ID {
			grantID = grant.ID
		}
	}
	if grantID == 0 {
		t.Fatal("created channel has no grant")
	}
	config := model.DefaultGroupRelayConfig()
	config.ManualMaxAttempts = 1
	group, err := op.GroupCreate(&model.GroupCreateRequest{
		Name: "fixture-w03-group", Mode: model.GroupModeManual, RelayConfig: config,
		Items: []model.GroupItemInput{{ChannelGrantID: grantID}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	active := group.Items[0].ID
	if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &active}, t.Context()); err != nil {
		t.Fatal(err)
	}
	apiKey := model.APIKey{Name: "fixture-w03-client", APIKey: "fixture-w03-api-key", Enabled: true}
	if err := op.APIKeyCreate(&apiKey, t.Context()); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"model": group.Name, "messages": []map[string]string{{"role": "user", "content": "fixture prompt"}}}
	for _, key := range []string{"", "fixture-invalid-key"} {
		if response := configurationRequest(t, engine, "POST", "/v1/chat/completions", payload, nil, key); response.Code != http.StatusUnauthorized {
			t.Fatal("invalid API key was accepted")
		}
	}
	for _, mode := range []string{"disabled", "expired"} {
		apiKey.Enabled, apiKey.ExpireAt = mode != "disabled", 0
		if mode == "expired" {
			apiKey.ExpireAt = time.Now().Add(-time.Minute).Unix()
		}
		if err := op.APIKeyUpdate(&apiKey, t.Context()); err != nil {
			t.Fatal(err)
		}
		if response := configurationRequest(t, engine, "POST", "/v1/chat/completions", payload, nil, apiKey.APIKey); response.Code != http.StatusUnauthorized {
			t.Fatalf("%s API key was accepted", mode)
		}
	}
	apiKey.Enabled, apiKey.ExpireAt = true, 0
	if err := op.APIKeyUpdate(&apiKey, t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(received) != 0 {
		t.Fatal("unauthorized requests reached upstream")
	}
	for _, phase := range []string{"unchanged", "modified", "optional-cleared"} {
		t.Run(phase, func(t *testing.T) {
			detail = read()
			switch phase {
			case "modified":
				detail.Keys[0].Key = "fixture-key-modified"
				detail.CustomHeader[0].HeaderValue = "fixture-header-modified"
				detail.OpenAIChatCompletionPath = "/custom-chat"
			case "optional-cleared":
				detail.CustomHeader = []model.CustomHeader{}
				detail.ParamOverride = ""
			}
			saved := configurationData[model.ChannelDetail](t, configurationRequest(t, engine, "POST", "/api/v1/channel/update", detail, cookie, ""))
			if !reflect.DeepEqual(saved, detail) {
				t.Fatal("save changed submitted configuration values")
			}
			if err := op.InitCache(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(read(), detail) {
				t.Fatal("configuration did not persist")
			}
			response := configurationRequest(t, engine, "POST", "/v1/chat/completions", payload, nil, apiKey.APIKey)
			if response.Code != http.StatusOK {
				t.Fatalf("forward failed: %d %s", response.Code, response.Body.String())
			}
			call := <-received
			expectedHeader := ""
			if len(detail.CustomHeader) > 0 {
				expectedHeader = detail.CustomHeader[0].HeaderValue
			}
			if call.authorization != "Bearer "+detail.Keys[0].Key || call.custom != expectedHeader || call.path != detail.OpenAIChatCompletionPath || call.body["model"] != "fixture-model" {
				t.Fatal("actual forwarding did not use the saved values")
			}
			if _, exists := call.body["temperature"]; exists != (detail.ParamOverride != "") {
				t.Fatal("clearing a parameter override did not change forwarding")
			}
		})
	}
	before := read()
	invalid := before
	invalid.BaseURL = ""
	if response := configurationRequest(t, engine, "POST", "/api/v1/channel/update", invalid, cookie, ""); response.Code < 400 {
		t.Fatal("empty required URL was accepted")
	}
	if !reflect.DeepEqual(before, read()) {
		t.Fatal("failed validation changed the channel")
	}
	detail = read()
	detail.Keys[0].Key = ""
	configurationData[model.ChannelDetail](t, configurationRequest(t, engine, "POST", "/api/v1/channel/update", detail, cookie, ""))
	if read().Keys[0].Key != "" {
		t.Fatal("explicit key clearing restored an old value")
	}
}

func testWebhookConfigurationValues(t *testing.T, engine *gin.Engine, cookie *http.Cookie) {
	type receivedHook struct{ authorization, query string }
	received := make(chan receivedHook, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		received <- receivedHook{request.Header.Get("Authorization"), request.URL.RawQuery}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	endpoint := model.WebhookEndpoint{
		Name: "fixture-w03-hook", Enabled: true, URL: upstream.URL + "/hook?token=fixture-initial",
		Headers: map[string]string{"Authorization": "Bearer fixture-initial"}, BodyTemplate: "{}",
	}
	endpoint = configurationData[model.WebhookEndpoint](t, configurationRequest(t, engine, "POST", "/api/v1/webhook/create", endpoint, cookie, ""))
	t.Cleanup(func() { _ = op.WebhookEndpointDelete(context.Background(), endpoint.ID) })
	read := func() model.WebhookEndpoint {
		response := configurationRequest(t, engine, "GET", "/api/v1/webhook/list", nil, cookie, "")
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("configuration list is cacheable")
		}
		for _, item := range configurationData[[]model.WebhookEndpoint](t, response) {
			if item.ID == endpoint.ID {
				return item
			}
		}
		t.Fatal("saved webhook missing")
		return model.WebhookEndpoint{}
	}
	for _, phase := range []string{"unchanged", "modified", "cleared", "literal-placeholder"} {
		t.Run(phase, func(t *testing.T) {
			endpoint = read()
			switch phase {
			case "modified":
				endpoint.URL = upstream.URL + "/hook?token=fixture-modified"
				endpoint.Headers["Authorization"] = "Bearer fixture-modified"
			case "cleared":
				endpoint.URL = upstream.URL + "/hook"
				endpoint.Headers = map[string]string{}
			case "literal-placeholder":
				endpoint.Headers = map[string]string{"Authorization": "***"}
			}
			saved := configurationData[model.WebhookEndpoint](t, configurationRequest(t, engine, "POST", fmt.Sprintf("/api/v1/webhook/update/%d", endpoint.ID), endpoint, cookie, ""))
			stored, err := op.WebhookEndpointGet(t.Context(), endpoint.ID)
			if err != nil || !reflect.DeepEqual(saved, endpoint) || !reflect.DeepEqual(stored, endpoint) || !reflect.DeepEqual(read(), endpoint) {
				t.Fatal("webhook round-trip changed configuration values", err)
			}
			response := configurationRequest(t, engine, "POST", fmt.Sprintf("/api/v1/webhook/test/%d", endpoint.ID), map[string]any{}, cookie, "")
			result := configurationData[map[string]any](t, response)
			if result["success"] != true {
				t.Fatal("webhook test delivery failed")
			}
			call := <-received
			query := ""
			if _, after, found := strings.Cut(endpoint.URL, "?"); found {
				query = after
			}
			if call.authorization != endpoint.Headers["Authorization"] || call.query != query {
				t.Fatal("webhook delivery changed the saved values")
			}
		})
	}
	before := read()
	for _, required := range []string{"name", "url"} {
		invalid := before
		if required == "name" {
			invalid.Name = ""
		} else {
			invalid.URL = ""
		}
		if response := configurationRequest(t, engine, "POST", fmt.Sprintf("/api/v1/webhook/update/%d", endpoint.ID), invalid, cookie, ""); response.Code != http.StatusBadRequest {
			t.Fatal("empty required webhook field was accepted")
		}
		if !reflect.DeepEqual(before, read()) {
			t.Fatal("failed validation changed the webhook")
		}
	}
}
