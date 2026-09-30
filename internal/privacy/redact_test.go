package privacy

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestFingerprintStableAcrossOutputs(t *testing.T) {
	secret := "fixture-key-unique"
	r := New(secret)
	mask := Mask(secret)
	if mask == Mask("different-fixture") || strings.Contains(mask, secret) {
		t.Fatal("fingerprints must distinguish credentials without exposing them")
	}
	for _, text := range []string{secret, "api_key=" + secret, `{"api_key":"` + secret + `"}`, "Bearer " + secret} {
		got := r.Text(text)
		if strings.Contains(got, secret) || !strings.Contains(got, mask) {
			t.Fatalf("inconsistent fingerprint: %s", got)
		}
		if again := r.Text(got); again != got {
			t.Fatalf("redaction is not idempotent: %q -> %q", got, again)
		}
	}
}

func TestNestedBodyKeepsContentAndExactNumbers(t *testing.T) {
	body := `{"messages":[{"content":"ordinary prompt"}],"metadata":{"api_key":"nested-fixture","credentials":{"password":"nested-password"}},"echo":"nested-fixture","max_tokens":4096,"integer":9007199254740993}`
	r := New()
	got := r.Body(body)
	for _, secret := range []string{"nested-fixture", "nested-password"} {
		if strings.Contains(got, secret) {
			t.Fatalf("body leaked credential: %s", got)
		}
	}
	if !strings.Contains(got, "ordinary prompt") || !strings.Contains(got, "9007199254740993") || !strings.Contains(got, `"max_tokens":4096`) {
		t.Fatalf("ordinary content changed: %s", got)
	}
	if !json.Valid([]byte(got)) || r.Body(got) != got {
		t.Fatalf("body invalid or non-idempotent: %s", got)
	}
	plain := "{\n  \"content\": \"ordinary prompt\"\n}"
	if r.Body(plain) != plain {
		t.Fatal("non-sensitive bodies should retain formatting")
	}
}

func TestURLAndKnownEncodedValues(t *testing.T) {
	raw := "https://fixture-user:fixture-password@example.test/v1?api_key=query-fixture&key=second-fixture&room=ops"
	r := New("arbitrary-custom-header", "escaped/fixture+key")
	r.AddURL(raw)
	r.AddHeaders(http.Header{"Authorization": {"Bearer inbound-fixture"}})
	text := "request to " + raw + " failed; fixture-password echoed arbitrary-custom-header inbound-fixture " + url.QueryEscape("escaped/fixture+key")
	got := r.Text(text)
	for _, secret := range []string{"fixture-user", "fixture-password", "query-fixture", "second-fixture", "arbitrary-custom-header", "inbound-fixture", url.QueryEscape("escaped/fixture+key")} {
		if strings.Contains(got, secret) {
			t.Fatalf("URL/error leaked credential: %s", got)
		}
	}
	if !strings.Contains(got, "example.test/v1") || !strings.Contains(got, "room=ops") {
		t.Fatalf("URL diagnostic context lost: %s", got)
	}
	if again := r.Text(got); again != got {
		t.Fatalf("URL redaction is not idempotent: %q -> %q", got, again)
	}
}

func TestDuplicateBodyFieldsCannotHideCredentials(t *testing.T) {
	for _, body := range []string{
		`{"api_key":"duplicate-fixture","api_key":""}`,
		`{"metadata":{"api_key":"duplicate-fixture"},"metadata":{},"echo":"duplicate-fixture"}`,
		`{"items":[{"password":"duplicate-fixture","password":null}],"echo":"duplicate-fixture"}`,
	} {
		r := New()
		got := r.Body(body)
		if strings.Contains(got, "duplicate-fixture") || !json.Valid([]byte(got)) {
			t.Fatalf("duplicate fields leaked a credential or damaged JSON: %s", got)
		}
		if r.Body(got) != got {
			t.Fatalf("duplicate field redaction is not idempotent: %s", got)
		}
	}
}

func TestUnstructuredErrorsAndSensitiveNameVariants(t *testing.T) {
	r := New("short-fixture")
	for _, name := range []string{"Authorization", "X-API-Key", "api_key", "key", "X-Notify-Token", "client_secret", "password", "Cookie"} {
		if !IsSensitiveName(name) {
			t.Fatalf("sensitive name not recognized: %s", name)
		}
		for _, input := range []string{name + "=private-fixture", name + `: "private-fixture"`, `upstream failure {"` + name + `":"private-fixture"}`} {
			got := r.Text(input)
			if strings.Contains(got, "private-fixture") {
				t.Fatalf("error leaked: %s", got)
			}
		}
	}
	metadata := `{"max_tokens":4096,"input_tokens":8,"api_key_name":"display label","api_key_id":42,"credential_name":"main","model":"alpha"}`
	if got := New().Body(metadata); got != metadata {
		t.Fatalf("structured metadata changed: %s", got)
	}
	if got := r.Text("HTTP 429: token limit exceeded for model alpha"); got != "HTTP 429: token limit exceeded for model alpha" {
		t.Fatalf("ordinary diagnostic changed: %s", got)
	}
}

