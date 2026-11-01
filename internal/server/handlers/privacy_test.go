package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	serverauth "github.com/Ink-lotus/octopus-roam/internal/server/auth"
	"github.com/Ink-lotus/octopus-roam/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func TestModelDiscoveryErrorsPreserveSubmittedValues(t *testing.T) {
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
		t.Fatalf("unexpected response: %d", response.Code)
	}
	for _, text := range []string{"denied", key, custom} {
		if !strings.Contains(response.Body.String(), text) {
			t.Fatal("discovery error lost original content")
		}
	}
}

type unreadRetiredBody struct{ reads int }

func (body *unreadRetiredBody) Read([]byte) (int, error) {
	body.reads++
	return 0, errors.New("retired request bodies must not be read")
}
func (*unreadRetiredBody) Close() error { return nil }

// Called by the existing registered-route integration test: RegisterAll drains
// the registry, so all registered-route assertions use the same real engine.
func testRetiredSettingRoutes(t *testing.T, engine *gin.Engine) {
	t.Helper()
	token, _, err := serverauth.GenerateJWTToken(60)
	if err != nil {
		t.Fatal(err)
	}
	keys := []model.SettingKey{model.SettingKeyPrivacyProtectionEnabled, model.SettingKeyPrivacyProtectionRules}
	var before []model.Setting
	if err := db.GetDB().Where(map[string]any{"key": keys}).Order("key").Find(&before).Error; err != nil {
		t.Fatal(err)
	}
	assertGone := func(response *httptest.ResponseRecorder) {
		t.Helper()
		var envelope struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Reason string `json:"reason"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusGone || envelope.Code != http.StatusGone || envelope.Message != model.ErrSettingRetired.Error() || envelope.Data.Reason != "feature_removed" || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("retired operation did not return the removal contract: %s", response.Body.String())
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/setting/privacy-rules"},
		{http.MethodPost, "/api/v1/setting/privacy-rules"},
		{http.MethodPost, "/api/v1/setting/privacy-preview"},
	} {
		t.Run(route.method+route.path, func(t *testing.T) {
			for _, cookie := range []string{"", "fixture-invalid-session"} {
				request := httptest.NewRequest(route.method, route.path, strings.NewReader("invalid retired sample"))
				if cookie != "" {
					request.AddCookie(&http.Cookie{Name: "auth", Value: cookie})
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatal("retired management endpoint bypassed authentication")
				}
			}
			probe := &unreadRetiredBody{}
			request := httptest.NewRequest(route.method, route.path, nil)
			request.Body = probe
			request.AddCookie(&http.Cookie{Name: "auth", Value: token})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			assertGone(response)
			if probe.reads != 0 {
				t.Fatal("retired management endpoint read its body")
			}
		})
	}
	for _, key := range keys {
		for _, value := range []string{`"not-json-legacy-fixture"`, `"true"`, `"yes"`, `{}`, `null`, `123`} {
			input := `{"key":"` + string(key) + `","value":` + value + `}`
			for _, authenticated := range []bool{false, true} {
				request := httptest.NewRequest(http.MethodPost, "/api/v1/setting/set", strings.NewReader(input))
				request.Header.Set("Content-Type", "application/json")
				if authenticated {
					request.AddCookie(&http.Cookie{Name: "auth", Value: token})
				}
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)
				if authenticated {
					assertGone(response)
				} else if response.Code != http.StatusUnauthorized {
					t.Fatal("retired setting write bypassed authentication")
				}
			}
		}
	}
	for _, input := range []string{`{`, `{"key":"proxy_url","value":{}}`} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/setting/set", strings.NewReader(input))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: "auth", Value: token})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatal("ordinary JSON/value validation was weakened")
		}
	}
	var after []model.Setting
	if err := db.GetDB().Where(map[string]any{"key": keys}).Order("key").Find(&after).Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retired management requests changed legacy rows")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/setting/list", nil)
	request.AddCookie(&http.Cookie{Name: "auth", Value: token})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	var list struct {
		Data []model.Setting `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatal("setting list failed")
	}
	for _, setting := range list.Data {
		if model.IsRetiredSetting(setting.Key) {
			t.Fatal("setting list exposed a retired key")
		}
	}
	var unauthorized resp.ResponseStruct
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/setting/list", nil))
	if err := json.Unmarshal(response.Body.Bytes(), &unauthorized); err != nil {
		t.Fatal(err)
	}
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatal("setting list bypassed authentication")
	}
}
