package op

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func categoryBackupFingerprint(t *testing.T) [32]byte {
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
	return sha256.Sum256(raw)
}

func categoryFixtureRows[T any](rows []T, keep func(T) bool) []T {
	result := []T{}
	for _, row := range rows {
		if keep(row) {
			result = append(result, row)
		}
	}
	return result
}

func TestBackupCategoriesEncryptedRoundTrip(t *testing.T) {
	group, category, channelID := newGroupCategoryFixture(t)
	empty, err := GroupCategoryCreate(fmt.Sprintf("w08-empty-backup-%d", category.ID), t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = GroupCategoryDelete(empty.ID, context.Background()) })
	if err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Update("request_failed", 17).Error; err != nil {
		t.Fatal(err)
	}
	exported, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// 取真实导出的本测试数据，避免把同进程其他用例的合成记录混入恢复目标。
	dump := &model.DBDump{Product: exported.Product, Version: exported.Version, ExportedAt: exported.ExportedAt,
		GroupCategories: categoryFixtureRows(exported.GroupCategories, func(c model.BackupGroupCategory) bool { return c.ID == category.ID || c.ID == empty.ID }),
		Groups:          categoryFixtureRows(exported.Groups, func(g model.Group) bool { return g.ID == group.ID }),
		GroupItems:      categoryFixtureRows(exported.GroupItems, func(i model.GroupItem) bool { return i.GroupID == group.ID }),
		Channels:        categoryFixtureRows(exported.Channels, func(c model.Channel) bool { return c.ID == channelID }),
		ChannelKeys:     categoryFixtureRows(exported.ChannelKeys, func(k model.ChannelKey) bool { return k.ChannelID == channelID }),
		ChannelModels:   categoryFixtureRows(exported.ChannelModels, func(m model.ChannelModel) bool { return m.ChannelID == channelID }),
		ChannelGrants: categoryFixtureRows(exported.ChannelGrants, func(g model.ChannelGrant) bool {
			return slices.ContainsFunc(group.Items, func(i model.GroupItem) bool { return i.ChannelGrantID == g.ID })
		}),
	}
	if len(dump.GroupCategories) != 2 || len(dump.Groups) != 1 || len(dump.GroupItems) != 2 || len(dump.ChannelGrants) != 2 {
		t.Fatal("export omitted category or member data")
	}
	raw, err := EncryptBackup(dump, "w08-fixture-backup-password")
	if err != nil {
		t.Fatal(err)
	}
	var envelope encryptedBackup
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Version != 1 || envelope.Format != "encrypted-backup" || envelope.KDF != "scrypt-n32768-r8-p1" || envelope.Cipher != "aes-256-gcm" {
		t.Fatalf("encryption container changed: %v", err)
	}
	decoded, err := DecryptBackup(raw, "w08-fixture-backup-password")
	if err != nil || decoded.Product != dumpProduct || decoded.Version != 1003 {
		t.Fatalf("content identity changed: %v", err)
	}
	plain, err := json.Marshal(decoded)
	if err != nil || strings.Contains(string(plain), "metadata_version") || strings.Contains(string(plain), "name_key") || strings.Contains(string(plain), "rule_cooldowns") || strings.Contains(string(plain), "generation") {
		t.Fatalf("runtime/internal fields entered backup: %v", err)
	}
	if err := GroupDel(group.ID, t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := ChannelDel(channelID, t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int{category.ID, empty.ID} {
		if _, err := GroupCategoryDelete(id, t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	result, err := DBImportIncremental(t.Context(), decoded)
	if err != nil || result.RowsAffected["group_categories"] != 2 || result.RowsAffected["groups"] != 1 {
		t.Fatalf("restore into absent fixture rows failed: %+v, %v", result, err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	restored, err := GroupGet(group.ID)
	if err != nil || !equalCategoryID(restored.CategoryID, &category.ID) || restored.ActiveItemID != group.ActiveItemID || !reflect.DeepEqual(restored.Items, group.Items) || restored.RelayConfig != group.RelayConfig || restored.ModelMatchMode != group.ModelMatchMode {
		t.Fatalf("backup lost category or group configuration: %v", err)
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, channelID).Error; err != nil || channel.RequestFailed != 17 || !reflect.DeepEqual(channel.CooldownRules, dump.Channels[0].CooldownRules) {
		t.Fatalf("backup lost rules or statistics: %v", err)
	}
	if !slices.ContainsFunc(GroupCategoryList().Items, func(c model.GroupCategory) bool { return c.ID == empty.ID && c.Name == empty.Name }) {
		t.Fatal("empty category did not survive encrypted restore")
	}
}

func TestBackupCategoryIDConflictsAndIncrementalSemantics(t *testing.T) {
	const a, b, available, alias, groupBase, channelID = 880071, 880072, 880073, 880074, 881001, 882001
	alpha := model.GroupCategory{ID: a, Name: "W08 Conflict Alpha", NameKey: "w08 conflict alpha"}
	beta := model.GroupCategory{ID: b, Name: "W08 Conflict Beta", NameKey: "w08 conflict beta"}
	localGroup := model.Group{ID: groupBase, Name: "w08-conflict-local-group", CategoryID: copyCategoryID(&beta.ID), Mode: model.GroupModeManual, RelayConfig: model.DefaultGroupRelayConfig()}
	localRule := model.CooldownRule{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel}
	localChannel := model.Channel{ID: channelID, ChannelConfig: model.ChannelConfig{Name: "w08-conflict-local-channel", BaseURL: "https://fixture.example", CooldownRules: []model.CooldownRule{localRule}}}
	t.Cleanup(func() {
		db.GetDB().Delete(&model.Group{}, "id BETWEEN ? AND ?", groupBase, groupBase+5)
		db.GetDB().Delete(&model.Channel{}, channelID)
		db.GetDB().Delete(&model.GroupCategory{}, "name_key LIKE ?", "w08 conflict %")
		_ = InitCache()
	})
	for _, row := range []any{&alpha, &beta, &localGroup, &localChannel} {
		if err := db.GetDB().Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	refA, refB, refAvailable, refAlias := a, b, available, alias
	dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion,
		GroupCategories: []model.BackupGroupCategory{{ID: a, Name: "W08 Conflict Incoming"}, {ID: available, Name: "W08 Conflict Available"}, {ID: alias, Name: " W08 CONFLICT ALPHA "}, {ID: b, Name: "w08 conflict beta"}},
		Groups: []model.Group{
			{ID: groupBase, Name: "should-not-overwrite", CategoryID: &refA},
			{ID: groupBase + 1, Name: "w08-conflict-new", CategoryID: &refA},
			{ID: groupBase + 2, Name: "w08-conflict-available", CategoryID: &refAvailable},
			{ID: groupBase + 3, Name: "w08-conflict-alias", CategoryID: &refAlias},
			{ID: groupBase + 4, Name: "w08-conflict-same", CategoryID: &refB},
		},
		Channels: []model.Channel{{ID: channelID, ChannelConfig: model.ChannelConfig{Name: "should-not-overwrite", BaseURL: "https://other.fixture.example", CooldownRules: []model.CooldownRule{{ErrorCode: "503", CooldownSeconds: 60, Scope: model.CooldownScopeChannel}}}}},
	}
	dump.Channels[0].RequestFailed = 23
	result, err := DBImportIncremental(t.Context(), dump)
	if err != nil || result.RowsAffected["group_categories"] != 2 {
		t.Fatalf("conflict import result: %+v, %v", result, err)
	}
	var groupCount int64
	if err := db.GetDB().Model(&model.Group{}).Where("id BETWEEN ? AND ?", groupBase, groupBase+4).Count(&groupCount).Error; err != nil || groupCount != 5 {
		t.Fatalf("conflict import did not preserve one existing and insert four groups: %d, %v", groupCount, err)
	}
	var incoming model.GroupCategory
	if err := db.GetDB().Where("name_key = ?", "w08 conflict incoming").First(&incoming).Error; err != nil || incoming.ID == a || incoming.ID == available {
		t.Fatalf("conflict ID consumed a reserved ID: %d, %v", incoming.ID, err)
	}
	for offset, expected := range []int{b, incoming.ID, available, a, b} {
		var group model.Group
		if err := db.GetDB().First(&group, groupBase+offset).Error; err != nil || !equalCategoryID(group.CategoryID, &expected) {
			t.Fatalf("group %d was mapped incorrectly: %v", offset, err)
		}
		if offset == 0 && group.Name != localGroup.Name {
			t.Fatal("incremental import overwrote existing group")
		}
	}
	var savedAlpha, savedBeta model.GroupCategory
	db.GetDB().First(&savedAlpha, a)
	db.GetDB().First(&savedBeta, b)
	if savedAlpha.Name != alpha.Name || savedBeta.Name != beta.Name {
		t.Fatal("natural-key merge overwrote local display names")
	}
	var savedChannel model.Channel
	if err := db.GetDB().First(&savedChannel, channelID).Error; err != nil || savedChannel.Name != localChannel.Name || savedChannel.BaseURL != localChannel.BaseURL || !slices.Equal(savedChannel.CooldownRules, []model.CooldownRule{localRule}) || savedChannel.RequestFailed != 23 {
		t.Fatalf("incremental rules/configuration/statistics contract changed: %v", err)
	}
	// 相同数值 ID 指向另一个本地自然键时，自然键优先；不把本地 Alpha 改名。
	second := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion,
		GroupCategories: []model.BackupGroupCategory{{ID: a, Name: "W08 CONFLICT BETA"}},
		Groups:          []model.Group{{ID: groupBase + 5, Name: "w08-conflict-key-priority", CategoryID: &refA}},
	}
	if _, err := DBImportIncremental(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	var priority model.Group
	if err := db.GetDB().First(&priority, groupBase+5).Error; err != nil || !equalCategoryID(priority.CategoryID, &beta.ID) {
		t.Fatalf("natural key lost priority over numeric ID: %v", err)
	}
	before := categoryBackupFingerprint(t)
	if repeated, err := DBImportIncremental(t.Context(), dump); err != nil || repeated.RowsAffected["group_categories"] != 0 || repeated.RowsAffected["groups"] != 0 || categoryBackupFingerprint(t) != before {
		t.Fatalf("repeat import changed restored data: %+v, %v", repeated, err)
	}
}

func TestBackupCategoriesValidationAndTransactionRollback(t *testing.T) {
	local, err := GroupCategoryCreate("W08 Validation Local", t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = GroupCategoryDelete(local.ID, context.Background()) })
	for _, tc := range []struct {
		name       string
		categories []model.BackupGroupCategory
		reference  int
	}{
		{"zero ID", []model.BackupGroupCategory{{ID: 0, Name: "Fixture"}}, 0},
		{"negative ID", []model.BackupGroupCategory{{ID: -1, Name: "Fixture"}}, -1},
		{"duplicate ID", []model.BackupGroupCategory{{ID: 71, Name: "Fixture A"}, {ID: 71, Name: "Fixture B"}}, 71},
		{"duplicate normalized name", []model.BackupGroupCategory{{ID: 71, Name: " Fixture "}, {ID: 72, Name: "FIXTURE"}}, 71},
		{"empty name", []model.BackupGroupCategory{{ID: 71, Name: " "}}, 71},
		{"long name", []model.BackupGroupCategory{{ID: 71, Name: strings.Repeat("类", 65)}}, 71},
		{"control name", []model.BackupGroupCategory{{ID: 71, Name: "Fi\x00xture"}}, 71},
		{"missing descriptor", nil, local.ID},
		{"wrong descriptor", []model.BackupGroupCategory{{ID: 71, Name: "Fixture"}}, 72},
		{"zero reference", []model.BackupGroupCategory{{ID: 71, Name: "Fixture"}}, 0},
		{"negative reference", []model.BackupGroupCategory{{ID: 71, Name: "Fixture"}}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := categoryBackupFingerprint(t)
			dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion, GroupCategories: tc.categories,
				Groups: []model.Group{{ID: 883001, Name: "w08-invalid-backup", CategoryID: &tc.reference}}}
			if _, err := DBImportIncremental(t.Context(), dump); err == nil || !strings.Contains(err.Error(), "platform categories") {
				t.Fatalf("invalid categories accepted: %v", err)
			}
			if categoryBackupFingerprint(t) != before {
				t.Fatal("rejected backup wrote data")
			}
		})
	}
	before, cacheBefore := categoryBackupFingerprint(t), GroupCategoryList()
	id := 883071
	dump := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion,
		GroupCategories: []model.BackupGroupCategory{{ID: id, Name: "W08 Rollback Category"}},
		Channels:        []model.Channel{{ID: 883001, ChannelConfig: model.ChannelConfig{Name: "w08-rollback-channel", BaseURL: "https://fixture.example"}}},
		Groups:          []model.Group{{ID: 883001, Name: "w08-rollback-group", CategoryID: &id}},
		GroupItems:      []model.GroupItem{{ID: 883001, GroupID: 883001, ChannelGrantID: 999999999, Priority: 1}},
	}
	if _, err := DBImportIncremental(t.Context(), dump); !errors.Is(err, ErrBackupWrite) {
		t.Fatalf("late foreign-key failure did not reject import: %v", err)
	}
	if categoryBackupFingerprint(t) != before || !reflect.DeepEqual(GroupCategoryList(), cacheBefore) {
		t.Fatal("transaction failure left partial category/group/channel data or cache state")
	}
}

func TestBackupCategoriesVersionDefaultsAndOrigins(t *testing.T) {
	for index, origin := range []struct {
		product string
		version int
	}{{dumpProduct, 1000}, {"", 0}, {"", 5}} {
		t.Run(fmt.Sprintf("legacy-%s-%d", origin.product, origin.version), func(t *testing.T) {
			id := 884001 + index
			raw := fmt.Sprintf(`{"product":%q,"version":%d,"group_categories":null,"groups":[{"id":%d,"name":"w08-legacy-%d"}],"channels":[{"id":%d,"name":"w08-legacy-%d","base_url":"https://fixture.example","cooldown_rules":null}]}`, origin.product, origin.version, id, id, id, id)
			var dump model.DBDump
			if err := json.Unmarshal([]byte(raw), &dump); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.GetDB().Delete(&model.Group{}, id); db.GetDB().Delete(&model.Channel{}, id) })
			if _, err := DBImportIncremental(t.Context(), &dump); err != nil {
				t.Fatal(err)
			}
			var group model.Group
			if err := db.GetDB().First(&group, id).Error; err != nil || group.CategoryID != nil {
				t.Fatalf("legacy group not uncategorized: %v", err)
			}
			for _, shape := range []string{"categories", "reference", "rules"} {
				old := &model.DBDump{Product: origin.product, Version: origin.version}
				ref := 71
				switch shape {
				case "categories":
					old.GroupCategories = []model.BackupGroupCategory{{ID: ref, Name: "Fixture"}}
				case "reference":
					old.Groups = []model.Group{{ID: id, CategoryID: &ref}}
				case "rules":
					old.Channels = []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{CooldownRules: []model.CooldownRule{{ErrorCode: "429", CooldownSeconds: 30, Scope: model.CooldownScopeModel}}}}}
				}
				before := categoryBackupFingerprint(t)
				if _, err := DBImportIncremental(t.Context(), old); err == nil || categoryBackupFingerprint(t) != before {
					t.Fatalf("old version accepted new %s semantics: %v", shape, err)
				}
			}
		})
	}
	for _, raw := range []string{
		`{"product":"octopus-roam","version":1002,"group_categories":null}`,
		`{"product":"octopus-roam","version":1002,"group_categories":[null]}`,
		`{"product":"octopus-roam","version":1002,"channels":[{"cooldown_rules":null}]}`,
		`{"version":6}`, `{"version":1000}`, `{"version":1001}`, `{"product":"other","version":1001}`, `{"product":"octopus-roam","version":1004}`,
	} {
		var dump model.DBDump
		if err := json.Unmarshal([]byte(raw), &dump); err != nil {
			t.Fatal(err)
		}
		before := categoryBackupFingerprint(t)
		if _, err := DBImportIncremental(t.Context(), &dump); err == nil || categoryBackupFingerprint(t) != before {
			t.Fatalf("invalid backup origin/shape accepted: %s, %v", raw, err)
		}
	}
}
