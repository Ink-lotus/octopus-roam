package relay

import (
	"context"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/privacy"
)

func TestRuleChangesKeepExistingRequestAndBodyCacheSnapshot(t *testing.T) {
	setTestPrivacyProtection(t, true)
	initial, err := op.PrivacyRulesGet()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, _ := op.PrivacyRulesGet()
		_, err := op.PrivacyRulesMutate(context.Background(), op.PrivacyMutation{Revision: current.Revision, Action: "toggle", ID: privacy.JSONFields, Enabled: true})
		if err != nil {
			t.Error(err)
		}
	})
	body := `{ "password": "fixture-snapshot-rule" }`
	old := privacy.NewDiagnostic()
	cache := &diagnosticBodyCache{}
	before := cache.read(old, body)
	if strings.Contains(before, "fixture-snapshot-rule") {
		t.Fatal("initial request was not protected")
	}
	if _, err := op.PrivacyRulesMutate(t.Context(), op.PrivacyMutation{Revision: initial.Revision, Action: "toggle", ID: privacy.JSONFields, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if cache.read(old, body) != before {
		t.Fatal("existing request changed after a rule write")
	}
	if got := cache.read(privacy.NewDiagnostic(), body); got != body {
		t.Fatal("new request did not use new rule policy", got)
	}
}
