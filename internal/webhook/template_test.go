package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

func TestNormalizeAndValidateAppliesDefaultsAndRendersJSON(t *testing.T) {
	endpoint := model.WebhookEndpoint{
		Name: " alert ", URL: "https://example.com/hook?token=secret",
	}
	if err := NormalizeAndValidate(&endpoint); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if endpoint.Name != "alert" || endpoint.Method != "POST" || endpoint.ContentType != "application/json" || endpoint.TimeoutSeconds != 10 {
		t.Fatalf("endpoint defaults not applied: %+v", endpoint)
	}
	if len(endpoint.Headers) != 0 {
		t.Fatalf("empty headers = %#v, want an empty map", endpoint.Headers)
	}
	body, err := Render(endpoint.BodyTemplate, Event{Event: "failure", Timestamp: "2026-01-01T00:00:00Z", GroupName: `model "one"`})
	if err != nil {
		t.Fatalf("render default template: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("default template produced invalid JSON: %s (%v)", body, err)
	}
	if decoded["group_name"] != `model "one"` {
		t.Fatalf("rendered group_name = %#v", decoded["group_name"])
	}
}

func TestNormalizeAndValidateRejectsInvalidTemplatesAndHeaders(t *testing.T) {
	base := func(body, contentType string) model.WebhookEndpoint {
		return model.WebhookEndpoint{
			Name: "test", URL: "https://example.com/hook", Method: "POST",
			ContentType: contentType, TimeoutSeconds: 10, BodyTemplate: body,
		}
	}
	for _, test := range []struct {
		name     string
		endpoint model.WebhookEndpoint
	}{
		{name: "template syntax", endpoint: base(`{{ if }}`, "text/plain")},
		{name: "invalid JSON", endpoint: base(`{"event":}`, "application/json")},
		{name: "header injection", endpoint: func() model.WebhookEndpoint {
			endpoint := base("ok", "text/plain")
			endpoint.Headers = map[string]string{"X-Notify": "safe\r\nInjected: yes"}
			return endpoint
		}()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := NormalizeAndValidate(&test.endpoint); err == nil {
				t.Fatal("NormalizeAndValidate() succeeded, want validation error")
			}
		})
	}
}

func TestMaskEndpointAndPreserveMaskedSecrets(t *testing.T) {
	originalURL := "https://service.example/hook?room=ops&token=private&signature=abcdef"
	original := model.WebhookEndpoint{
		URL: originalURL,
		Headers: map[string]string{
			"Authorization":  "Bearer private",
			"X-Notify-Token": "secret-token",
			"X-Region":       "west",
		},
	}
	masked := MaskEndpoint(original)
	for _, secret := range []string{"private", "abcdef", "secret-token"} {
		if strings.Contains(masked.URL, secret) || strings.Contains(strings.Join(mapValues(masked.Headers), " "), secret) {
			t.Fatalf("masked endpoint leaked %q: %+v", secret, masked)
		}
	}
	incoming := masked
	incoming.Name = "updated"
	incoming.Headers = cloneHeaders(masked.Headers)
	incoming.Headers["X-Region"] = "east"
	PreserveMaskedSecrets(&incoming, original)
	if incoming.URL != originalURL {
		t.Fatalf("unchanged masked URL was not restored exactly: %q", incoming.URL)
	}
	if incoming.Headers["Authorization"] != original.Headers["Authorization"] || incoming.Headers["X-Notify-Token"] != original.Headers["X-Notify-Token"] {
		t.Fatalf("masked header values were not restored: %#v", incoming.Headers)
	}
	if incoming.Headers["X-Region"] != "east" {
		t.Fatalf("non-sensitive header edit was lost: %#v", incoming.Headers)
	}
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
