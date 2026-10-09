package op

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

func preservePrivacySettings(t *testing.T) {
	t.Helper()
	var settings []model.Setting
	if err := db.GetDB().Where("key IN ?", []model.SettingKey{model.SettingKeyPrivacyProtectionRules, model.SettingKeyPrivacyProtectionEnabled}).Find(&settings).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, setting := range settings {
			if err := db.GetDB().Model(&model.Setting{}).Where("key = ?", setting.Key).Update("value", setting.Value).Error; err != nil {
				t.Error(err)
			}
		}
		if err := settingRefreshCache(context.Background()); err != nil {
			t.Error(err)
		}
	})
}

func TestPrivacyMutationsConflictAndPersistence(t *testing.T) {
	preservePrivacySettings(t)
	initial, err := PrivacyRulesGet()
	if err != nil {
		t.Fatal(err)
	}
	mutation := PrivacyMutation{Revision: initial.Revision, Action: "create", Rule: privacy.Rule{Name: "客户", Kind: privacy.Literal, Pattern: " a! ", Enabled: true}}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := PrivacyRulesMutate(t.Context(), mutation); errorsSeen <- err })
	}
	wg.Wait()
	close(errorsSeen)
	successes, conflicts := 0, 0
	for err := range errorsSeen {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrPrivacyConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("concurrent editors did not receive one success and one conflict")
	}
	view, _ := PrivacyRulesGet()
	last := view.CustomRules[len(view.CustomRules)-1]
	if last.Pattern != " a! " || last.ID == "" || view.Revision == initial.Revision {
		t.Fatal("rule form or revision was not preserved")
	}
	if err := settingRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := PrivacyRulesGet()
	if reloaded.Revision != view.Revision || reloaded.CustomRules[len(reloaded.CustomRules)-1] != last {
		t.Fatal("reload changed saved rule state")
	}
	for _, action := range []string{"update", "delete"} {
		if _, err := PrivacyRulesMutate(t.Context(), PrivacyMutation{Revision: view.Revision, Action: action, ID: privacy.JSONFields}); err == nil {
			t.Fatal("builtin definition was writable")
		}
	}
	mutation.Revision = view.Revision
	mutation.Rule.Kind, mutation.Rule.Pattern = privacy.Regex, "a*"
	if _, err := PrivacyRulesMutate(t.Context(), mutation); err == nil {
		t.Fatal("invalid regex was saved")
	}
	after, _ := PrivacyRulesGet()
	if after.Revision != view.Revision {
		t.Fatal("failed save replaced the effective configuration")
	}
	if err := SettingSetString(model.SettingKeyPrivacyProtectionRules, privacy.DefaultConfigJSON()); err == nil {
		t.Fatal("generic string setter bypassed rule permissions")
	}
	if err := SettingSetInt(model.SettingKeyPrivacyProtectionRules, 1); err == nil {
		t.Fatal("generic integer setter bypassed rule permissions")
	}
}

func TestPrivacyReloadRejectsCorruptConfigWithoutPublishing(t *testing.T) {
	preservePrivacySettings(t)
	initial, _ := PrivacyRulesGet()
	if err := db.GetDB().Model(&model.Setting{}).Where("key = ?", model.SettingKeyPrivacyProtectionRules).Update("value", `{"version":999}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := settingRefreshCache(t.Context()); err == nil {
		t.Fatal("invalid stored config was silently repaired")
	}
	after, _ := PrivacyRulesGet()
	if after.Revision != initial.Revision {
		t.Fatal("invalid reload replaced the last valid policy")
	}
}

func TestPrivacyMutationStrictShapes(t *testing.T) {
	for _, raw := range []string{
		`{"revision":"v","action":"toggle","id":"builtin.json_fields","enabled":null}`,
		`{"revision":"v","action":"toggle","id":"builtin.json_fields","enabled":false,"rule":{}}`,
		`{"revision":"v","action":"delete","id":"custom_a","enabled":false}`,
		`{"revision":"v","action":"create","rule":{"id":"builtin.json_fields","name":"bad","kind":"literal","pattern":"x","enabled":true}}`,
		`{"revision":"v","action":"create","rule":{"name":"bad","kind":"literal","pattern":"x"}}`,
	} {
		if _, err := ParsePrivacyMutation([]byte(raw)); err == nil {
			t.Fatal("accepted invalid mutation shape")
		}
	}
	input := map[string]any{"revision": "v", "action": "toggle", "id": privacy.JSONFields, "enabled": false}
	raw, _ := json.Marshal(input)
	mutation, err := ParsePrivacyMutation(raw)
	if err != nil || mutation.Enabled {
		t.Fatal("explicit false was rejected", err)
	}
}

func TestPrivacyFailedPersistenceRetainsPublishedConfiguration(t *testing.T) {
	preservePrivacySettings(t)
	before, _ := PrivacyRulesGet()
	stored, _ := SettingGetString(model.SettingKeyPrivacyProtectionRules)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := PrivacyRulesMutate(ctx, PrivacyMutation{Revision: before.Revision, Action: "create", Rule: privacy.Rule{Name: "canceled write", Kind: privacy.Literal, Pattern: "canceled-fixture", Enabled: true}})
	if err == nil {
		t.Fatal("canceled persistence succeeded")
	}
	after, _ := PrivacyRulesGet()
	var row model.Setting
	if err := db.GetDB().First(&row, "key = ?", model.SettingKeyPrivacyProtectionRules).Error; err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || row.Value != stored {
		t.Fatal("failed persistence replaced configuration or cache")
	}
}
