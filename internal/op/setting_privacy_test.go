package op

import (
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

func TestPrivacySettingDefaultsPersistsAndReloads(t *testing.T) {
	key := model.SettingKeyPrivacyProtectionEnabled
	previous, err := SettingGetString(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SettingSetString(key, previous) })
	if previous != "false" || privacy.DiagnosticsEnabled() {
		t.Fatal("new installations must default to disabled privacy")
	}
	for _, value := range []string{"true", "false"} {
		if err := SettingSetString(key, value); err != nil {
			t.Fatal(err)
		}
		enabled := value == "true"
		if privacy.DiagnosticsEnabled() != enabled {
			t.Fatal("saved setting did not apply to new requests")
		}
		var stored model.Setting
		if err := db.GetDB().First(&stored, "key = ?", key).Error; err != nil || stored.Value != value {
			t.Fatal("privacy setting was not persisted", err)
		}
		privacy.SetDiagnosticsEnabled(!enabled)
		if err := settingRefreshCache(t.Context()); err != nil {
			t.Fatal(err)
		}
		if privacy.DiagnosticsEnabled() != enabled {
			t.Fatal("cache reload did not restore the saved preference")
		}
	}
	for _, value := range []string{"", "1", "TRUE", "invalid"} {
		if err := SettingSetString(key, value); err == nil {
			t.Fatalf("accepted invalid privacy value %q", value)
		}
	}
	if err := SettingSetInt(key, 1); err == nil {
		t.Fatal("integer setter bypassed boolean validation")
	}
	if privacy.DiagnosticsEnabled() {
		t.Fatal("rejected updates changed the active preference")
	}

	// Simulate upgrading an existing database that has no privacy setting.
	if err := db.GetDB().Delete(&model.Setting{}, "key = ?", key).Error; err != nil {
		t.Fatal(err)
	}
	settingCache.Del(key)
	if err := settingRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, err := SettingGetString(key); err != nil || value != "false" || privacy.DiagnosticsEnabled() {
		t.Fatal("upgrade did not default privacy to disabled", err)
	}
}
