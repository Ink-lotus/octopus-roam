package privacy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigStrictValidationAndMissingBuiltins(t *testing.T) {
	config := DefaultConfig()
	config.BuiltinEnabled = map[string]bool{JSONFields: false}
	raw, _ := json.Marshal(config)
	parsed, err := ParseConfig(string(raw))
	if err != nil || parsed.BuiltinEnabled[JSONFields] || !parsed.BuiltinEnabled[AuthText] {
		t.Fatalf("missing defaults did not preserve disabled rules: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(string(raw), `"version":1`, `"version":2`, 1),
		strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(raw), `"version":1`, `"version":1,"definition":{}`, 1),
		strings.Replace(string(raw), `false`, `null`, 1),
		strings.Replace(string(raw), `false`, `"false"`, 1),
		strings.Replace(string(raw), JSONFields, "builtin.future_rule", 1),
		strings.Replace(string(raw), `"custom_rules":[]`, `"custom_rules":null`, 1),
	} {
		if _, err := ParseConfig(bad); err == nil {
			t.Fatal("accepted malformed rule configuration")
		}
	}
	policy, err := Compile(parsed)
	if err != nil {
		t.Fatal(err)
	}
	parsed.BuiltinEnabled[AuthText] = false
	copy := policy.Config()
	copy.BuiltinEnabled[AuthText] = false
	if !policy.Config().BuiltinEnabled[AuthText] {
		t.Fatal("published policy can be mutated by its caller")
	}
}

func TestRuleValidationPreservesLiteralsAndRejectsNullableRegex(t *testing.T) {
	for _, pattern := range []string{"(", "a*", "a?", "a|", "^$", `\b`, "(?:a{0,3})", "(a?)+", "(?:^|abc)", "(?=x)"} {
		if err := ValidateRule(Rule{Name: "expression", Kind: Regex, Pattern: pattern}); err == nil {
			t.Fatalf("accepted invalid/nullable regex %q", pattern)
		}
	}
	for _, rule := range []Rule{
		{Name: "名称", Kind: Literal, Pattern: " ! ", Enabled: true},
		{Name: "expression", Kind: Regex, Pattern: `(?i)\baccount-[0-9]+\b`},
		{Name: "field", Kind: FieldName, Pattern: "客户编号"},
	} {
		if err := ValidateRule(rule); err != nil {
			t.Fatal(err)
		}
	}
	for _, rule := range []Rule{
		{Name: strings.Repeat("界", 81), Kind: Literal, Pattern: "x"},
		{Name: "literal", Kind: Literal, Pattern: strings.Repeat("界", 342)},
		{Name: "field", Kind: FieldName, Pattern: "api_key = value"},
		{Name: "unknown", Kind: "javascript", Pattern: "x"},
	} {
		if err := ValidateRule(rule); err == nil {
			t.Fatal("accepted invalid rule")
		}
	}
	config := DefaultConfig()
	for range MaxCustomRules + 1 {
		config.CustomRules = append(config.CustomRules, Rule{ID: NewRuleID(), Name: "literal", Kind: Literal, Pattern: "x"})
	}
	if _, err := Compile(config); err == nil {
		t.Fatal("accepted too many rules")
	}
	config.CustomRules = config.CustomRules[:2]
	config.CustomRules[1].ID = config.CustomRules[0].ID
	if _, err := Compile(config); err == nil {
		t.Fatal("accepted duplicate IDs")
	}
}
