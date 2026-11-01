package op

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

var retiredSettingKeys = []model.SettingKey{
	model.SettingKeyPrivacyProtectionEnabled,
	model.SettingKeyPrivacyProtectionRules,
}

// Startup tests own a process as well as a database: op caches must not be
// redirected underneath other tests or background observers.
func runRetiredSettingsIsolated(t *testing.T) bool {
	t.Helper()
	if os.Getenv("OCTOPUS_W03_TEST_CASE") == t.Name() {
		return false
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.timeout=2m")
	command.Env = append(os.Environ(), "OCTOPUS_W03_TEST_CASE="+t.Name())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("isolated compatibility test failed: %v\n%s", err, output)
	}
	return true
}

func openRetiredSettingsDatabase(t *testing.T) string {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy-settings.db")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRetiredSettingRows(t *testing.T, expected []model.Setting) {
	t.Helper()
	var actual []model.Setting
	if err := db.GetDB().Where(map[string]any{"key": retiredSettingKeys}).Order("key").Find(&actual).Error; err != nil {
		t.Fatal(err)
	}
	if len(actual) != len(expected) || (len(expected) > 0 && !reflect.DeepEqual(actual, expected)) {
		t.Fatalf("legacy rows changed: got %#v, want %#v", actual, expected)
	}
	list, err := SettingList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range list {
		if model.IsRetiredSetting(setting.Key) {
			t.Fatal("retired setting appeared in the active list")
		}
	}
}

func TestRetiredSettingsStartupAndCache(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	path := openRetiredSettingsDatabase(t)
	for _, setting := range model.DefaultSettings() {
		if model.IsRetiredSetting(setting.Key) {
			t.Fatal("new installation created a retired default")
		}
	}
	assertRetiredSettingRows(t, nil)
	legacy := []model.Setting{
		{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "not-a-boolean"},
		{Key: model.SettingKeyPrivacyProtectionRules, Value: "{malformed legacy rule text\x00"},
	}
	if err := db.GetDB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := SettingSetString(model.SettingKeyModelFilter, "fixture-control"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("legacy values prevented startup: %v", err)
	}
	if value, err := SettingGetString(model.SettingKeyModelFilter); err != nil || value != "fixture-control" {
		t.Fatal("ordinary setting did not survive reopening", err)
	}
	for _, key := range retiredSettingKeys {
		// Even a stale cache entry cannot make the retired API usable.
		settingCache.Set(key, "stale-fixture")
		_, getString := SettingGetString(key)
		_, getInt := SettingGetInt(key)
		_, getBool := SettingGetBool(key)
		for _, err := range []error{
			getString, getInt, getBool,
			SettingSetString(key, "true"), SettingSetInt(key, 1),
			(&model.Setting{Key: key, Value: "invalid"}).Validate(),
		} {
			if !errors.Is(err, model.ErrSettingRetired) {
				t.Fatalf("retired key was accepted: %s: %v", key, err)
			}
		}
	}
	assertRetiredSettingRows(t, legacy)
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	for _, key := range retiredSettingKeys {
		if _, exists := settingCache.Get(key); exists {
			t.Fatal("cache reload retained a retired entry")
		}
	}
	assertRetiredSettingRows(t, legacy)
}
