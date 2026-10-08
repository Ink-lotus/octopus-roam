package privacy

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

const (
	KnownCredentials = "builtin.known_credentials"
	SensitiveHeaders = "builtin.sensitive_headers"
	URLUserinfo      = "builtin.url_userinfo"
	URLQuery         = "builtin.url_query"
	AuthText         = "builtin.auth_text"
	TextFields       = "builtin.text_fields"
	JSONFields       = "builtin.json_fields"

	FieldName = "field_name"
	Literal   = "literal"
	Regex     = "regex"

	MaxCustomRules  = 64
	MaxNameRunes    = 80
	MaxPatternBytes = 1024
	MaxConfigBytes  = 128 << 10
	MaxPreviewBytes = 32 << 10
	MaxKnownValues  = 16
	MaxRequestBytes = 192 << 10
)

// Builtin metadata contains only fixed, public examples. Display text is localized
// by Key; definitions can never be supplied by a settings client.
type Builtin struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	InputType   string   `json:"input_type"`
	Example     string   `json:"example"`
	KnownValues []string `json:"known_values,omitempty"`
}

var builtinCatalog = []Builtin{
	{KnownCredentials, "known_credentials", "text", "upstream echoed demo-credential", []string{"demo-credential"}},
	{SensitiveHeaders, "sensitive_headers", "headers", `{"Authorization":"Bearer demo-token","Accept":"application/json"}`, nil},
	{URLUserinfo, "url_userinfo", "url", "https://demo-user:demo-password@example.test/v1", nil},
	{URLQuery, "url_query", "url", "https://example.test/v1?token=demo-token&model=demo", nil},
	{AuthText, "auth_text", "text", "request failed: Bearer demo-token", nil},
	{TextFields, "text_fields", "text", `password="demo-password" status=failed`, nil},
	{JSONFields, "json_fields", "json", `{"password":"demo-password","max_tokens":4096}`, nil},
}

func Builtins() []Builtin {
	result := append([]Builtin(nil), builtinCatalog...)
	for i := range result {
		result[i].KnownValues = append([]string(nil), result[i].KnownValues...)
	}
	return result
}

type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern"`
	Enabled bool   `json:"enabled"`
}

// Keep missing/null booleans distinct from an explicitly saved false value.
func (r *Rule) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Pattern string `json:"pattern"`
		Enabled *bool  `json:"enabled"`
	}
	if err := DecodeStrict(data, &wire); err != nil {
		return err
	}
	if wire.Enabled == nil {
		return Invalid("enabled", "required", "enabled must be a boolean")
	}
	*r = Rule{wire.ID, wire.Name, wire.Kind, wire.Pattern, *wire.Enabled}
	return nil
}

type Config struct {
	Version        int             `json:"version"`
	Revision       string          `json:"revision"`
	BuiltinEnabled map[string]bool `json:"builtin_enabled"`
	CustomRules    []Rule          `json:"custom_rules"`
}

type Limits struct {
	CustomRules  int `json:"custom_rules"`
	NameRunes    int `json:"name_runes"`
	PatternBytes int `json:"pattern_bytes"`
	ConfigBytes  int `json:"config_bytes"`
	PreviewBytes int `json:"preview_bytes"`
	KnownValues  int `json:"known_values"`
	RequestBytes int `json:"request_bytes"`
}

func RuleLimits() Limits {
	return Limits{MaxCustomRules, MaxNameRunes, MaxPatternBytes, MaxConfigBytes, MaxPreviewBytes, MaxKnownValues, MaxRequestBytes}
}

type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func Invalid(field, code, message string) error {
	return &ValidationError{field, code, message}
}

func NewRevision() string { return rand.Text() }
func NewRuleID() string   { return "custom_" + rand.Text() }

func DefaultConfig() Config {
	enabled := make(map[string]bool, len(builtinCatalog))
	for _, rule := range builtinCatalog {
		enabled[rule.ID] = true
	}
	return Config{1, NewRevision(), enabled, []Rule{}}
}

func DefaultConfigJSON() string {
	value, _ := json.Marshal(DefaultConfig())
	return string(value)
}

