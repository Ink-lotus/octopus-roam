package webhook

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/privacy"
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

func TestEndpointFingerprintsPreserveCredentialsWhenEditingOrdinaryFields(t *testing.T) {
	original := model.WebhookEndpoint{
		URL:     "https://fixture-user:fixture-pass@example.test/hook?api_key=query-fixture&key=key-fixture&room=before",
		Headers: map[string]string{"X-API-Key": "header-fixture", "X-Region": "west"},
	}
	masked := MaskEndpoint(original)
	if masked.Headers["X-API-Key"] != privacy.Mask("header-fixture") {
		t.Fatal("endpoint must use the shared credential fingerprint")
	}
	for _, secret := range []string{"fixture-user", "fixture-pass", "query-fixture", "key-fixture"} {
		if strings.Contains(masked.URL, secret) {
			t.Fatalf("endpoint URL leaked: %s", masked.URL)
		}
	}
	masked.URL = strings.Replace(masked.URL, "room=before", "room=after", 1)
	PreserveMaskedSecrets(&masked, original)
	if masked.URL != strings.Replace(original.URL, "room=before", "room=after", 1) {
		t.Fatalf("editing an ordinary URL field damaged credentials: %s", masked.URL)
	}
	if masked.Headers["X-API-Key"] != original.Headers["X-API-Key"] {
		t.Fatal("fingerprint was saved as a credential")
	}
	legacy := model.WebhookEndpoint{URL: "https://***@example.test/hook?api_key=***&key=***&room=after", Headers: map[string]string{"X-API-Key": "***"}}
	PreserveMaskedSecrets(&legacy, original)
	if legacy.URL != masked.URL || legacy.Headers["X-API-Key"] != masked.Headers["X-API-Key"] {
		t.Fatal("legacy masked edits are no longer accepted")
	}
}

func TestStaleEndpointFingerprintCannotBeSavedAsCredential(t *testing.T) {
	current := model.WebhookEndpoint{Name: "fixture", URL: "https://example.test/hook", Headers: map[string]string{"Authorization": "Bearer actual-fixture"}}
	incoming := current
	incoming.Headers = map[string]string{"Authorization": privacy.Mask("different-fixture")}
	PreserveMaskedSecrets(&incoming, current)
	if err := NormalizeAndValidate(&incoming); err == nil {
		t.Fatal("stale fingerprint would overwrite a real credential")
	}
}

func TestCredentialSuffixesRemainPrivateInWebhookViewsAndEvents(t *testing.T) {
	previous := privacy.DiagnosticsEnabled()
	privacy.SetDiagnosticsEnabled(true)
	t.Cleanup(func() { privacy.SetDiagnosticsEnabled(previous) })
	for _, name := range []string{"X-Secret-Id", "X-Token-Value", "api_key_id", "credential_name"} {
		t.Run(name, func(t *testing.T) {
			const secret = "fixture-webhook-suffix-value"
			original := model.WebhookEndpoint{
				Name: "fixture", URL: "https://example.test/hook?" + name + "=" + secret,
				Headers: map[string]string{name: secret},
			}
			masked := MaskEndpoint(original)
			if masked.Headers[name] != privacy.Mask(secret) || strings.Contains(masked.URL, secret) {
				t.Fatal("webhook view leaked a credential")
			}
			PreserveMaskedSecrets(&masked, original)
			if masked.Headers[name] != secret || masked.URL != original.URL {
				t.Fatal("editing the masked webhook damaged its actual credentials")
			}
			masked.Headers[name] = privacy.Mask("fixture-stale-value")
			if err := NormalizeAndValidate(&masked); err == nil {
				t.Fatal("stale suffix credential fingerprint was accepted")
			}
			body, err := Render(DefaultBodyTemplate, Event{LastError: name + "=" + secret})
			if err != nil || strings.Contains(string(body), secret) {
				t.Fatal("webhook last_error leaked a suffix credential", err)
			}
		})
	}
}
