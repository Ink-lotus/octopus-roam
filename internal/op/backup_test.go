package op

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func TestValidateDumpOrigin(t *testing.T) {
	for _, tc := range []struct {
		name      string
		dump      *model.DBDump
		wantError string
	}{
		{"roam", &model.DBDump{Product: "octopus-roam", Version: 1000}, ""},
		{"upstream zero", &model.DBDump{Version: 0}, ""},
		{"upstream five", &model.DBDump{Version: 5}, ""},
		{"nil", nil, "empty dump"},
		{"ambiguous six", &model.DBDump{Version: 6}, "re-export"},
		{"upstream four", &model.DBDump{Version: 4}, "no product marker"},
		{"future upstream", &model.DBDump{Version: 7}, "no product marker"},
		{"missing identity", &model.DBDump{Version: 1000}, "no product marker"},
		{"foreign", &model.DBDump{Product: "foreign-product", Version: 5}, "product"},
		{"case", &model.DBDump{Product: "Octopus-Roam", Version: 1000}, "product"},
		{"whitespace", &model.DBDump{Product: " octopus-roam ", Version: 1000}, "product"},
		{"roam zero", &model.DBDump{Product: "octopus-roam", Version: 0}, "unsupported octopus-roam"},
		{"roam six", &model.DBDump{Product: "octopus-roam", Version: 6}, "unsupported octopus-roam"},
		{"future roam", &model.DBDump{Product: "octopus-roam", Version: 1001}, "unsupported octopus-roam"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDumpOrigin(tc.dump)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestDumpConstantsAreIndependentOfUpstream(t *testing.T) {
	// 后续上游同步不能带入其版本或扩大接受白名单，否则重新引入备份撞号。
	if dumpProduct != "octopus-roam" || dbDumpVersion != 1000 {
		t.Fatal("backup identity changed")
	}
	if len(upstreamDumpVersions) != 2 {
		t.Fatal("upstream whitelist expanded")
	}
	for _, version := range []int{0, 5} {
		if _, ok := upstreamDumpVersions[version]; !ok {
			t.Fatalf("missing upstream %d", version)
		}
	}
	if len(roamDumpVersions) != 1 {
		t.Fatal("roam whitelist changed")
	}
	if _, ok := roamDumpVersions[1000]; !ok {
		t.Fatal("missing roam 1000")
	}
}

func TestBackupDisabledStateAndConflictSemantics(t *testing.T) {
	const id = 810001
	dump := &model.DBDump{
		Version:     5,
		Channels:    []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: t.Name(), BaseURL: "https://example.invalid", Enabled: false}}},
		ChannelKeys: []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "disabled", Key: "synthetic-backup-key", Enabled: false}}},
		APIKeys:     []model.APIKey{{ID: id, Name: t.Name(), APIKey: "synthetic-client-key", Enabled: false, SupportedModels: []string{"example"}}},
		Groups:      []model.Group{{ID: id, Name: t.Name()}},
	}
	t.Cleanup(func() {
		db.GetDB().Delete(&model.Channel{}, id)
		db.GetDB().Delete(&model.APIKey{}, id)
		db.GetDB().Delete(&model.Group{}, id)
	})
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatal(err)
	}
	assertState := func(channelEnabled, keyEnabled, apiEnabled bool) {
		t.Helper()
		var channel model.Channel
		var key model.ChannelKey
		var api model.APIKey
		if err := db.GetDB().First(&channel, id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.GetDB().First(&key, id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.GetDB().First(&api, id).Error; err != nil {
			t.Fatal(err)
		}
		if channel.Enabled != channelEnabled || key.Enabled != keyEnabled || api.Enabled != apiEnabled {
			t.Fatalf("enabled: channel=%t key=%t api=%t", channel.Enabled, key.Enabled, api.Enabled)
		}
		if !channel.FollowsGlobalAutoUpdate() || channel.AllowedProtocols != model.ProtocolAll || channel.MaxConcurrency != 0 {
			t.Fatal("legacy channel defaults lost")
		}
		if len(api.SupportedModels) != 1 || api.SupportedModels[0] != "example" {
			t.Fatal("serialized field lost")
		}
	}
	assertState(false, false, false)
	var group model.Group
	if err := db.GetDB().First(&group, id).Error; err != nil {
		t.Fatal(err)
	}
	if group.Mode != model.GroupModeManual || group.ModelMatchMode != model.GroupModelMatchModeKeyword {
		t.Fatal("empty group defaults lost")
	}
	// 已有渠道/API Key 保留配置，渠道凭据按备份覆盖；必须同时覆盖 true 和 false。
	dump.Channels[0].Enabled, dump.ChannelKeys[0].Enabled, dump.APIKeys[0].Enabled = true, true, true
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatal(err)
	}
	assertState(false, true, false)
	dump.ChannelKeys[0].Enabled = false
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatal(err)
	}
	assertState(false, false, false)
}

