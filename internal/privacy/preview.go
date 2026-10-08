package privacy

import (
	"encoding/json"
	"strings"
)

type PreviewRequest struct {
	Revision    string   `json:"revision"`
	InputType   string   `json:"input_type"`
	Input       string   `json:"input"`
	Mode        string   `json:"mode"`
	RuleID      string   `json:"rule_id,omitempty"`
	Draft       *Rule    `json:"draft,omitempty"`
	KnownValues []string `json:"known_values,omitempty"`
}

type PreviewResult struct {
	Revision string `json:"revision"`
	Output   string `json:"output"`
	Hits     []Hit  `json:"hits"`
}

func Preview(config Config, request PreviewRequest) (PreviewResult, error) {
	total := len(request.Input)
	for _, value := range request.KnownValues {
		total += len(value)
	}
	if total > MaxPreviewBytes || len(request.KnownValues) > MaxKnownValues {
		return PreviewResult{}, Invalid("input", "request_limit", "preview input and known values exceed the preview limits")
	}
	switch request.InputType {
	case "text", "url":
	case "json":
		if !json.Valid([]byte(request.Input)) {
			return PreviewResult{}, Invalid("input", "json", "sample must contain valid JSON")
		}
	case "headers":
		var headers map[string]json.RawMessage
		if err := DecodeStrict([]byte(request.Input), &headers); err != nil || headers == nil {
			return PreviewResult{}, Invalid("input", "headers", "headers must be a JSON object of strings or string arrays")
		}
		for _, value := range headers {
			var text string
			var values []json.RawMessage
			if string(value) == "null" || (json.Unmarshal(value, &text) != nil && json.Unmarshal(value, &values) != nil) {
				return PreviewResult{}, Invalid("input", "headers", "header values must be strings or string arrays")
			}
			for _, entry := range values {
				if string(entry) == "null" || json.Unmarshal(entry, &text) != nil {
					return PreviewResult{}, Invalid("input", "headers", "header values must be strings or string arrays")
				}
			}
		}
	default:
		return PreviewResult{}, Invalid("input_type", "unknown", "unsupported preview input type")
	}
	if request.Mode != "current" && request.Mode != "enabled" {
		return PreviewResult{}, Invalid("mode", "unknown", "unsupported preview mode")
	}
	config = cloneConfig(config)
	selected := request.RuleID
	_, builtin := config.BuiltinEnabled[selected]
	index := -1
	for i, rule := range config.CustomRules {
		if rule.ID == selected {
			index = i
			break
		}
	}
	if selected != "" && !builtin && index < 0 {
		return PreviewResult{}, Invalid("rule_id", "not_found", "preview rule no longer exists")
	}
	if request.Draft != nil {
		if builtin || request.Draft.ID != "" {
			return PreviewResult{}, Invalid("draft", "builtin", "only custom rule forms can be previewed as drafts")
		}
		if err := ValidateRule(*request.Draft); err != nil {
			return PreviewResult{}, err
		}
		draft := *request.Draft
		if selected == "" {
			selected = "custom_preview"
		}
		draft.ID = selected
		if request.Mode == "current" {
			config.CustomRules = []Rule{draft}
		} else if index >= 0 {
			config.CustomRules[index] = draft
		} else {
			config.CustomRules = append(config.CustomRules, draft)
		}
	}
	if request.Mode == "current" {
		if selected == "" {
			return PreviewResult{}, Invalid("rule_id", "required", "select a rule or provide a custom draft")
		}
		for id := range config.BuiltinEnabled {
			config.BuiltinEnabled[id] = id == selected
		}
		for i := range config.CustomRules {
			config.CustomRules[i].Enabled = config.CustomRules[i].ID == selected
		}
	}
	policy, err := Compile(config)
	if err != nil {
		return PreviewResult{}, err
	}
	r := NewWithPolicy(policy)
	knownValues := request.KnownValues
	if len(knownValues) == 0 && request.RuleID == KnownCredentials {
		knownValues = []string{"demo-credential"}
	}
	r.Register(KnownCredentials, knownValues...)
	output, hits := r.execute(request.Input, request.InputType, true)
	if hits == nil {
		hits = []Hit{}
	}
	return PreviewResult{Revision: config.Revision, Output: output, Hits: hits}, nil
}

// A sample can intentionally contain malformed URL/query data to exercise the
// documented fallback rules; its contents are never included in an error.
func ParsePreviewRequest(data []byte) (PreviewRequest, error) {
	var request PreviewRequest
	if err := DecodeStrict(data, &request); err != nil {
		return request, err
	}
	var required struct {
		Input *string `json:"input"`
	}
	_ = json.Unmarshal(data, &required)
	if required.Input == nil {
		return request, Invalid("input", "required", "sample input must be a string")
	}
	if strings.TrimSpace(request.Revision) == "" {
		return request, Invalid("revision", "required", "revision is required")
	}
	return request, nil
}
