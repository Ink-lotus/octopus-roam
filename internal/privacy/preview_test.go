package privacy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPreviewModesDraftsAndFormalExecutionAgree(t *testing.T) {
	config := isolatedPolicy(t, JSONFields).Config()
	config.BuiltinEnabled[JSONFields] = false
	request := PreviewRequest{Revision: config.Revision, Mode: "current", RuleID: JSONFields, InputType: "json", Input: `{"echo":"fixture-secret","password":"fixture-secret","password":"fixture-secret"}`}
	result, err := Preview(config, request)
	if err != nil || strings.Contains(result.Output, "fixture-secret") || len(result.Hits) != 1 || result.Hits[0].Count != 3 {
		t.Fatal("disabled rule preview or position deduplication failed", result, err)
	}
	request.Mode = "enabled"
	result, err = Preview(config, request)
	if err != nil || result.Output != request.Input || len(result.Hits) != 0 {
		t.Fatal("enabled-rule preview ignored saved states", result, err)
	}
	request.Draft = &Rule{Name: "draft", Kind: Literal, Pattern: "fixture-secret", Enabled: true}
	request.RuleID = ""
	result, err = Preview(config, request)
	if err != nil || strings.Contains(result.Output, "fixture-secret") {
		t.Fatal("draft preview failed", result, err)
	}
	formal := config
	formal.CustomRules = []Rule{{ID: "custom_preview", Name: "draft", Kind: Literal, Pattern: "fixture-secret", Enabled: true}}
	policy, _ := Compile(formal)
	if NewWithPolicy(policy).Body(request.Input) != result.Output {
		t.Fatal("preview differs from formal execution")
	}
	if len(config.CustomRules) != 0 || config.BuiltinEnabled[JSONFields] {
		t.Fatal("preview changed its input configuration")
	}
	request.Draft.Enabled = false
	result, err = Preview(config, request)
	if err != nil || result.Output != request.Input {
		t.Fatal("disabled draft was enabled in collection mode")
	}
	request.Mode = "current"
	result, err = Preview(config, request)
	if err != nil || result.Output == request.Input {
		t.Fatal("current-rule preview did not temporarily enable a draft")
	}
}

func TestPreviewLimitsAndErrorsDoNotIncludeInput(t *testing.T) {
	config := DefaultConfig()
	base := PreviewRequest{Revision: config.Revision, Mode: "enabled", InputType: "text", Input: "private-sample-content"}
	for _, change := range []func(*PreviewRequest){
		func(r *PreviewRequest) { r.InputType = "json" },
		func(r *PreviewRequest) { r.InputType = "headers" },
		func(r *PreviewRequest) { r.InputType = "headers"; r.Input = `{"X-Header":123}` },
		func(r *PreviewRequest) { r.InputType = "headers"; r.Input = `{"X-Header":[null]}` },
		func(r *PreviewRequest) { r.Input = strings.Repeat("x", MaxPreviewBytes+1) },
		func(r *PreviewRequest) { r.KnownValues = make([]string, MaxKnownValues+1) },
		func(r *PreviewRequest) {
			r.Draft = &Rule{Name: "draft", Kind: Regex, Pattern: "(?=private-sample-content)"}
		},
		func(r *PreviewRequest) {
			r.RuleID = JSONFields
			r.Draft = &Rule{Name: "fake", Kind: Literal, Pattern: "private-sample-content"}
		},
	} {
		request := base
		change(&request)
		if _, err := Preview(config, request); err == nil || strings.Contains(err.Error(), "private-sample-content") {
			t.Fatal("preview accepted invalid input or echoed it", err)
		}
	}
	request := base
	request.Mode, request.RuleID, request.Input = "current", KnownCredentials, "demo-credential"
	result, err := Preview(config, request)
	if err != nil || result.Output != Mask("demo-credential") {
		t.Fatal("known credential example not isolated", err)
	}
	request.InputType, request.Input = "headers", `{"Authorization":["Bearer fixture-one","Bearer fixture-two"],"Accept":"text/plain"}`
	request.RuleID = SensitiveHeaders
	result, err = Preview(config, request)
	if err != nil || !json.Valid([]byte(result.Output)) || strings.Contains(result.Output, "fixture-") || len(result.Hits) != 1 || result.Hits[0].Count != 2 {
		t.Fatal("header preview failed", result, err)
	}
}

func TestPreviewRequestRequiresStringInput(t *testing.T) {
	for _, input := range []string{
		`{"revision":"v","mode":"enabled","input_type":"text"}`,
		`{"revision":"v","mode":"enabled","input_type":"text","input":null}`,
		`{"revision":"v","mode":"enabled","input_type":"text","input":17}`,
	} {
		if _, err := ParsePreviewRequest([]byte(input)); err == nil {
			t.Fatal("preview accepted a missing or non-string input")
		}
	}
	if _, err := ParsePreviewRequest([]byte(`{"revision":"v","mode":"enabled","input_type":"text","input":""}`)); err != nil {
		t.Fatal("explicit empty text is a valid sample", err)
	}
}
