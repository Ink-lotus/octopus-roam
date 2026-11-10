package op

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestChannelCooldownRulesPresenceAndPersistence(t *testing.T) {
	const idName = "w04-cooldown-presence"
	detail, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{
			Name: idName, Enabled: true, BaseURL: "https://fixture.example",
			CooldownRules: []model.CooldownRule{{ErrorCode: " 429 ", Keyword: " quota ", CooldownSeconds: 30, Scope: model.CooldownScopeModel}},
		},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ChannelDel(detail.ID, t.Context()) })
	if len(detail.CooldownRules) != 1 || detail.CooldownRules[0].ErrorCode != "429" || detail.CooldownRules[0].Keyword != "quota" {
		t.Fatalf("create did not normalize rules: %+v", detail.CooldownRules)
	}

	withoutRules := *detail
	withoutRules.Name = idName + "-renamed"
	withoutRules.CooldownRules = nil
	updated, err := ChannelUpdate(&withoutRules, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.CooldownRules) != 1 || updated.CooldownRules[0].CooldownSeconds != 30 {
		t.Fatalf("omitted update cleared rules: %+v", updated.CooldownRules)
	}

	invalid := *updated
	invalid.CooldownRules = []model.CooldownRule{{Keyword: "", CooldownSeconds: 0, Scope: model.CooldownScopeModel}}
	if _, err := ChannelUpdateWithOptions(&invalid, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context()); !errors.Is(err, model.ErrInvalidCooldownRule) {
		t.Fatalf("invalid update error = %v", err)
	}
	stored, err := ChannelDetailGet(detail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.CooldownRules) != 1 || stored.CooldownRules[0].CooldownSeconds != 30 {
		t.Fatalf("failed update changed stored rules: %+v", stored.CooldownRules)
	}

	cleared := stored
	cleared.CooldownRules = []model.CooldownRule{}
	updated, err = ChannelUpdateWithOptions(&cleared, model.ChannelSaveOptions{CooldownRulesPresent: true}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if updated.CooldownRules == nil || len(updated.CooldownRules) != 0 {
		t.Fatalf("explicit empty update did not clear rules: %+v", updated.CooldownRules)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	restarted, err := ChannelDetailGet(detail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.CooldownRules == nil || len(restarted.CooldownRules) != 0 {
		t.Fatalf("restart did not preserve empty rules: %+v", restarted.CooldownRules)
	}
}

func TestCooldownRulesBackupVersionAndRoundTrip(t *testing.T) {
	const id = 840041
	rules := []model.CooldownRule{{ErrorCode: "429", Keyword: "quota", CooldownSeconds: 120, Scope: model.CooldownScopeChannel}}
	dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion, Channels: []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: "w04-backup-channel", BaseURL: "https://fixture.example", CooldownRules: rules}}}}
	raw, err := EncryptBackup(dump, "w04-backup-password")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptBackup(raw, "w04-backup-password")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != 1003 || len(restored.GroupCategories) != 0 || len(restored.Channels) != 1 || len(restored.Channels[0].CooldownRules) != 1 || restored.Channels[0].CooldownRules[0] != rules[0] {
		t.Fatalf("backup round trip changed rules: %+v", restored)
	}
	if _, err := DBImportIncremental(t.Context(), restored); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.GetDB().Delete(&model.Channel{}, id)
		_ = InitCache()
	})
	var channel model.Channel
	if err := db.GetDB().First(&channel, id).Error; err != nil {
		t.Fatal(err)
	}
	if len(channel.CooldownRules) != 1 || channel.CooldownRules[0] != rules[0] {
		t.Fatalf("database restore changed rules: %+v", channel.CooldownRules)
	}

	legacy := &model.DBDump{Product: dumpProduct, Version: 1000, Channels: []model.Channel{{ID: id + 1, ChannelConfig: model.ChannelConfig{Name: "w04-old-channel", BaseURL: "https://fixture.example", CooldownRules: rules}}}}
	if _, err := DBImportIncremental(t.Context(), legacy); err == nil || !strings.Contains(err.Error(), "cannot contain cooldown_rules") {
		t.Fatalf("legacy version with rules was accepted: %v", err)
	}
	var count int64
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", id+1).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rejected legacy backup wrote data: count=%d err=%v", count, err)
	}

	var withCategory model.DBDump
	if err := json.Unmarshal([]byte(`{"product":"octopus-roam","version":1001,"groups":[{"id":1,"name":"fixture","category_id":71}]}`), &withCategory); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), &withCategory); err == nil || !strings.Contains(err.Error(), "platform categories") {
		t.Fatalf("non-null category reference was accepted: %v", err)
	}
}