func TestCredentialSuffixesAndMetadataUseTheirContext(t *testing.T) {
	for _, name := range []string{"X-Secret-Id", "session_secret_id", "password_name", "api_key_id", "api_key_name", "credential_name", "authorization_id", "X-Token-Value", "subscription_key_name", "max_tokens"} {
		t.Run(name, func(t *testing.T) {
			const secret = "fixture-context-credential"
			if !IsSensitiveName(name) {
				t.Fatal("header/query credential word was exempted")
			}
			r := New()
			r.AddHeaders(http.Header{name: {secret}})
			if got := r.Text("upstream echoed " + secret); strings.Contains(got, secret) {
				t.Fatal("header value was not captured")
			}
			r = New()
			rawURL := "https://example.test/hook?" + url.QueryEscape(name) + "=" + secret + "&room=ops"
			r.AddURL(rawURL)
			if strings.Contains(r.URL(rawURL), secret) || strings.Contains(r.Text("echo "+secret), secret) {
				t.Fatal("query credential was not masked/captured")
			}
			if got := New().Text(name + "=" + secret); strings.Contains(got, secret) {
				t.Fatal("unstructured error credential was not masked")
			}
		})
	}
	for _, name := range []string{"X-Secret-Id", "session_secret_id", "password_name", "authorization_id", "X-Token-Value", "subscription_key_name"} {
		raw, _ := json.Marshal(map[string]string{name: "fixture-body-credential", "echo": "fixture-body-credential"})
		if got := New().Body(string(raw)); strings.Contains(got, "fixture-body-credential") {
			t.Fatalf("body field or its echo leaked for %s", name)
		}
	}
	got := New("fixture-known-value").Body(`{"api_key_name":"fixture-known-value","credential_name":"display label"}`)
	if strings.Contains(got, "fixture-known-value") || !strings.Contains(got, "display label") {
		t.Fatal("metadata exception bypassed known credentials or damaged an ordinary label")
	}
}

func TestMatcherRefreshesForAdditionalAndChangedCredentials(t *testing.T) {
	r := New()
	input := "fixture-late-value xy"
	if r.Text(input) != input {
		t.Fatal("empty redactor changed ordinary text")
	}
	r.Add("fixture-late-value")
	if got := r.Text(input); strings.Contains(got, "fixture-late-value") || !strings.Contains(got, "xy") {
		t.Fatal("first credential was not applied")
	}
	r.Add("xy")
	if got := r.Text(input); strings.Contains(got, "fixture-late-value") || strings.Contains(got, "xy") {
		t.Fatal("cached matcher missed a late short credential")
	}
	revision := r.Revision()
	r.Add("fixture-late-value", "xy")
	if r.Revision() != revision {
		t.Fatal("duplicate registration invalidated diagnostic caches")
	}
	r.Add("fixture-late-value-extended")
	if got := r.Text("fixture-late-value-extended"); got != Mask("fixture-late-value-extended") || r.Text(got) != got {
		t.Fatal("new matcher lost longest-match ordering or damaged a fingerprint")
	}
	r.Add("fixture/value")
	encoded := url.QueryEscape("fixture/value")
	if r.Text(encoded) != Mask("fixture/value") {
		t.Fatal("encoded credential not matched")
	}
	r.Add(encoded)
	if r.Text(encoded) != Mask(encoded) {
		t.Fatal("changed alias mapping did not invalidate the matcher")
	}
}

func TestLaterTextFieldsRedactEarlierBodyEchoes(t *testing.T) {
	r := New()
	got := r.Body(`["fixture-text-discovery","api_key=fixture-text-discovery"]`)
	if strings.Contains(got, "fixture-text-discovery") || !json.Valid([]byte(got)) || r.Body(got) != got {
		t.Fatal("a credential discovered later in the body left an earlier echo exposed")
	}
}

func TestRepeatedShortSecretsAndEchoesAreRedacted(t *testing.T) {
	r := New("xy")
	got := r.Text("xy xy xy diagnostic words")
	if strings.Contains(got, "xy") || !strings.Contains(got, "diagnostic words") {
		t.Fatalf("short credential handling failed: %s", got)
	}
	got = r.Text(`upstream failure {"api_key":"newly-observed-fixture","echo":"newly-observed-fixture"}`)
	if strings.Contains(got, "newly-observed-fixture") {
		t.Fatalf("structured credential echo leaked: %s", got)
	}
}

func TestKnownCompoundHeaderIsRedactedBeforeParsing(t *testing.T) {
	secret := `Digest username="fixture-user", response="fixture-signature"`
	got := New(secret).Text("Authorization: " + secret)
	if strings.Contains(got, "fixture-user") || strings.Contains(got, "fixture-signature") {
		t.Fatalf("compound authorization leaked: %s", got)
	}
}

func TestConcurrentDiagnosticReadsAndCredentialCapture(t *testing.T) {
	r := New("fixture-concurrent-key")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 12 {
				r.Add("fixture-added-key")
				got := r.Body(`{"message":"fixture-concurrent-key fixture-added-key","api_key":"fixture-body-key"}`)
				for _, secret := range []string{"fixture-concurrent-key", "fixture-added-key", "fixture-body-key"} {
					if strings.Contains(got, secret) {
						t.Errorf("concurrent diagnostic leaked a credential: %s", got)
					}
				}
			}
		})
	}
	wg.Wait()
}
