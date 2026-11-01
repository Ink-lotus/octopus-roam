package op

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func retiredBackupSettings() []model.Setting {
	return []model.Setting{
		{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "neither true nor false"},
		{Key: model.SettingKeyPrivacyProtectionRules, Value: "{not-json"},
		{Key: model.SettingKeyPrivacyProtectionRules, Value: "\x00 duplicate legacy value"},
		{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "true"},
	}
}

func retiredBackupSnapshot(t *testing.T) []byte {
	t.Helper()
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	dump.ExportedAt = time.Time{}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRetiredSettingsBackupCompatibility(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	legacy := []model.Setting{
		{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "original-invalid-enabled"},
		{Key: model.SettingKeyPrivacyProtectionRules, Value: "{original-invalid-rules"},
	}
	if err := db.GetDB().Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	for index, origin := range []struct {
		product string
		version int
	}{{dumpProduct, 1000}, {"", 0}, {"", 5}} {
		t.Run(fmt.Sprintf("version-%d", origin.version), func(t *testing.T) {
			id := 930100 + index
			dump := &model.DBDump{
				Product: origin.product, Version: origin.version,
				Settings:    append(retiredBackupSettings(), model.Setting{Key: model.SettingKeyChannelAutoUpdateEnabled, Value: "true"}),
				Channels:    []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("legacy-backup-%d", id), BaseURL: "http://127.0.0.1:1", Enabled: false}}},
				ChannelKeys: []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "fixture", Key: "fixture-literal-key", Enabled: false}}},
			}
			const password = "synthetic W03 compatibility backup password"
			encrypted, err := EncryptBackup(dump, password)
			if err != nil {
				t.Fatal(err)
			}
			restored, err := DecryptBackup(encrypted, password)
			if err != nil || len(restored.Settings) != len(dump.Settings) {
				t.Fatal("encrypted legacy payload did not round-trip", err)
			}
			before := retiredBackupSnapshot(t)
			for _, invalid := range []struct {
				body     []byte
				password string
			}{{encrypted, "synthetic wrong password"}, {encrypted[:len(encrypted)/2], password}} {
				if _, err := DecryptBackup(invalid.body, invalid.password); err == nil {
					t.Fatal("invalid encrypted backup was accepted")
				}
				if !bytes.Equal(before, retiredBackupSnapshot(t)) {
					t.Fatal("failed decryption changed the database")
				}
			}
			result, err := DBImportIncremental(t.Context(), restored)
			if err != nil || result.RowsAffected["settings"] != 1 || result.RowsAffected["channels"] != 1 {
				t.Fatalf("legacy import failed or counted retired rows: %+v %v", result, err)
			}
			if err := InitCache(); err != nil {
				t.Fatal(err)
			}
			var channel model.Channel
			var key model.ChannelKey
			if err := db.GetDB().First(&channel, id).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.GetDB().First(&key, id).Error; err != nil {
				t.Fatal(err)
			}
			if channel.Enabled || key.Enabled || key.Key != "fixture-literal-key" {
				t.Fatal("backup changed normal configuration values")
			}
			assertRetiredSettingRows(t, legacy)
			exported, err := DBExportAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, setting := range exported.Settings {
				if model.IsRetiredSetting(setting.Key) {
					t.Fatal("export included a retired setting")
				}
			}
		})
	}
}

func TestRetiredSettingsDoNotWeakenBackupValidationOrRollback(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	for _, scenario := range []string{"duplicate-active", "invalid-active", "invalid-channel", "unknown-product", "upstream-six", "transaction-failure"} {
		t.Run(scenario, func(t *testing.T) {
			dump := &model.DBDump{
				Product: dumpProduct, Version: 1000, Settings: retiredBackupSettings(),
				Channels: []model.Channel{{ID: 940001, ChannelConfig: model.ChannelConfig{Name: "must-roll-back", BaseURL: "http://127.0.0.1:1"}}},
			}
			switch scenario {
			case "duplicate-active":
				dump.Settings = append(dump.Settings,
					model.Setting{Key: model.SettingKeyProxyURL, Value: ""},
					model.Setting{Key: model.SettingKeyProxyURL, Value: ""})
			case "invalid-active":
				dump.Settings = append(dump.Settings, model.Setting{Key: model.SettingKeyChannelAutoUpdateEnabled, Value: "bad"})
			case "invalid-channel":
				dump.Channels[0].BaseURL = ""
			case "unknown-product":
				dump.Product = "synthetic-other-product"
			case "upstream-six":
				dump.Product, dump.Version = "", 6
			case "transaction-failure":
				if err := db.GetDB().Exec("CREATE TRIGGER fail_w03_import BEFORE INSERT ON webhook_endpoints BEGIN SELECT RAISE(ABORT, 'synthetic transaction failure'); END").Error; err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.GetDB().Exec("DROP TRIGGER IF EXISTS fail_w03_import").Error })
				dump.WebhookEndpoints = []model.WebhookEndpoint{{ID: 940001, Name: "rollback", URL: "http://127.0.0.1:1"}}
			}
			before := retiredBackupSnapshot(t)
			if _, err := DBImportIncremental(t.Context(), dump); err == nil {
				t.Fatal("invalid backup was accepted")
			}
			if !bytes.Equal(before, retiredBackupSnapshot(t)) {
				t.Fatal("failed import committed a partial write")
			}
			assertRetiredSettingRows(t, nil)
		})
	}
}
