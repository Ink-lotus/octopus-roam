package webhook

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/relay"
)

func TestWebhookOriginalErrorSurvivesObservationQueueRenderAndRetry(t *testing.T) {
	const original = "password=fixture-event-value; Authorization: Bearer fixture-event-key"
	groupID := createWebhookTestGroup(t, "original-error", model.GroupModeManual)
	service := testService()
	service.observeHealth(relay.HealthSnapshot{
		GroupID: groupID, ConfigVersion: 1,
		Members: map[int]relay.MemberHealth{0: {Version: 1, FailureStreak: 100, LastError: original}},
	})
	event := assertWebhookEvent(t, service.events, "failure", groupID)
	if event.LastError != original {
		t.Fatal("observing health changed the original error")
	}
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if request.Header.Get("Authorization") != "Bearer fixture-notification-key" {
			t.Error("notification credentials changed")
		}
		mu.Lock()
		bodies = append(bodies, string(body))
		attempt := len(bodies)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()
	endpoint := model.WebhookEndpoint{
		URL: upstream.URL, Method: "POST", ContentType: "application/json",
		BodyTemplate: "{\"error\":{{json .LastError}}}", TimeoutSeconds: 2,
		Headers: map[string]string{"Authorization": "Bearer fixture-notification-key"},
	}
	result := sendWithRetry(t.Context(), endpoint, event)
	expected, err := json.Marshal(map[string]string{"error": original})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !result.Success || !reflect.DeepEqual(bodies, []string{string(expected), string(expected)}) {
		t.Fatalf("retry changed the queued event: %+v, bodies=%v", result, bodies)
	}
}

func TestWebhookDeliveryDiagnosticsKeepTheirExistingContentScope(t *testing.T) {
	const responseBody = "fixture-target-response-only"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, responseBody)
	}))
	defer upstream.Close()
	endpoint := model.WebhookEndpoint{
		URL: upstream.URL + "?token=fixture-endpoint-query", Method: "POST",
		ContentType: "application/json", BodyTemplate: "{}", TimeoutSeconds: 2,
		Headers: map[string]string{"Authorization": "Bearer fixture-delivery-key"},
	}
	result := sendWithRetry(t.Context(), endpoint, Event{LastError: "fixture-event-error"})
	if result.Success || result.Error != "endpoint returned HTTP 400" || result.StatusCode != 400 {
		t.Fatalf("delivery failure classification changed: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{responseBody, endpoint.URL, "fixture-delivery-key", "fixture-event-error"} {
		if strings.Contains(string(encoded), value) {
			t.Fatal("delivery diagnostics started collecting new content")
		}
	}
}