func TestBackupRejectsBeforeWriteAndRollsBackInvalidMember(t *testing.T) {
	const id = 810002
	for _, version := range []int{6, 7, 1000} {
		if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: version, Groups: []model.Group{{ID: id, Name: t.Name()}}}); err == nil {
			t.Fatalf("accepted version %d", version)
		}
	}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{
		Version:    5,
		Groups:     []model.Group{{ID: id, Name: t.Name()}},
		GroupItems: []model.GroupItem{{ID: id, GroupID: id, ChannelGrantID: id}},
	}); err == nil {
		t.Fatal("accepted dangling member")
	}
	var count int64
	if err := db.GetDB().Model(&model.Group{}).Where("id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected import left data behind")
	}
}

func TestBackupExportJSONRoundTrip(t *testing.T) {
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if dump.Product != "octopus-roam" || dump.Version != 1000 {
		t.Fatal("export lacks roam identity")
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["product"]) != `"octopus-roam"` {
		t.Fatal("product JSON tag missing")
	}
	var decoded model.DBDump
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateDumpOrigin(&decoded); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := DBImportIncremental(t.Context(), &decoded); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBackupRestoresExplicitConfiguration(t *testing.T) {
	const id = 810003
	no := false
	policy := model.DefaultGroupRelayConfig()
	policy.MemberMaxAttempts = 4
	policy.RetryMode = model.RetryModeExponential
	policy.RetryInitialIntervalSeconds = 2
	dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion,
		Channels:         []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: t.Name(), BaseURL: "https://example.invalid", Enabled: false, FollowGlobalAutoUpdate: &no, MaxConcurrency: 3, AllowedProtocols: model.ProtocolOpenAIResponse}}},
		ChannelKeys:      []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "key", Key: "config-fixture-key", Enabled: false}}},
		ChannelModels:    []model.ChannelModel{{ID: id, ChannelID: id, Name: "exact-model"}},
		ChannelGrants:    []model.ChannelGrant{{ID: id, ChannelModelID: id, ChannelKeyID: id, Protocols: model.ProtocolOpenAIResponse}},
		Groups:           []model.Group{{ID: id, Name: t.Name(), Mode: model.GroupModeFailover, ModelMatchMode: model.GroupModelMatchModeExact, RelayConfig: policy}},
		GroupItems:       []model.GroupItem{{ID: id, GroupID: id, ChannelGrantID: id, Priority: 2}},
		WebhookEndpoints: []model.WebhookEndpoint{{ID: id, Name: t.Name(), Enabled: false, URL: "https://example.invalid/hook", Method: "POST", ContentType: "application/json", Headers: map[string]string{"Authorization": "Bearer synthetic-header"}, BodyTemplate: "{}", TimeoutSeconds: 15}},
	}
	t.Cleanup(func() {
		db.GetDB().Delete(&model.Group{}, id)
		db.GetDB().Delete(&model.Channel{}, id)
		db.GetDB().Delete(&model.WebhookEndpoint{}, id)
	})
	raw, err := EncryptBackup(dump, "explicit-configuration-password")
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptBackup(raw, "explicit-configuration-password")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := DBImportIncremental(t.Context(), restored); err != nil {
			t.Fatal(err)
		}
	}
	var channel model.Channel
	var group model.Group
	var item model.GroupItem
	var webhook model.WebhookEndpoint
	for _, row := range []any{&channel, &group, &item, &webhook} {
		if err := db.GetDB().First(row, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if channel.Enabled || channel.FollowsGlobalAutoUpdate() || channel.MaxConcurrency != 3 || channel.AllowedProtocols != model.ProtocolOpenAIResponse {
		t.Fatal("channel configuration lost")
	}
	if group.Mode != model.GroupModeFailover || group.ModelMatchMode != model.GroupModelMatchModeExact || group.RelayConfig != policy {
		t.Fatal("group configuration lost")
	}
	if item.Priority != 2 || item.ChannelGrantID != id {
		t.Fatal("membership lost")
	}
	if webhook.Enabled || webhook.Headers["Authorization"] != "Bearer synthetic-header" || webhook.TimeoutSeconds != 15 {
		t.Fatal("webhook configuration lost")
	}
}

func TestBackupExportUsesOneDatabaseSnapshot(t *testing.T) {
	const id = 810004
	dump := &model.DBDump{Version: 5,
		Channels:    []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: t.Name(), BaseURL: "https://example.invalid"}}},
		ChannelKeys: []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: "snapshot", Key: "snapshot-fixture-key"}}},
	}
	if _, err := DBImportIncremental(t.Context(), dump); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.GetDB().Delete(&model.Channel{}, id) })
	type snapshotContextKey struct{}
	changed := false
	callback := "r1:export_snapshot"
	if err := db.GetDB().Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if changed || tx.Statement.Table != "channels" || tx.Statement.Context.Value(snapshotContextKey{}) != true {
			return
		}
		changed = true
		// 模拟模型刷新/删除在两张表导出之间提交；必须用另一连接。
		if err := db.GetDB().Delete(&model.Channel{}, id).Error; err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Query().Remove(callback) })
	exported, err := DBExportAll(context.WithValue(t.Context(), snapshotContextKey{}, true))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("concurrent mutation did not run")
	}
	foundKey := false
	for _, key := range exported.ChannelKeys {
		if key.ID == id {
			foundKey = true
		}
	}
	if !foundKey {
		t.Fatal("export mixed table snapshots and lost a referenced key")
	}
}