// DecodeStrict rejects duplicate properties, unknown fields and trailing JSON.
// Error messages intentionally exclude submitted values, including regex/sample text.
func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return fmt.Errorf("duplicate property")
				}
				seen[name] = true
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		case json.Delim('['):
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
		}
		return err
	}
	// json.Valid also imposes the standard nesting limit before walking tokens.
	if !json.Valid(data) || walk() != nil {
		return Invalid("request", "json", "invalid JSON or duplicate properties")
	}
	decoder = json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if validation, ok := err.(*ValidationError); ok {
			return validation
		}
		return Invalid("request", "fields", "unknown fields or invalid field types")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Invalid("request", "json", "expected one JSON value")
	}
	return nil
}

func ParseConfig(raw string) (Config, error) {
	if len(raw) > MaxConfigBytes {
		return Config{}, Invalid("config", "limit", "rule configuration exceeds 128 KiB")
	}
	var wire struct {
		Version        int              `json:"version"`
		Revision       string           `json:"revision"`
		BuiltinEnabled map[string]*bool `json:"builtin_enabled"`
		CustomRules    *[]Rule          `json:"custom_rules"`
	}
	if err := DecodeStrict([]byte(raw), &wire); err != nil {
		return Config{}, err
	}
	if wire.BuiltinEnabled == nil || wire.CustomRules == nil {
		return Config{}, Invalid("config", "required", "builtin_enabled and custom_rules are required")
	}
	config := Config{wire.Version, wire.Revision, map[string]bool{}, *wire.CustomRules}
	for id, enabled := range wire.BuiltinEnabled {
		if enabled == nil {
			return Config{}, Invalid("builtin_enabled", "boolean", "builtin states must be booleans")
		}
		config.BuiltinEnabled[id] = *enabled
	}
	if err := normalizeConfig(&config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func ValidateRule(rule Rule) error {
	if strings.TrimSpace(rule.Name) == "" || !utf8.ValidString(rule.Name) || utf8.RuneCountInString(rule.Name) > MaxNameRunes {
		return Invalid("name", "length", "name must contain 1 to 80 Unicode characters")
	}
	if rule.Pattern == "" || len(rule.Pattern) > MaxPatternBytes || !utf8.ValidString(rule.Pattern) {
		return Invalid("pattern", "length", "pattern must contain 1 to 1024 UTF-8 bytes")
	}
	switch rule.Kind {
	case FieldName:
		for _, char := range rule.Pattern {
			if unicode.IsSpace(char) || unicode.IsControl(char) || strings.ContainsRune(`"'=:,;{}[]&?`, char) {
				return Invalid("pattern", "field_name", "use a complete field name without whitespace or assignment syntax")
			}
		}
	case Literal:
	case Regex:
		expression, err := syntax.Parse(rule.Pattern, syntax.Perl)
		if err != nil {
			return Invalid("pattern", "regex", "invalid Go regular expression")
		}
		if nullable(expression) {
			return Invalid("pattern", "empty_match", "regular expression must not match empty text")
		}
		if _, err := regexp.Compile(rule.Pattern); err != nil {
			return Invalid("pattern", "regex", "invalid Go regular expression")
		}
	default:
		return Invalid("kind", "unknown", "unsupported rule kind")
	}
	return nil
}

func nullable(expression *syntax.Regexp) bool {
	switch expression.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpStar, syntax.OpQuest:
		return true
	case syntax.OpLiteral:
		return len(expression.Rune) == 0
	case syntax.OpCapture, syntax.OpPlus:
		return nullable(expression.Sub[0])
	case syntax.OpRepeat:
		return expression.Min == 0 || nullable(expression.Sub[0])
	case syntax.OpAlternate:
		for _, child := range expression.Sub {
			if nullable(child) {
				return true
			}
		}
	case syntax.OpConcat:
		for _, child := range expression.Sub {
			if !nullable(child) {
				return false
			}
		}
		return true
	}
	return false
}

func normalizeConfig(config *Config) error {
	if config.Version != 1 {
		return Invalid("version", "unsupported", "unsupported privacy rule configuration version")
	}
	if config.Revision == "" || len(config.Revision) > 128 {
		return Invalid("revision", "required", "a valid revision is required")
	}
	if len(config.CustomRules) > MaxCustomRules {
		return Invalid("custom_rules", "limit", "at most 64 custom rules are allowed")
	}
	known := map[string]bool{}
	for _, rule := range builtinCatalog {
		known[rule.ID] = true
	}
	for id := range config.BuiltinEnabled {
		if !known[id] {
			return Invalid("builtin_enabled", "unknown", "unknown builtin rule ID: "+id)
		}
	}
	if config.BuiltinEnabled == nil {
		config.BuiltinEnabled = map[string]bool{}
	}
	for id := range known {
		if _, exists := config.BuiltinEnabled[id]; !exists {
			config.BuiltinEnabled[id] = true
		}
	}
	seen := map[string]bool{}
	for _, rule := range config.CustomRules {
		if !regexpID.MatchString(rule.ID) || seen[rule.ID] {
			return Invalid("id", "invalid", "invalid or duplicate custom rule ID")
		}
		seen[rule.ID] = true
		if err := ValidateRule(rule); err != nil {
			return err
		}
	}
	if config.CustomRules == nil {
		config.CustomRules = []Rule{}
	}
	encoded, _ := json.Marshal(config)
	if len(encoded) > MaxConfigBytes {
		return Invalid("config", "limit", "rule configuration exceeds 128 KiB")
	}
	return nil
}

var regexpID = regexp.MustCompile(`^custom_[A-Za-z0-9_-]{1,80}$`)

type compiledRule struct {
	Rule
	regex *regexp.Regexp
}

// Policy owns all its data and is immutable after Compile. Requests only retain
// this pointer, never the mutable configuration used by an editing client.
type Policy struct {
	config Config
	custom []compiledRule
	order  map[string]int
	active bool
}

func cloneConfig(config Config) Config {
	copy := config
	copy.BuiltinEnabled = make(map[string]bool, len(config.BuiltinEnabled))
	for id, enabled := range config.BuiltinEnabled {
		copy.BuiltinEnabled[id] = enabled
	}
	copy.CustomRules = append([]Rule{}, config.CustomRules...)
	return copy
}

func Compile(config Config) (*Policy, error) {
	config = cloneConfig(config)
	if err := normalizeConfig(&config); err != nil {
		return nil, err
	}
	policy := &Policy{config: config, order: map[string]int{}}
	for i, rule := range builtinCatalog {
		policy.order[rule.ID] = i
		policy.active = policy.active || config.BuiltinEnabled[rule.ID]
	}
	for i, rule := range config.CustomRules {
		policy.order[rule.ID] = len(builtinCatalog) + i
		if !rule.Enabled {
			continue
		}
		compiled := compiledRule{Rule: rule}
		if rule.Kind == Regex {
			compiled.regex, _ = regexp.Compile(rule.Pattern)
		}
		policy.custom = append(policy.custom, compiled)
		policy.active = true
	}
	return policy, nil
}

func (p *Policy) Config() Config { return cloneConfig(p.config) }

func (p *Policy) enabled(id string) bool {
	if enabled, builtin := p.config.BuiltinEnabled[id]; builtin {
		return enabled
	}
	for _, rule := range p.custom {
		if rule.ID == id {
			return true
		}
	}
	return false
}

type diagnosticPolicy struct {
	enabled bool
	rules   *Policy
}

var defaultPolicy, _ = Compile(DefaultConfig())
var activePolicy atomic.Pointer[diagnosticPolicy]

func currentPolicy() *diagnosticPolicy {
	if current := activePolicy.Load(); current != nil {
		return current
	}
	return &diagnosticPolicy{rules: defaultPolicy}
}

func Publish(enabled bool, rules *Policy) {
	activePolicy.Store(&diagnosticPolicy{enabled: enabled, rules: rules})
}

func SetDiagnosticsEnabled(enabled bool) {
	for {
		previous := activePolicy.Load()
		rules := defaultPolicy
		if previous != nil {
			rules = previous.rules
		}
		if activePolicy.CompareAndSwap(previous, &diagnosticPolicy{enabled: enabled, rules: rules}) {
			return
		}
	}
}

func DiagnosticsEnabled() bool { return currentPolicy().enabled }
