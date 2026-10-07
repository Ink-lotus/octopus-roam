package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
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
