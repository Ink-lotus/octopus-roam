package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/privacy"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func TestModelDiscoveryErrorsRedactSubmittedCredentials(t *testing.T) {
	enableHandlerPrivacy(t)
	const key, custom = "fixture-discovery-key", "fixture-discovery-custom"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key && r.Header.Get("X-API-Key") != key {
			t.Error("discovery credential changed")
		}
		if r.Header.Get("X-Private-Custom") != custom {
			t.Error("custom header changed")
		}
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "denied "+key+" "+custom)
	}))
	defer upstream.Close()
	payload, _ := json.Marshal(model.ChannelFetchModelRequest{
		Channel: model.ChannelConfig{BaseURL: upstream.URL, CustomHeader: []model.CustomHeader{{HeaderKey: "X-Private-Custom", HeaderValue: custom}}},
		Key:     key,
	})
	engine := gin.New()
	engine.POST("/fetch", fetchModel)
	request := httptest.NewRequest(http.MethodPost, "/fetch", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), custom) {
		t.Fatalf("discovery error leaked credentials: %s", response.Body)
	}
	if !strings.Contains(response.Body.String(), "denied") {
		t.Fatal("discovery error lost its diagnostic context")
	}
}

func privacyHandlerRequest(handler gin.HandlerFunc, input string) *httptest.ResponseRecorder {
	engine := gin.New()
	engine.POST("/", middleware.RequireJSON(), handler)
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(input))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func TestPrivacyHandlerCRUDConflictValidationAndPreview(t *testing.T) {
	initial, _ := op.PrivacyRulesGet()
	create := map[string]any{"revision": initial.Revision, "action": "create", "rule": map[string]any{"name": "客户编号", "kind": "field_name", "pattern": "customer_id", "enabled": true}}
	data, _ := json.Marshal(create)
	response := privacyHandlerRequest(mutatePrivacyRules, string(data))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body.String())
	}
	var decoded struct {
		Data op.PrivacyRules `json:"data"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	created := decoded.Data.CustomRules[len(decoded.Data.CustomRules)-1]
	t.Cleanup(func() {
		view, _ := op.PrivacyRulesGet()
		_, _ = op.PrivacyRulesMutate(context.Background(), op.PrivacyMutation{Revision: view.Revision, Action: "delete", ID: created.ID})
	})
	if privacyHandlerRequest(mutatePrivacyRules, string(data)).Code != http.StatusConflict {
		t.Fatal("stale editor was not rejected")
	}
	preview := privacy.PreviewRequest{Revision: decoded.Data.Revision, Mode: "current", RuleID: created.ID, InputType: "json", Input: `{"customer_id":"fixture-customer"}`}
	data, _ = json.Marshal(preview)
	response = privacyHandlerRequest(previewPrivacy, string(data))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "fixture-customer") || !strings.Contains(response.Body.String(), created.ID) {
		t.Fatal("preview response invalid", response.Body.String())
	}
	view, _ := op.PrivacyRulesGet()
	if view.Revision != decoded.Data.Revision {
		t.Fatal("preview wrote configuration")
	}
	for _, test := range []struct {
		action, id string
		code       int
	}{{"delete", privacy.JSONFields, 400}, {"delete", "custom_missing", 404}} {
		data, _ := json.Marshal(map[string]any{"revision": view.Revision, "action": test.action, "id": test.id})
		if got := privacyHandlerRequest(mutatePrivacyRules, string(data)); got.Code != test.code {
			t.Fatal("wrong mutation error", got.Code, got.Body.String())
		}
	}
	preview.Input = strings.Repeat("x", privacy.MaxPreviewBytes+1)
	data, _ = json.Marshal(preview)
	if response := privacyHandlerRequest(previewPrivacy, string(data)); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("preview size limit not enforced")
	}
	if response := privacyHandlerRequest(previewPrivacy, strings.Repeat(" ", privacy.MaxRequestBytes+1)); response.Code != http.StatusRequestEntityTooLarge {
		t.Fatal("transport limit not enforced")
	}
	if response := privacyHandlerRequest(setSetting, `{"key":"privacy_protection_rules","value":"{}"}`); response.Code != http.StatusBadRequest {
		t.Fatal("generic route bypassed rule interface")
	}
	preview.Revision = initial.Revision
	preview.Input = "fixture-sample"
	data, _ = json.Marshal(preview)
	if response := privacyHandlerRequest(previewPrivacy, string(data)); response.Code != http.StatusConflict {
		t.Fatal("stale preview was accepted")
	}
}

func TestPrivacyRoutesRequireAuthentication(t *testing.T) {
	for _, handler := range []gin.HandlerFunc{getPrivacyRules, mutatePrivacyRules, previewPrivacy} {
		engine := gin.New()
		engine.POST("/", middleware.Auth(), handler)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}")))
		if response.Code != http.StatusUnauthorized {
			t.Fatal("privacy route was accessible without authentication")
		}
	}
}
