package privacy

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type secretSource struct {
	id        string
	masked    string
	substring bool
	direct    bool
}

type registeredMatch struct {
	value   string
	sources []secretSource
}

type policyMatcher struct{ entries []registeredMatch }

func NewWithPolicy(policy *Policy) *Redactor {
	return &Redactor{policy: policy, disabled: !policy.active}
}

func (r *Redactor) RuleEnabled(id string) bool {
	return r != nil && !r.disabled && (r.policy == nil || r.policy.enabled(id))
}

// Register records a discovery's origin. Only active sources may register;
// disabling explicit configuration credentials never disables other sources.
func (r *Redactor) Register(id string, values ...string) {
	if !r.RuleEnabled(id) {
		return
	}
	if r.policy == nil {
		r.Add(values...)
		return
	}
	substring, aliasOnly := false, false
	for _, rule := range r.policy.custom {
		if rule.ID == id {
			substring = rule.Kind == Literal || rule.Kind == Regex
			// A contextual regex match must not become an unrestricted literal
			// rule. Keep only its distinct encoded aliases for diagnostic echoes.
			aliasOnly = rule.Kind == Regex
			break
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sources == nil {
		r.sources = map[string]map[string]secretSource{}
	}
	changed := false
	set := func(value, masked string, direct bool) {
		if value == "" || ContainsMask(value) {
			return
		}
		if r.sources[value] == nil {
			r.sources[value] = map[string]secretSource{}
		}
		source := secretSource{id, masked, substring, direct}
		previous, exists := r.sources[value][id]
		if exists && (previous == source || (previous.direct && !direct) || (previous.direct == direct && previous.masked < masked)) {
			return
		}
		r.sources[value][id] = source
		changed = true
	}
	for _, value := range values {
		if value == "" || ContainsMask(value) {
			continue
		}
		masked := Mask(value)
		if !aliasOnly {
			set(value, masked, true)
		}
		encodedJSON, _ := json.Marshal(value)
		for _, encoded := range []string{url.QueryEscape(value), url.PathEscape(value), string(encodedJSON[1 : len(encodedJSON)-1])} {
			if encoded != value {
				set(encoded, masked, false)
			}
		}
		if !substring {
			if fields := strings.Fields(value); len(fields) == 2 && (strings.EqualFold(fields[0], "Bearer") || strings.EqualFold(fields[0], "Basic")) {
				set(fields[1], Mask(fields[1]), true)
			}
		}
	}
	if changed {
		r.revision++
		r.policyMatcher = nil
	}
}

func (r *Redactor) policyMatcherSnapshot() *policyMatcher {
	r.mu.RLock()
	matcher := r.policyMatcher
	r.mu.RUnlock()
	if matcher != nil {
		return matcher
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.policyMatcher != nil {
		return r.policyMatcher
	}
	matcher = &policyMatcher{}
	for value, sources := range r.sources {
		entry := registeredMatch{value: value}
		for _, source := range sources {
			entry.sources = append(entry.sources, source)
		}
		sort.Slice(entry.sources, func(i, j int) bool { return r.policy.order[entry.sources[i].id] < r.policy.order[entry.sources[j].id] })
		matcher.entries = append(matcher.entries, entry)
	}
	sort.Slice(matcher.entries, func(i, j int) bool {
		if len(matcher.entries[i].value) == len(matcher.entries[j].value) {
			return matcher.entries[i].value < matcher.entries[j].value
		}
		return len(matcher.entries[i].value) > len(matcher.entries[j].value)
	})
	r.policyMatcher = matcher
	return matcher
}

func (r *Redactor) fieldSources(name, builtin string) []string {
	var sources []string
	sensitive := IsSensitiveName(name)
	if builtin == JSONFields {
		sensitive = isSensitiveBodyName(name)
	}
	if sensitive && r.RuleEnabled(builtin) {
		sources = append(sources, builtin)
	}
	for _, rule := range r.policy.custom {
		if rule.Kind == FieldName && strings.EqualFold(rule.Pattern, name) {
			sources = append(sources, rule.ID)
		}
	}
	return sources
}

func (r *Redactor) registerHeader(name, value string) []string {
	sources := r.fieldSources(name, SensitiveHeaders)
	for _, id := range sources {
		r.Register(id, value)
		if id != SensitiveHeaders {
			continue
		}
		if strings.Contains(strings.ToLower(name), "cookie") {
			for _, pair := range strings.Split(value, ";") {
				if _, secret, ok := strings.Cut(pair, "="); ok {
					r.Register(id, strings.TrimSpace(secret))
				}
			}
		}
		parts := strings.Fields(value)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Basic") {
			if decoded, err := base64.StdEncoding.DecodeString(parts[1]); err == nil {
				username, password, _ := strings.Cut(string(decoded), ":")
				r.Register(id, username, password)
			}
		}
	}
	return sources
}

func (r *Redactor) policyAddHeaders(headers http.Header) {
	for name, values := range headers {
		for _, value := range values {
			r.registerHeader(name, value)
			plan := newRedactionPlan(r, value)
			plan.custom(plan.root())
		}
	}
}
