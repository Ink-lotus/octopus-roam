package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
)

func TestChannelCustomHeaderModelOnlyHTTPPersistence(t *testing.T) {
	engine := gin.New()
	engine.POST("/create", createChannel)
	engine.POST("/update", updateChannel)
	headers := []model.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: "fixture-discovery/1"}}
	response := serveCooldownJSON(t, engine, http.MethodPost, "/create", map[string]any{
		"name": t.Name(), "base_url": "https://example.invalid", "custom_header": headers,
	})
	read := func(t *testing.T, response *httptest.ResponseRecorder, want bool) model.ChannelDetail {
		t.Helper()
		if response.Code != http.StatusOK {
			t.Fatalf("channel save failed: %d %s", response.Code, response.Body.String())
		}
		var envelope struct{ Data model.ChannelDetail }
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.CustomHeaderModelOnly != want || !reflect.DeepEqual(envelope.Data.CustomHeader, headers) {
			t.Fatal("save response changed the switch or header values")
		}
		var stored model.Channel
		if err := db.GetDB().First(&stored, envelope.Data.ID).Error; err != nil || stored.CustomHeaderModelOnly != want {
			t.Fatalf("switch was not persisted: %v", err)
		}
		return envelope.Data
	}
	detail := read(t, response, false)
	t.Cleanup(func() { _ = op.ChannelDel(detail.ID, context.Background()) })
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name  string
		value any
		want  bool
	}{{"enable", true, true}, {"old-caller-omits-field", nil, true}, {"disable", false, false}} {
		t.Run(step.name, func(t *testing.T) {
			if step.value == nil {
				delete(payload, "custom_header_model_only")
				payload["name"] = t.Name()
			} else {
				payload["custom_header_model_only"] = step.value
			}
			read(t, serveCooldownJSON(t, engine, http.MethodPost, "/update", payload), step.want)
			if err := op.InitCache(); err != nil {
				t.Fatal(err)
			}
			cached, err := op.ChannelDetailGet(detail.ID)
			if err != nil || cached.CustomHeaderModelOnly != step.want {
				t.Fatalf("reloaded channel lost switch: %v", err)
			}
		})
	}
	payload["custom_header_model_only"] = "true"
	if response := serveCooldownJSON(t, engine, http.MethodPost, "/update", payload); response.Code != http.StatusBadRequest {
		t.Fatal("non-boolean switch value was accepted")
	}
}

func TestChannelCustomHeaderModelOnlyFetchAndRefresh(t *testing.T) {
	previous, err := op.SettingGetString(model.SettingKeyChannelAutoUpdateEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, previous) })
	for _, modelOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("model-only-%t", modelOnly), func(t *testing.T) {
			var openAICalls, anthropicCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Error("unexpected model discovery request")
				}
				if r.Header.Get("User-Agent") != "fixture-discovery/1" || r.Header.Get("X-Client-Type") != "fixture-client" {
					t.Error("model discovery did not receive all custom headers")
				}
				w.Header().Set("Content-Type", "application/json")
				if key := r.Header.Get("X-Api-Key"); key != "" {
					anthropicCalls.Add(1)
					if key != "fixture-key-a" && key != "fixture-key-b" {
						t.Error("Anthropic discovery used the wrong credential")
					}
					if r.URL.Query().Get("after_id") == "" {
						_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-a"}],"has_more":true,"last_id":"fixture-model-a"}`)
					} else {
						_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-b"}],"has_more":false}`)
					}
					return
				}
				openAICalls.Add(1)
				if key := r.Header.Get("Authorization"); key != "Bearer fixture-key-a" && key != "Bearer fixture-key-b" {
					t.Error("OpenAI discovery used the wrong credential")
				}
				_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model-a"},{"id":"fixture-model-b"}]}`)
			}))
			defer upstream.Close()
			config := model.ChannelConfig{
				Name: t.Name(), Enabled: true, BaseURL: upstream.URL, CustomHeaderModelOnly: modelOnly,
				OpenAIChatCompletionPath: "/v1/chat/completions", OpenAIResponsePath: "/v1/responses", AnthropicMessagePath: "/v1/messages",
				CustomHeader: []model.CustomHeader{
					{HeaderKey: "User-Agent", HeaderValue: "fixture-discovery/1"},
					{HeaderKey: "X-Client-Type", HeaderValue: "fixture-client"},
				},
			}
			engine := gin.New()
			engine.POST("/fetch", fetchModel)
			engine.POST("/refresh/:id", refreshChannelModels)
			response := serveCooldownJSON(t, engine, http.MethodPost, "/fetch", model.ChannelFetchModelRequest{Channel: config, Key: "fixture-key-a"})
			if response.Code != http.StatusOK {
				t.Fatalf("draft model fetch failed: %d %s", response.Code, response.Body.String())
			}
			created, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: config, Keys: []model.ChannelKeyConfig{
				{Name: "a", Key: "fixture-key-a", Enabled: true}, {Name: "b", Key: "fixture-key-b", Enabled: true},
			}}, t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = op.ChannelDel(created.ID, context.Background()) })
			response = serveCooldownJSON(t, engine, http.MethodPost, fmt.Sprintf("/refresh/%d", created.ID), map[string]any{"enabled_only": true})
			if response.Code != http.StatusOK {
				t.Fatalf("bulk refresh entry failed: %d %s", response.Code, response.Body.String())
			}
			var refreshed struct {
				Data model.ChannelModelRefreshResult
			}
			if err := json.Unmarshal(response.Body.Bytes(), &refreshed); err != nil || !refreshed.Data.Success {
				t.Fatalf("bulk refresh did not succeed: %v", err)
			}
			result, err := op.RefreshChannelModels(t.Context(), created.ID, model.ChannelModelRefreshOptions{Automatic: true})
			if err != nil || !result.Success {
				t.Fatalf("automatic refresh failed: %v", err)
			}
			if openAICalls.Load() != 5 || anthropicCalls.Load() != 10 {
				t.Fatalf("not all keys and pages were tested: openai=%d anthropic=%d", openAICalls.Load(), anthropicCalls.Load())
			}
		})
	}
}
