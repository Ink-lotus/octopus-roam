package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

func TestWebhookEventRetainsPolicyThroughQueueRenderAndRetry(t *testing.T) {
	previous, _ := op.SettingGetString(model.SettingKeyPrivacyProtectionEnabled)
	t.Cleanup(func() { _ = op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, previous) })
	if err := op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	event := prepareEvent(model.Group{}, Event{Event: "failure", LastError: "password=fixture-event-secret"})
	if err := op.SettingSetString(model.SettingKeyPrivacyProtectionEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		attempt := len(bodies)
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture-upstream-secret" {
			t.Error("notification credentials changed")
		}
		privacy.SetDiagnosticsEnabled(attempt%2 == 0)
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer upstream.Close()
	endpoint := model.WebhookEndpoint{URL: upstream.URL, Method: "POST", ContentType: "application/json", BodyTemplate: `{"error":{{json .LastError}}}`, Headers: map[string]string{"Authorization": "Bearer fixture-upstream-secret"}, TimeoutSeconds: 1}
	result := sendWithRetry(context.Background(), endpoint, event)
	mu.Lock()
	defer mu.Unlock()
	if !result.Success || len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[0], "fixture-event-secret") {
		t.Fatal("retry changed the event policy", result, bodies)
	}
	privacy.SetDiagnosticsEnabled(true)
	newEvent := prepareEvent(model.Group{}, Event{LastError: event.LastError})
	if strings.Contains(newEvent.LastError, "fixture-event-secret") {
		t.Fatal("new event did not use current policy")
	}
}
