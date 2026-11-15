package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/codexcatalog"
	"github.com/Ink-lotus/octopus-roam/internal/conf"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

type catalogRoundTrip func(*http.Request) (*http.Response, error)

func (f catalogRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func catalogTestRequest(engine *gin.Engine, path, key string, anthropic bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		if anthropic {
			request.Header.Set("X-API-Key", key)
		} else {
			request.Header.Set("Authorization", "Bearer "+key)
		}
	}
	request.Header.Set("X-Client-Private", "private-fixture-header")
	request.AddCookie(&http.Cookie{Name: "fixture-cookie", Value: "private-fixture-cookie"})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func catalogSlugs(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()
	if response.Code != 200 {
		t.Fatalf("catalog status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("ETag") != "" || response.Header().Get("Content-Length") == "987654" {
		t.Fatal("catalog response metadata did not match the filtered body")
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	result := make([]string, 0, len(catalog.Models))
	for _, item := range catalog.Models {
		result = append(result, item.Slug)
	}
	return result
}

// The existing runtime integration test owns RegisterAll; exercise the real new
// route in that same engine rather than making a second, incomplete registry.
func testCodexCatalogRoutes(t *testing.T, engine *gin.Engine) {
	const mainModel = "c01-main"
	const reviewer = "gpt-5.6-luna"
	const raw = ` { "future":{"precise":9007199254740993}, "models":[
{"slug":"c01-main","auto_review_model_override":null,"instructions":"\u003c\n","future":{"nested":[1,2]}},
{"slug":"gpt-5.6-luna","supported_in_api":true,"auto_review_model_override":"official-reviewer"},
{"slug":"c01-other","capability":{"tools":true}}
] } `
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	service := codexcatalog.New(func() (http.RoundTripper, error) {
		return catalogRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			if r.URL.Scheme != "https" || r.URL.Host != "raw.githubusercontent.com" || r.URL.RawQuery != "" ||
				r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Client-Private") != "" {
				t.Error("client information or target escaped to the catalog source")
			}
			status, body := 200, raw
			switch r.URL.Path {
			case "/openai/codex/rust-v0.169.0/codex-rs/models-manager/models.json":
			case "/openai/codex/rust-v0.170.0/codex-rs/models-manager/models.json":
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			case "/openai/codex/rust-v99.1.2/codex-rs/models-manager/models.json":
				body = `{"models":[{"slug":"c01-main","different_version":true}]}`
			default:
				status, body = 404, "private-fixture-upstream-body"
			}
			return &http.Response{StatusCode: status, Header: http.Header{"ETag": {"upstream-etag"}, "Content-Length": {"987654"}},
				Body: io.NopCloser(strings.NewReader(body))}, nil
		}), nil
	})
	previous := catalogService.Swap(service)
	t.Cleanup(func() { catalogService.Store(previous); service.Close() })
	group := func(name string) *model.Group {
		t.Helper()
		value, err := op.GroupCreate(&model.GroupCreateRequest{Name: name, Mode: model.GroupModeManual, RelayConfig: model.DefaultGroupRelayConfig()}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := op.GroupGet(value.ID); err == nil {
				_ = op.GroupDel(value.ID, context.Background())
			}
		})
		return value
	}
	group(mainModel)
	luna := group(reviewer)
	group("c01-other")
	key := func(name string, allowed []string) model.APIKey {
		t.Helper()
		value := model.APIKey{Name: name, APIKey: "synthetic-" + name, Enabled: true, SupportedModels: allowed}
		if err := op.APIKeyCreate(&value, t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = op.APIKeyDelete(value.ID, context.Background()) })
		return value
	}
	a, b := key("c01-a", []string{mainModel}), key("c01-b", []string{reviewer})
	url := "/codex/models?client_version=0.169.0"
	for _, credential := range []string{"", "invalid-synthetic-key"} {
		if response := catalogTestRequest(engine, url, credential, false); response.Code != 401 {
			t.Fatal("missing/invalid key accepted")
		}
	}
	for _, mode := range []string{"disabled", "expired", "over-budget"} {
		bad := key("c01-"+mode, nil)
		switch mode {
		case "disabled":
			bad.Enabled = false
		case "expired":
			bad.ExpireAt = time.Now().Add(-time.Minute).Unix()
		case "over-budget":
			bad.MaxCost = 1
			if err := op.StatsAPIKeyUpdate(bad.ID, model.StatsMetrics{OutputCost: 2}); err != nil {
				t.Fatal(err)
			}
		}
		if err := op.APIKeyUpdate(&bad, t.Context()); err != nil {
			t.Fatal(err)
		}
		if response := catalogTestRequest(engine, url, bad.APIKey, false); response.Code != 401 {
			t.Fatalf("%s key accepted", mode)
		}
	}
	for _, query := range []string{"", "?client_version=", "?client_version=0.169", "?client_version=0.169.0&client_version=1.2.3",
		"?client_version=01.2.3", "?client_version=1.2.3-alpha", "?client_version=%ff", "?client_version=1.2.3&bad=%xx"} {
		if response := catalogTestRequest(engine, "/codex/models"+query, a.APIKey, false); response.Code != 400 {
			t.Fatalf("bad query accepted: %s", query)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized or invalid request fetched upstream")
	}
	beforeA, beforeB := op.StatsAPIKeyGet(a.ID), op.StatsAPIKeyGet(b.ID)
	check := func(path string, credential string, want ...string) *httptest.ResponseRecorder {
		t.Helper()
		response := catalogTestRequest(engine, path, credential, false)
		got := catalogSlugs(t, response)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("visible models=%v want=%v", got, want)
		}
		return response
	}
	response := check(url+"&ignored=private-fixture", a.APIKey, mainModel)
	if !bytes.Contains(response.Body.Bytes(), []byte(`"future":{"precise":9007199254740993}`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"auto_review_model_override":null`)) {
		t.Fatal("raw metadata was changed")
	}
	check(url, b.APIKey, reviewer)
	if calls.Load() != 1 {
		t.Fatal("full public snapshot was not shared across keys")
	}
	a.SupportedModels = []string{reviewer}
	if err := op.APIKeyUpdate(&a, t.Context()); err != nil {
		t.Fatal(err)
	}
	response = check(url, a.APIKey, reviewer)
	if !bytes.Contains(response.Body.Bytes(), []byte(`"auto_review_model_override":"official-reviewer"`)) {
		t.Fatal("official override was changed")
	}
	if err := op.GroupDel(luna.ID, t.Context()); err != nil {
		t.Fatal(err)
	}
	check(url, a.APIKey)
	group(reviewer)
	b.SupportedModels = nil
	if err := op.APIKeyUpdate(&b, t.Context()); err != nil {
		t.Fatal(err)
	}
	check(url, b.APIKey, mainModel, reviewer, "c01-other")
	if calls.Load() != 1 {
		t.Fatal("permission/group changes did not reuse the full snapshot")
	}
	a.SupportedModels = []string{mainModel}
	if err := op.APIKeyUpdate(&a, t.Context()); err != nil {
		t.Fatal(err)
	}
	response = check("/codex/models?client_version=99.1.2", a.APIKey, mainModel)
	if !bytes.Contains(response.Body.Bytes(), []byte(`"different_version":true`)) {
		t.Fatal("other version did not have independent content")
	}
	missing := catalogTestRequest(engine, "/codex/models?client_version=0.0.0", a.APIKey, false)
	if missing.Code != 404 || strings.Contains(missing.Body.String(), "private-fixture") {
		t.Fatal("version failure was hidden or leaked")
	}
	// Revoke a valid key while its uncached request is in flight.
	pending := make(chan *httptest.ResponseRecorder, 1)
	go func() { pending <- catalogTestRequest(engine, "/codex/models?client_version=0.170.0", a.APIKey, false) }()
	<-started
	a.Enabled = false
	if err := op.APIKeyUpdate(&a, t.Context()); err != nil {
		t.Fatal(err)
	}
	close(release)
	if response := <-pending; response.Code != 401 {
		t.Fatal("revocation during fetch was not rechecked")
	}
	a.Enabled = true
	if err := op.APIKeyUpdate(&a, t.Context()); err != nil {
		t.Fatal(err)
	}
	b.SupportedModels = []string{reviewer}
	if err := op.APIKeyUpdate(&b, t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key       string
		anthropic bool
		want      string
	}{{a.APIKey, false, mainModel}, {b.APIKey, true, reviewer}} {
		result := catalogTestRequest(engine, "/v1/models", test.key, test.anthropic)
		var envelope struct {
			Object  string
			Data    []struct{ ID string }
			HasMore bool
			FirstID string `json:"first_id"`
		}
		if result.Code != 200 || json.Unmarshal(result.Body.Bytes(), &envelope) != nil || len(envelope.Data) != 1 || envelope.Data[0].ID != test.want {
			t.Fatal("existing model list permissions changed")
		}
		if test.anthropic {
			if envelope.FirstID != test.want || envelope.HasMore {
				t.Fatal("Anthropic list contract changed")
			}
		} else if envelope.Object != "list" {
			t.Fatal("OpenAI list contract changed")
		}
	}
	if beforeA != op.StatsAPIKeyGet(a.ID) || beforeB != op.StatsAPIKeyGet(b.ID) {
		t.Fatal("catalog requests changed inference accounting")
	}
	// Default-disabled and missing-proxy behavior also use the real registered route.
	configBefore := conf.AppConfig
	proxyBefore, _ := op.SettingGetString(model.SettingKeyProxyURL)
	t.Cleanup(func() {
		conf.AppConfig = configBefore
		_ = op.SettingSetString(model.SettingKeyProxyURL, proxyBefore)
		CloseCodexCatalog()
	})
	conf.AppConfig.CodexCatalog = conf.CodexCatalog{}
	InitCodexCatalog()
	if response := catalogTestRequest(engine, url, a.APIKey, false); response.Code != 503 {
		t.Fatal("disabled catalog served")
	}
	if response := catalogTestRequest(engine, "/v1/models", a.APIKey, false); response.Code != 200 {
		t.Fatal("disabled catalog affected model list")
	}
	conf.AppConfig.CodexCatalog = conf.CodexCatalog{Enabled: true, UseProxy: true}
	for _, proxyValue := range []string{"", "http://", "invalid://fixture"} {
		if err := op.SettingSetString(model.SettingKeyProxyURL, proxyValue); err != nil {
			// The application's own setting validation may reject an invalid value.
			continue
		}
		InitCodexCatalog()
		if response := catalogTestRequest(engine, url, a.APIKey, false); response.Code != 503 {
			t.Fatal("invalid selected proxy fell back to direct")
		}
	}
}
