package privacy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func isolatedPolicy(t *testing.T, ids ...string) *Policy {
	t.Helper()
	config := DefaultConfig()
	for id := range config.BuiltinEnabled {
		config.BuiltinEnabled[id] = false
	}
	for _, id := range ids {
		config.BuiltinEnabled[id] = true
	}
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func customPolicy(t *testing.T, rules ...Rule) *Policy {
	t.Helper()
	config := isolatedPolicy(t).Config()
	for i := range rules {
		if rules[i].ID == "" {
			rules[i].ID = NewRuleID()
		}
	}
	config.CustomRules = rules
	policy, err := Compile(config)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestEachBuiltinHasAnIndependentDiscoveryBoundary(t *testing.T) {
	const secret = "fixture-only-secret"
	tests := []struct {
		id  string
		run func(*Redactor) string
	}{
		{KnownCredentials, func(r *Redactor) string { r.Add(secret); return r.Text("echo " + secret) }},
		{SensitiveHeaders, func(r *Redactor) string {
			r.AddHeaders(http.Header{"X-API-Key": {secret}})
			return r.Text("echo " + secret)
		}},
		{URLUserinfo, func(r *Redactor) string {
			r.AddURL("https://user:" + secret + "@example.test")
			return r.Text("echo " + secret)
		}},
		{URLQuery, func(r *Redactor) string {
			r.AddURL("https://example.test/?token=" + secret)
			return r.Text("echo " + secret)
		}},
		{AuthText, func(r *Redactor) string { return r.Text("Bearer " + secret) }},
		{TextFields, func(r *Redactor) string { return r.Text("password=" + secret) }},
		{JSONFields, func(r *Redactor) string { return r.Body(`{"password":"` + secret + `","echo":"` + secret + `"}`) }},
	}
	for _, test := range tests {
		t.Run(test.id, func(t *testing.T) {
			enabled := NewWithPolicy(isolatedPolicy(t, test.id))
			if got := test.run(enabled); strings.Contains(got, secret) || !strings.Contains(got, "redacted") {
				t.Fatalf("enabled source did not redact: %s", got)
			}
			// Another rule stays active to exercise the actual disabled path,
			// rather than only the no-rules fast path.
			other := KnownCredentials
			if test.id == KnownCredentials {
				other = JSONFields
			}
			disabled := NewWithPolicy(isolatedPolicy(t, other))
			if got := test.run(disabled); !strings.Contains(got, secret) || disabled.Revision() != 0 {
				t.Fatalf("disabled source still collected/redacted: %s", got)
			}
		})
	}
}

func TestPolicyFallbacksAndAllRulesOffPreserveOriginal(t *testing.T) {
	malformedURL := "https://user:%zz@example.test/path"
	malformedQuery := "https://example.test/path?token=fixture-secret;bad"
	for _, test := range []struct{ input, id string }{{malformedURL, URLUserinfo}, {malformedQuery, URLQuery}} {
		r := NewWithPolicy(isolatedPolicy(t, test.id))
		if got := r.URL(test.input); got == test.input || !strings.Contains(got, "redacted") {
			t.Fatal("fallback was not protected", got)
		}
		for _, other := range []string{KnownCredentials, JSONFields, SensitiveHeaders} {
			r = NewWithPolicy(isolatedPolicy(t, other))
			if got := r.URL(test.input); got != test.input || r.Revision() != 0 {
				t.Fatal("disabled fallback still ran", got)
			}
		}
	}
	r := NewWithPolicy(isolatedPolicy(t))
	for _, input := range []string{`{ "password": "x", "password": "y", "value": 9007199254740993 }`, malformedURL, malformedQuery, "Bearer fixture-secret"} {
		r.Add("fixture-secret")
		r.AddURL(input)
		if r.Text(input) != input || r.Body(input) != input || r.URL(input) != input || r.Revision() != 0 {
			t.Fatal("all-off policy modified original input")
		}
	}
}

func TestDiagnosticJSONPreservesStructureAndDiscoversBeforeReplacing(t *testing.T) {
	r := NewWithPolicy(defaultPolicy)
	body := "{\n \"echo\":\"fixture-late\", \"password\":\"fixture-first\", \"password\":\"fixture-late\", \"nested\": [\"fixture-text\",\"api_key=fixture-text\"], \"n\":9007199254740993, \"num\":1.2300e+004, \"max_tokens\":42, \"ordinary\":\"\\u0061\\/b\"\n}"
	got := r.Body(body)
	for _, value := range []string{"fixture-first", "fixture-late", "fixture-text"} {
		if strings.Contains(got, value) {
			t.Fatal("late or duplicate value leaked", got)
		}
	}
	for _, value := range []string{"\n ", `"n":9007199254740993`, `"num":1.2300e+004`, `"max_tokens":42`, `"ordinary":"\u0061\/b"`} {
		if !strings.Contains(got, value) {
			t.Fatal("untouched JSON changed", got)
		}
	}
	if strings.Count(got, `"password"`) != 2 || !json.Valid([]byte(got)) || r.Body(got) != got {
		t.Fatal("JSON structure/idempotence failed", got)
	}
	metadata := `{"api_key_name":"display label","api_key_id":42,"credential_name":"main","input_tokens":8}`
	if NewWithPolicy(defaultPolicy).Body(metadata) != metadata {
		t.Fatal("metadata exception changed")
	}
}

func TestSourceAliasesAcrossHeaderURLAndBody(t *testing.T) {
	secret := `fixture/汉字+"value`
	r := NewWithPolicy(isolatedPolicy(t, SensitiveHeaders))
	r.AddHeaders(http.Header{"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte("fixture-user:"+secret))}, "Cookie": {"session=fixture-cookie"}})
	for _, value := range []string{secret, url.QueryEscape(secret), "fixture-cookie", "fixture-user"} {
		if got := r.Text("echo " + value); strings.Contains(got, value) {
			t.Fatal("derived header credential leaked", got)
		}
	}
	r = NewWithPolicy(isolatedPolicy(t, URLQuery))
	r.AddURL("https://example.test/?token=" + url.QueryEscape(secret))
	body, _ := json.Marshal(map[string]string{"echo": secret})
	if got := r.Body(string(body)); strings.Contains(got, "fixture") || !strings.Contains(got, Mask(secret)) {
		t.Fatal("URL-to-body alias changed fingerprint", got)
	}
}

func TestCustomRulesAcrossTypesAndMetadata(t *testing.T) {
	field := Rule{ID: "custom_field", Name: "field", Kind: FieldName, Pattern: "api_key_name", Enabled: true}
	for _, kind := range []string{"json", "headers", "url", "text"} {
		r := NewWithPolicy(customPolicy(t, field))
		input := map[string]string{"json": `{"echo":"custom-secret","nested":{"API_KEY_NAME":"custom-secret"},"n":9007199254740993}`, "headers": `{"API_KEY_NAME":"custom-secret","Ordinary":"okay"}`, "url": "https://example.test/?API_KEY_NAME=custom-secret&n=1", "text": `API_KEY_NAME="custom-secret"`}[kind]
		got, _ := r.execute(input, kind, true)
		if strings.Contains(got, "custom-secret") || !strings.Contains(got, "API_KEY_NAME") {
			t.Fatal("custom field did not override metadata exception", kind, got)
		}
	}
	for _, rule := range []Rule{{Name: "short", Kind: Literal, Pattern: "x", Enabled: true}, {Name: "unicode", Kind: Literal, Pattern: "汉字", Enabled: true}, {Name: "spaces", Kind: Literal, Pattern: " ! ", Enabled: true}, {Name: "regex", Kind: Regex, Pattern: `(?i)client-[0-9]+`, Enabled: true}} {
		r := NewWithPolicy(customPolicy(t, rule))
		input := "axb 汉字 ! CLIENT-42 end"
		got := r.Text(input)
		if got == input || r.Text(got) != got {
			t.Fatal("custom rule did not match or was not idempotent", got)
		}
		body := `{"content":"axb 汉字 ! CLIENT-42 end","n":1.2300e+004}`
		if got := r.Body(body); !json.Valid([]byte(got)) || !strings.Contains(got, `"n":1.2300e+004`) || r.Body(got) != got {
			t.Fatal("custom rule damaged JSON", got)
		}
	}
	r := NewWithPolicy(customPolicy(t, Rule{Name: "anchored", Kind: Regex, Pattern: "^a", Enabled: true}))
	if got := r.Text("abc a"); got != Mask("a")+"bc a" {
		t.Fatal("regex discovery became a global literal", got)
	}
}

func TestCustomOverlapCountsAndProtectedMarkers(t *testing.T) {
	policy := customPolicy(t, Rule{ID: "custom_short", Name: "short", Kind: Literal, Pattern: "ab", Enabled: true}, Rule{ID: "custom_long", Name: "long", Kind: Regex, Pattern: "abc", Enabled: true})
	r := NewWithPolicy(policy)
	got, hits := r.execute("abc abc", "text", true)
	if got != Mask("abc")+" "+Mask("abc") || len(hits) != 2 || hits[0].Count != 2 || hits[1].Count != 2 {
		t.Fatal("overlap ordering or counts changed", got, hits)
	}
	for _, pattern := range []string{"redacted", ".", "a"} {
		r := NewWithPolicy(customPolicy(t, Rule{Name: "marker", Kind: Regex, Pattern: pattern, Enabled: true}))
		if r.Text(got) != got && pattern != "." {
			t.Fatal("custom rule damaged a marker")
		}
		marker := Mask("abc")
		if r.Text(marker) != marker {
			t.Fatal("marker was matched by custom rule")
		}
	}
	r = NewWithPolicy(isolatedPolicy(t, KnownCredentials, AuthText))
	r.Add("fixture-token")
	_, hits = r.execute("Bearer fixture-token", "text", true)
	if len(hits) != 2 || hits[0].RuleID != KnownCredentials || hits[1].RuleID != AuthText || hits[0].Count != 1 || hits[1].Count != 1 {
		t.Fatal("overlapping builtin sources were not reported", hits)
	}
}

func TestPolicySnapshotsAndConcurrentCollection(t *testing.T) {
	previous := currentPolicy()
	t.Cleanup(func() { Publish(previous.enabled, previous.rules) })
	Publish(true, isolatedPolicy(t, JSONFields))
	old := NewDiagnostic()
	Publish(true, isolatedPolicy(t))
	body := `{"password":"fixture-snapshot"}`
	if strings.Contains(old.Body(body), "fixture-snapshot") || NewDiagnostic().Body(body) != body || New().Body(body) == body {
		t.Fatal("request snapshot or fixed editing policy changed")
	}
	r := NewWithPolicy(defaultPolicy)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				r.Add("fixture-known")
				r.AddHeaders(http.Header{"Cookie": {"session=fixture-cookie"}})
				got := r.Body(`{"password":"fixture-secret","echo":"fixture-known fixture-cookie fixture-secret"}`)
				if strings.Contains(got, "fixture-") {
					t.Error("concurrent discovery leaked a credential")
				}
			}
		})
	}
	wg.Wait()
}

