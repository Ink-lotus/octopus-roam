package privacy

import (
	"net/http"
	"strings"
	"testing"
)

func TestDiagnosticProtectionOptInAndRequestSnapshot(t *testing.T) {
	previous := DiagnosticsEnabled()
	t.Cleanup(func() { SetDiagnosticsEnabled(previous) })
	if previous {
		t.Fatal("diagnostic privacy must start disabled")
	}
	const secret = "fixture-diagnostic-secret"
	const body = "{ \"password\": \"" + secret + "\", \"echo\": \"" + secret + "\" }"
	const rawURL = "https://example.test/?token=" + secret + "&a=1"
	disabled := FromContext(t.Context())
	disabled.Add(secret)
	disabled.AddHeaders(http.Header{"Authorization": {"Bearer " + secret}})
	disabled.AddURL(rawURL)
	if disabled.Body(body) != body || disabled.Text("Bearer "+secret) != "Bearer "+secret || disabled.URL(rawURL) != rawURL || disabled.Revision() != 0 {
		t.Fatal("disabled privacy changed diagnostics or captured credentials")
	}

	SetDiagnosticsEnabled(true)
	enabled := FromContext(t.Context())
	for _, got := range []string{enabled.Body(body), enabled.Text("Bearer " + secret), enabled.URL(rawURL)} {
		if strings.Contains(got, secret) || !strings.Contains(got, "redacted") {
			t.Fatal("enabled privacy did not redact a credential")
		}
	}
	if disabled.Body(body) != body || FromContext(WithRedactor(t.Context(), disabled)) != disabled {
		t.Fatal("enabling privacy changed an existing request")
	}

	SetDiagnosticsEnabled(false)
	if strings.Contains(enabled.Body(body), secret) || NewDiagnostic().Body(body) != body {
		t.Fatal("disabling privacy did not preserve the per-request setting")
	}
	if strings.Contains(New().Body(body), secret) || strings.Contains(Mask(secret), secret) {
		t.Fatal("optional diagnostic privacy disabled mandatory credential masking")
	}
}
