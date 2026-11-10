package db

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestChannelHeaderModelOnlyUpgradeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "header-model-only.db")
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Close() })
	// 只在空的合成库中去掉新增列，随后按上一版结构写入渠道及关联凭据。
	if err := GetDB().Migrator().DropColumn(&model.Channel{}, "custom_header_model_only"); err != nil {
		t.Fatal(err)
	}
	headers := []model.CustomHeader{{HeaderKey: "User-Agent", HeaderValue: "fixture-client/1"}}
	legacy := model.Channel{ChannelConfig: model.ChannelConfig{
		Name: "legacy-header-channel", BaseURL: "https://example.invalid", CustomHeader: headers,
	}}
	if err := GetDB().Omit("custom_header_model_only").Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	key := model.ChannelKey{ChannelID: legacy.ID, ChannelKeyConfig: model.ChannelKeyConfig{
		Name: "fixture-key", Key: "fixture-credential", Enabled: true,
	}}
	if err := GetDB().Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	var stored model.Channel
	if err := GetDB().First(&stored, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CustomHeaderModelOnly || !reflect.DeepEqual(stored.CustomHeader, headers) {
		t.Fatal("upgrade changed existing header configuration or enabled the switch")
	}
	var storedKey model.ChannelKey
	if err := GetDB().First(&storedKey, key.ID).Error; err != nil || storedKey.ChannelID != legacy.ID || storedKey.Key != key.Key {
		t.Fatalf("upgrade lost channel key relationship: %v", err)
	}
	fresh := model.Channel{ChannelConfig: model.ChannelConfig{Name: "new-header-channel", BaseURL: "https://example.invalid"}}
	if err := GetDB().Create(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if err := GetDB().First(&fresh, fresh.ID).Error; err != nil || fresh.CustomHeaderModelOnly {
		t.Fatalf("new channel switch is not off: %v", err)
	}
	if err := GetDB().Model(&stored).Update("custom_header_model_only", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	if err := GetDB().First(&stored, legacy.ID).Error; err != nil || !stored.CustomHeaderModelOnly || !reflect.DeepEqual(stored.CustomHeader, headers) {
		t.Fatalf("restart lost enabled switch or headers: %v", err)
	}
}
