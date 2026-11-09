package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

func TestChannelCooldownRulesHTTPPresenceAndValidation(t *testing.T) {
	engine := gin.New()
	engine.POST("/create", createChannel)
	engine.POST("/update", updateChannel)
	rules := []model.CooldownRule{{ErrorCode: " 429 ", Keyword: " quota ", CooldownSeconds: 30, Scope: model.CooldownScopeModel}}
	payload := model.ChannelSaveRequest{ChannelDetail: model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "w04-handler-channel", Enabled: true, BaseURL: "https://fixture.example", CooldownRules: rules}}}
	response := serveCooldownJSON(t, engine, http.MethodPost, "/create", payload)
	if response.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct{ Data model.ChannelDetail }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	detail := envelope.Data
	t.Cleanup(func() { _ = op.ChannelDel(detail.ID, t.Context()) })
	if len(detail.CooldownRules) != 1 || detail.CooldownRules[0].ErrorCode != "429" || detail.CooldownRules[0].Keyword != "quota" {
		t.Fatalf("create response rules = %+v", detail.CooldownRules)
	}

	without := map[string]any{}
	raw, _ := json.Marshal(detail)
	if err := json.Unmarshal(raw, &without); err != nil {
		t.Fatal(err)
	}
	delete(without, "cooldown_rules")
	without["name"] = "w04-handler-channel-renamed"
	response = serveCooldownJSON(t, engine, http.MethodPost, "/update", without)
	if response.Code != http.StatusOK {
		t.Fatalf("omitted update status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.CooldownRules) != 1 {
		t.Fatalf("omitted update cleared rules: %+v", envelope.Data.CooldownRules)
	}

	without["cooldown_rules"] = []model.CooldownRule{}
	response = serveCooldownJSON(t, engine, http.MethodPost, "/update", without)
	if response.Code != http.StatusOK {
		t.Fatalf("clear update status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.CooldownRules == nil || len(envelope.Data.CooldownRules) != 0 {
		t.Fatalf("explicit empty update did not clear rules: %+v", envelope.Data.CooldownRules)
	}

	for _, invalid := range []string{
		`{"cooldown_rules":null}`,
		`{"cooldown_rules":[{"error_code":"429","cooldown_seconds":1e2,"scope":"model"}]}`,
		`{"cooldown_rules":[{"error_code":"429","cooldown_seconds":30,"scope":"model","extra":true}]}`,
	} {
		response = serveCooldownRaw(t, engine, http.MethodPost, "/update", invalid)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "cooldown_rules") {
			t.Fatalf("invalid payload status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestChannelResponsesHistoryHTTPPresenceAndPersistence(t *testing.T) {
	engine := gin.New()
	engine.POST("/create", createChannel)
	engine.POST("/update", updateChannel)
	payload := model.ChannelSaveRequest{ChannelDetail: model.ChannelDetail{ChannelConfig: model.ChannelConfig{
		Name:                  "responses-history-handler-channel",
		Enabled:               true,
		BaseURL:               "https://fixture.example",
		StripResponsesHistory: true,
		CooldownRules:         []model.CooldownRule{},
	}}}
	response := serveCooldownJSON(t, engine, http.MethodPost, "/create", payload)
	if response.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var envelope struct{ Data model.ChannelDetail }
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	detail := envelope.Data
	t.Cleanup(func() { _ = op.ChannelDel(detail.ID, t.Context()) })
	if !detail.StripResponsesHistory {
		t.Fatal("create response did not preserve enabled cleanup")
	}

	without := map[string]any{}
	raw, _ := json.Marshal(detail)
	if err := json.Unmarshal(raw, &without); err != nil {
		t.Fatal(err)
	}
	delete(without, "strip_responses_history")
	without["name"] = "responses-history-handler-channel-renamed"
	response = serveCooldownJSON(t, engine, http.MethodPost, "/update", without)
	if response.Code != http.StatusOK {
		t.Fatalf("omitted update status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Data.StripResponsesHistory {
		t.Fatal("omitted update disabled cleanup")
	}

	without["strip_responses_history"] = false
	response = serveCooldownJSON(t, engine, http.MethodPost, "/update", without)
	if response.Code != http.StatusOK {
		t.Fatalf("explicit disable status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.StripResponsesHistory {
		t.Fatal("explicit false update did not disable cleanup")
	}
}

func serveCooldownJSON(t *testing.T, engine *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return serveCooldownRaw(t, engine, method, path, string(raw))
}

func serveCooldownRaw(t *testing.T, engine *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}
