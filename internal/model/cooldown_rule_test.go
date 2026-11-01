package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateCooldownRulesJSONNormalizesAndPreservesOrder(t *testing.T) {
	rules, err := ValidateCooldownRulesJSON([]byte(`[
        {"error_code":" 429 ","keyword":" quota ","cooldown_seconds":1,"scope":"model"},
        {"keyword":"Overloaded","cooldown_seconds":2147483647,"scope":"channel"}
    ]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0].ErrorCode != "429" || rules[0].Keyword != "quota" || rules[1].Keyword != "Overloaded" || rules[1].Scope != CooldownScopeChannel {
		t.Fatalf("unexpected normalized rules: %+v", rules)
	}
	rules[0].Keyword = "changed"
	if rules[1].Keyword != "Overloaded" {
		t.Fatal("rule order or values were aliased")
	}
}

func TestValidateCooldownRulesJSONRejectsInvalidShapeAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"empty condition", `[{"error_code":" ","keyword":" ","cooldown_seconds":30,"scope":"model"}]`, "cooldown_rules[0]"},
		{"zero", `[{"error_code":"429","cooldown_seconds":0,"scope":"model"}]`, "cooldown_rules[0].cooldown_seconds"},
		{"negative", `[{"error_code":"429","cooldown_seconds":-1,"scope":"model"}]`, "cooldown_seconds"},
		{"decimal", `[{"error_code":"429","cooldown_seconds":1.5,"scope":"model"}]`, "cooldown_seconds"},
		{"exponent", `[{"error_code":"429","cooldown_seconds":1e2,"scope":"model"}]`, "cooldown_seconds"},
		{"overflow", `[{"error_code":"429","cooldown_seconds":2147483648,"scope":"model"}]`, "cooldown_seconds"},
		{"bad scope", `[{"error_code":"429","cooldown_seconds":30,"scope":"group"}]`, "scope"},
		{"null scope", `[{"error_code":"429","cooldown_seconds":30,"scope":null}]`, "scope"},
		{"unknown field", `[{"error_code":"429","cooldown_seconds":30,"scope":"model","extra":true}]`, "unknown field"},
		{"null rule", `[null]`, "cooldown_rules[0]"},
		{"null array", `null`, "cooldown_rules must be an array"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateCooldownRulesJSON([]byte(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), ErrInvalidCooldownRule.Error()) {
				t.Fatalf("error does not preserve sentinel: %v", err)
			}
		})
	}
}

func TestCooldownRuleJSONRoundTripUsesFourFields(t *testing.T) {
	rules := []CooldownRule{{ErrorCode: "429", CooldownSeconds: 30, Scope: CooldownScopeModel}}
	raw, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "extra") || !strings.Contains(string(raw), `"keyword":""`) {
		t.Fatalf("unexpected rule JSON: %s", raw)
	}
	var decoded []CooldownRule
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0] != rules[0] {
		t.Fatalf("round trip changed rule: %+v", decoded)
	}
}
