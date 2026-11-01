package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrInvalidCooldownRule = errors.New("invalid cooldown rule")

const MaxCooldownSeconds int64 = 2147483647

type CooldownScope string

const (
	CooldownScopeModel   CooldownScope = "model"
	CooldownScopeChannel CooldownScope = "channel"
)

// CooldownRule 是一条渠道级自定义冷却规则。规则配置只允许这四个字段。
type CooldownRule struct {
	ErrorCode       string        `json:"error_code"`
	Keyword         string        `json:"keyword"`
	CooldownSeconds int64         `json:"cooldown_seconds"`
	Scope           CooldownScope `json:"scope"`
}

// CloneCooldownRules 返回稳定的非 nil 副本，避免详情或缓存调用方共享切片底层数组。
func CloneCooldownRules(rules []CooldownRule) []CooldownRule {
	if len(rules) == 0 {
		return []CooldownRule{}
	}
	return append([]CooldownRule(nil), rules...)
}

// NormalizeCooldownRules 统一规则的空白、枚举和秒数边界，并保留数组顺序。
func NormalizeCooldownRules(rules []CooldownRule) ([]CooldownRule, error) {
	normalized := make([]CooldownRule, len(rules))
	for index, rule := range rules {
		rule, err := normalizeCooldownRule(rule, index)
		if err != nil {
			return nil, err
		}
		normalized[index] = rule
	}
	return normalized, nil
}

func normalizeCooldownRule(rule CooldownRule, index int) (CooldownRule, error) {
	rule.ErrorCode = strings.TrimSpace(rule.ErrorCode)
	rule.Keyword = strings.TrimSpace(rule.Keyword)
	if rule.ErrorCode == "" && rule.Keyword == "" {
		return rule, cooldownRuleError(index, "error_code or keyword", "at least one condition is required")
	}
	if rule.CooldownSeconds < 1 || rule.CooldownSeconds > MaxCooldownSeconds {
		return rule, cooldownRuleError(index, "cooldown_seconds", "must be an integer from 1 to 2147483647")
	}
	if rule.CooldownSeconds > int64((time.Duration(1<<63-1))/time.Second) {
		return rule, cooldownRuleError(index, "cooldown_seconds", "overflows time.Duration")
	}
	if rule.Scope != CooldownScopeModel && rule.Scope != CooldownScopeChannel {
		return rule, cooldownRuleError(index, "scope", "must be model or channel")
	}
	return rule, nil
}

func cooldownRuleError(index int, field, message string) error {
	return fmt.Errorf("%w: cooldown_rules[%d].%s %s", ErrInvalidCooldownRule, index, field, message)
}

// ValidateCooldownRulesJSON parses an API/backup array and wraps errors with the
// exact rule index. It also rejects JSON exponent notation for cooldown_seconds.
func ValidateCooldownRulesJSON(data []byte) ([]CooldownRule, error) {
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, fmt.Errorf("%w: cooldown_rules must be an array", ErrInvalidCooldownRule)
	}
	var rawRules []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawRules); err != nil {
		return nil, fmt.Errorf("%w: cooldown_rules must be an array: %v", ErrInvalidCooldownRule, err)
	}
	if rawRules == nil {
		return []CooldownRule{}, nil
	}
	rules := make([]CooldownRule, len(rawRules))
	for index, raw := range rawRules {
		if err := json.Unmarshal(raw, &rules[index]); err != nil {
			return nil, fmt.Errorf("%w: cooldown_rules[%d]: %v", ErrInvalidCooldownRule, index, err)
		}
		normalized, err := normalizeCooldownRule(rules[index], index)
		if err != nil {
			return nil, err
		}
		rules[index] = normalized
	}
	return rules, nil
}

func (rule *CooldownRule) UnmarshalJSON(data []byte) error {
	if rule == nil {
		return fmt.Errorf("%w: cooldown rule destination is nil", ErrInvalidCooldownRule)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("rule must be a JSON object")
		}
		return fmt.Errorf("%w: %v", ErrInvalidCooldownRule, err)
	}
	for field := range fields {
		switch field {
		case "error_code", "keyword", "cooldown_seconds", "scope":
		default:
			return fmt.Errorf("%w: unknown field %q", ErrInvalidCooldownRule, field)
		}
	}
	errorCode, err := optionalCooldownString(fields, "error_code")
	if err != nil {
		return fmt.Errorf("%w: error_code: %v", ErrInvalidCooldownRule, err)
	}
	keyword, err := optionalCooldownString(fields, "keyword")
	if err != nil {
		return fmt.Errorf("%w: keyword: %v", ErrInvalidCooldownRule, err)
	}
	secondsRaw, ok := fields["cooldown_seconds"]
	if !ok || bytes.Equal(bytes.TrimSpace(secondsRaw), []byte("null")) {
		return fmt.Errorf("%w: cooldown_seconds is required", ErrInvalidCooldownRule)
	}
	secondsText := string(bytes.TrimSpace(secondsRaw))
	for _, char := range secondsText {
		if char < '0' || char > '9' {
			return fmt.Errorf("%w: cooldown_seconds must be a JSON integer without exponent notation", ErrInvalidCooldownRule)
		}
	}
	seconds, err := strconv.ParseInt(secondsText, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: cooldown_seconds is out of range", ErrInvalidCooldownRule)
	}
	scopeRaw, ok := fields["scope"]
	if !ok || bytes.Equal(bytes.TrimSpace(scopeRaw), []byte("null")) {
		return fmt.Errorf("%w: scope is required", ErrInvalidCooldownRule)
	}
	var scope CooldownScope
	if err := json.Unmarshal(scopeRaw, &scope); err != nil {
		return fmt.Errorf("%w: scope must be a string", ErrInvalidCooldownRule)
	}
	*rule = CooldownRule{ErrorCode: errorCode, Keyword: keyword, CooldownSeconds: seconds, Scope: scope}
	return nil
}

func optionalCooldownString(fields map[string]json.RawMessage, field string) (string, error) {
	raw, ok := fields[field]
	if !ok {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("must be a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("must be a string")
	}
	return strings.TrimSpace(value), nil
}