func TestEncodedUnicodeEscapesAndMixedExistingMarkers(t *testing.T) {
	r := NewWithPolicy(customPolicy(t, Rule{Name: "unicode", Kind: Literal, Pattern: "😀汉字", Enabled: true}))
	body := `{"echo":"before \ud83d\ude00\u6c49字 after","raw":"keep\u0061"}`
	got := r.Body(body)
	if strings.Contains(got, `\ud83d`) || !strings.Contains(got, `"raw":"keep\u0061"`) || !json.Valid([]byte(got)) {
		t.Fatal("encoded Unicode mapping damaged JSON", got)
	}
	r = NewWithPolicy(isolatedPolicy(t, TextFields))
	got = r.Text(`echo private'value then password='private\'value'`)
	if strings.Contains(got, "private") || !strings.Contains(got, Mask("private'value")) {
		t.Fatal("quoted text alias was not decoded", got)
	}
	marker := Mask("existing")
	r = NewWithPolicy(customPolicy(t, Rule{ID: "custom_all", Name: "all", Kind: Regex, Pattern: ".+", Enabled: true}))
	got, hits := r.execute("before "+marker+" after", "text", true)
	if !strings.Contains(got, marker) || strings.Contains(got, "before") || strings.Contains(got, "after") || r.Text(got) != got || len(hits) != 1 || hits[0].Count != 1 {
		t.Fatal("existing markers blocked new matches or were damaged", got, hits)
	}
	invalidUTF8 := "{\"content\":\"" + string([]byte{0xff}) + " fixture-key\"}"
	r = NewWithPolicy(defaultPolicy)
	r.Add("fixture-key")
	if got := r.Body(invalidUTF8); strings.Contains(got, "fixture-key") {
		t.Fatal("invalid UTF-8 prevented credential redaction")
	}
}

