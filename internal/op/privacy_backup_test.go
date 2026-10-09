package op

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

func TestPrivacyBackupRestoresRulesAndRejectsBeforeAnyWrite(t *testing.T) {
	preservePrivacySettings(t)
	config := privacy.DefaultConfig()
	config.BuiltinEnabled[privacy.JSONFields] = false
	config.CustomRules = []privacy.Rule{{ID: "custom_backup", Name: "backup", Kind: privacy.Regex, Pattern: `client-[0-9]+`, Enabled: true}}
	raw, _ := json.Marshal(config)
	dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion, Settings: []model.Setting{{Key: model.SettingKeyPrivacyProtectionRules, Value: string(raw)}, {Key: model.SettingKeyPrivacyProtectionEnabled, Value: "true"}}}
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatal(err)
	}
	view, _ := PrivacyRulesGet()
	if !view.Enabled || view.Revision == config.Revision || len(view.CustomRules) != 1 || view.CustomRules[0].ID != "custom_backup" {
		t.Fatal("imported privacy configuration was not published", view)
	}
	if got := privacy.NewDiagnostic().Text("client-42"); strings.Contains(got, "client-42") {
		t.Fatal("import did not publish compiled custom rule")
	}
	exported, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var backupRules string
	for _, setting := range exported.Settings {
		if setting.Key == model.SettingKeyPrivacyProtectionRules {
			backupRules = setting.Value
		}
	}
	if backupRules == "" {
		t.Fatal("rules missing from exported backup")
	}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: 5, Settings: []model.Setting{{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "false"}}}); err != nil {
		t.Fatal(err)
	}
	legacy, _ := PrivacyRulesGet()
	if legacy.Enabled || len(legacy.CustomRules) != 1 || legacy.CustomRules[0] != view.CustomRules[0] {
		t.Fatal("old backup overwrote target rules")
	}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: 5, Settings: []model.Setting{{Key: model.SettingKeyPrivacyProtectionRules, Value: backupRules}}}); err != nil {
		t.Fatal(err)
	}
	restored, _ := PrivacyRulesGet()
	if restored.Revision == view.Revision || restored.CustomRules[0] != view.CustomRules[0] {
		t.Fatal("rule round trip failed")
	}
	for _, settings := range [][]model.Setting{
		{{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "yes"}},
		{{Key: model.SettingKeyPrivacyProtectionRules, Value: strings.Replace(string(raw), "client-[0-9]+", "a*", 1)}},
		{{Key: model.SettingKeyPrivacyProtectionRules, Value: strings.Replace(string(raw), `"version":1`, `"version":2`, 1)}},
		{{Key: model.SettingKeyPrivacyProtectionRules, Value: strings.Replace(string(raw), privacy.JSONFields, "builtin.future", 1)}},
		{{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "true"}, {Key: model.SettingKeyPrivacyProtectionEnabled, Value: "false"}},
	} {
		const id = 981221
		bad := &model.DBDump{Version: 5, Settings: settings, Groups: []model.Group{{ID: id, Name: "privacy-rejected-import"}}}
		if _, err := DBImportIncremental(t.Context(), bad); err == nil {
			t.Fatal("invalid backup was accepted")
		}
		var count int64
		if err := db.GetDB().Model(&model.Group{}).Where("id = ?", id).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("failed import left partial writes", err)
		}
		after, _ := PrivacyRulesGet()
		if after.Revision != restored.Revision {
			t.Fatal("failed import changed active rules")
		}
	}
}