func TestCustomTextMatchesAcrossURLComponents(t *testing.T) {
	for _, kind := range []string{Literal, Regex} {
		r := NewWithPolicy(customPolicy(t, Rule{Name: "URL span", Kind: kind, Pattern: `example.test/private`, Enabled: true}))
		input := "request https://example.test/private/path failed"
		if got := r.Text(input); strings.Contains(got, "example.test/private") {
			t.Fatal("custom match was split by URL parsing", got)
		}
		if got := r.URL("https://example.test/private/path"); strings.Contains(got, "example.test/private") {
			t.Fatal("URL preview ignored an entire literal/regex span", got)
		}
	}
	r := NewWithPolicy(customPolicy(t, Rule{Name: "anchored", Kind: Regex, Pattern: `^ secret`, Enabled: true}))
	input := "visit https://example.test/path secret"
	if got := r.Text(input); got != input {
		t.Fatal("URL splitting introduced a new regex anchor", got)
	}
}

func TestURLUserinfoAndEncodedMarkersRemainIdempotent(t *testing.T) {
	policy := isolatedPolicy(t, URLUserinfo, URLQuery)
	r := NewWithPolicy(policy)
	raw := "https://fixture-user:fixture-password@example.test/path?token=fixture-query"
	got := r.URL(raw)
	parsed, err := url.Parse(got)
	if err != nil || parsed.User == nil || parsed.User.Username() != Mask("fixture-user") {
		t.Fatal("username marker changed URL structure", got, err)
	}
	password, _ := parsed.User.Password()
	if password != Mask("fixture-password") || r.URL(got) != got || NewWithPolicy(policy).URL(got) != got {
		t.Fatal("userinfo redaction is not idempotent", got)
	}
	for _, rule := range []Rule{
		{Name: "short", Kind: Literal, Pattern: "a", Enabled: true},
		{Name: "all", Kind: Regex, Pattern: ".+", Enabled: true},
		{Name: "hex", Kind: Regex, Pattern: `[a-f0-9]+`, Enabled: true},
	} {
		custom := customPolicy(t, rule)
		for _, marker := range []string{Mask("prior"), url.QueryEscape(Mask("prior")), url.QueryEscape(url.QueryEscape(Mask("prior")))} {
			redactor := NewWithPolicy(custom)
			if redactor.Text(marker) != marker {
				t.Fatal("custom rule modified an encoded marker")
			}
		}
		redactor := NewWithPolicy(custom)
		first := redactor.URL(raw)
		if second := redactor.URL(first); second != first {
			t.Fatal("custom URL output changed on another pass", first, second)
		}
		body, _ := json.Marshal(map[string]string{"url": raw})
		first = redactor.Body(string(body))
		if second := redactor.Body(first); second != first {
			t.Fatal("custom JSON URL output changed on another pass", first, second)
		}
	}
}

func TestSensitiveContainerWithExistingMarkerStillRedactsFreshValues(t *testing.T) {
	marker := Mask("prior")
	body := `{"credentials":{"note":"fresh-secret","old":"` + marker + `","number":123}}`
	r := NewWithPolicy(isolatedPolicy(t, JSONFields))
	got := r.Body(body)
	if strings.Contains(got, "fresh-secret") || strings.Contains(got, "123") || !strings.Contains(got, marker) || !json.Valid([]byte(got)) || r.Body(got) != got {
		t.Fatal("sensitive container exposed fresh values or changed an existing marker", got)
	}
}
