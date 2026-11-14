package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"gorm.io/gorm"
)

var entityTables = []string{"channels", "channel_keys", "channel_models", "channel_grants", "group_categories", "groups", "group_items", "api_keys", "webhook_endpoints"}

func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func cloneDump(dump *model.DBDump) *model.DBDump {
	raw, err := json.Marshal(dump)
	if err != nil {
		panic(err)
	}
	var copy model.DBDump
	if err := json.Unmarshal(raw, &copy); err != nil {
		panic(err)
	}
	return &copy
}

func sparseDump(dump *model.DBDump, offset int) *model.DBDump {
	d := cloneDump(dump)
	for i := range d.Channels {
		d.Channels[i].ID += offset
	}
	for i := range d.ChannelKeys {
		d.ChannelKeys[i].ID += offset
		d.ChannelKeys[i].ChannelID += offset
	}
	for i := range d.ChannelModels {
		d.ChannelModels[i].ID += offset
		d.ChannelModels[i].ChannelID += offset
	}
	for i := range d.ChannelGrants {
		d.ChannelGrants[i].ID += offset
		d.ChannelGrants[i].ChannelKeyID += offset
		d.ChannelGrants[i].ChannelModelID += offset
	}
	for i := range d.GroupCategories {
		d.GroupCategories[i].ID += offset
	}
	for i := range d.Groups {
		d.Groups[i].ID += offset
		if d.Groups[i].CategoryID != nil {
			id := *d.Groups[i].CategoryID + offset
			d.Groups[i].CategoryID = &id
		}
		if d.Groups[i].ActiveItemID != 0 {
			d.Groups[i].ActiveItemID += offset
		}
	}
	for i := range d.GroupItems {
		d.GroupItems[i].ID += offset
		d.GroupItems[i].GroupID += offset
		d.GroupItems[i].ChannelGrantID += offset
	}
	for i := range d.APIKeys {
		d.APIKeys[i].ID += offset
	}
	for i := range d.WebhookEndpoints {
		d.WebhookEndpoints[i].ID += offset
	}
	for i := range d.StatsAPIKey {
		d.StatsAPIKey[i].APIKeyID += offset
	}
	return d
}

func snapshot() (string, error) {
	dump, err := op.DBExportAll(db.GetDB().Statement.Context)
	if err != nil {
		return "", err
	}
	dump.ExportedAt = time.Time{}
	// SQL heap/order changes after an upsert do not change backup content.
	for _, rows := range []any{dump.Channels, dump.ChannelKeys, dump.ChannelModels, dump.ChannelGrants,
		dump.GroupCategories, dump.Groups, dump.GroupItems, dump.APIKeys, dump.WebhookEndpoints, dump.StatsTotal} {
		sortRows(rows, "ID")
	}
	sortRows(dump.LLMInfos, "Name")
	sortRows(dump.Settings, "Key")
	sortRows(dump.StatsDaily, "Date")
	sortRows(dump.StatsHourly, "Hour")
	sortRows(dump.StatsAPIKey, "APIKeyID")
	raw, err := json.Marshal(dump)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func sortRows(rows any, field string) {
	values := reflect.ValueOf(rows)
	sort.Slice(rows, func(i, j int) bool {
		a, b := values.Index(i).FieldByName(field), values.Index(j).FieldByName(field)
		if a.Kind() == reflect.String {
			return a.String() < b.String()
		}
		return a.Int() < b.Int()
	})
}

func maxima(conn *gorm.DB) (map[string]int64, error) {
	result := make(map[string]int64)
	for _, table := range entityTables {
		var highest sql.NullInt64
		if err := conn.Table(table).Select("MAX(id)").Scan(&highest).Error; err != nil {
			return nil, err
		}
		result[table] = highest.Int64
	}
	return result, nil
}

func entitySnapshot(conn *gorm.DB) (string, error) {
	rows := map[string]any{
		"channels": &[]model.Channel{}, "channel_keys": &[]model.ChannelKey{},
		"channel_models": &[]model.ChannelModel{}, "channel_grants": &[]model.ChannelGrant{},
		"group_categories": &[]model.GroupCategory{}, "groups": &[]model.Group{},
		"group_items": &[]model.GroupItem{}, "api_keys": &[]model.APIKey{},
		"webhook_endpoints": &[]model.WebhookEndpoint{},
	}
	for table, target := range rows {
		if err := conn.Table(table).Order("id").Find(target).Error; err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func (s *suite) makeFixture() error {
	if s.opts.dialect != "sqlite" {
		return errors.New("fixture export requires new SQLite")
	}
	defer db.Close()
	return s.step("synthetic-fixture-export", func() (any, error) {
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		detail := model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: fixtureChannel, Dialect: model.DialectGeneric, BaseURL: "http://127.0.0.1:1", Enabled: true}, Models: []string{fixtureModel, unusedModel}}
		for _, name := range []string{"synthetic-key-a", "synthetic-key-b", "synthetic-key-c"} {
			detail.Keys = append(detail.Keys, model.ChannelKeyConfig{Name: name, Key: "synthetic-value-" + name, Enabled: true})
			detail.Grants = append(detail.Grants, model.ChannelGrantConfig{ModelName: fixtureModel, KeyName: name, Protocols: model.ProtocolOpenAIChatCompletion})
		}
		channel, err := op.ChannelCreate(&detail, s.ctx)
		if err != nil || channel.ID != 1 {
			return nil, errors.New("synthetic channel creation failed")
		}
		category, err := op.GroupCategoryCreate("Synthetic repro category", s.ctx)
		if err != nil {
			return nil, err
		}
		if _, err := op.GroupCreate(&model.GroupCreateRequest{Name: fixtureModel, CategoryID: &category.ID, Mode: model.GroupModeManual, ModelMatchMode: model.GroupModelMatchModeExact, Items: []model.GroupItemInput{{ChannelGrantID: 1}, {ChannelGrantID: 2}}}, s.ctx); err != nil {
			return nil, err
		}
		if err := db.GetDB().Create(&model.APIKey{Name: "synthetic-repro-api-key", APIKey: "synthetic-value", Enabled: true}).Error; err != nil {
			return nil, err
		}
		if err := db.GetDB().Create(&model.WebhookEndpoint{Name: "synthetic-repro-hook", URL: "http://127.0.0.1:1", Headers: map[string]string{}, BodyTemplate: "{}"}).Error; err != nil {
			return nil, err
		}
		dump, err := op.DBExportAll(s.ctx)
		if err != nil {
			return nil, err
		}
		raw, err := op.EncryptBackup(dump, fixturePassword)
		if err != nil {
			return nil, err
		}
		if err := writeExclusive(s.opts.backupPath, raw); err != nil {
			return nil, err
		}
		s.report.BackupSHA256 = digest(raw)
		return map[string]string{"backup_sha256": s.report.BackupSHA256}, nil
	})
}

func (s *suite) verifyCommon(dsn string, dump *model.DBDump) error {
	defer db.Close()
	if err := s.step("initialize-with-migration-16", func() (any, error) { return nil, s.boot(dsn) }); err != nil {
		return err
	}
	if err := s.step("import-entities", func() (any, error) {
		result, err := op.DBImportIncremental(s.ctx, cloneDump(dump))
		if err != nil {
			return nil, err
		}
		return result.RowsAffected, nil
	}); err != nil {
		return err
	}
	if err := s.step("reload-cache-after-import", func() (any, error) { return nil, op.InitCache() }); err != nil {
		return err
	}
	if err := s.step("restored-entity-graph", func() (any, error) { return nil, s.verifyGraph(dump) }); err != nil {
		return err
	}
	if err := s.step("first-channel-save-auto-add", func() (any, error) { return nil, s.saveAutoAdd(dump) }); err != nil {
		return err
	}
	if err := s.step("committed-new-entities", s.createEntities); err != nil {
		return err
	}
	if err := s.step("repeat-import-preserves-content", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
			return nil, err
		}
		if err := op.InitCache(); err != nil {
			return nil, err
		}
		after, err := snapshot()
		if err != nil {
			return nil, err
		}
		if before != after {
			return nil, errors.New("repeat import changed persistent content")
		}
		return map[string]string{"before": before, "after": after}, nil
	}); err != nil {
		return err
	}
	if err := s.step("late-write-full-rollback", func() (any, error) { return s.verifyRollback(dump) }); err != nil {
		return err
	}
	if err := s.step("category-merge-and-reserved-ids", func() (any, error) { return nil, s.verifyCategories(dump) }); err != nil {
		return err
	}
	return s.step("reconnect-preserves-content", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
		if err := s.boot(dsn); err != nil {
			return nil, err
		}
		after, err := snapshot()
		if err != nil || before != after {
			return nil, errors.New("database reconnect changed persistent content")
		}
		var record migrate.MigrationRecord
		if err := db.GetDB().First(&record, "version = ?", 16).Error; err != nil || record.Status != migrate.MigrationRecordStatusSuccess {
			return nil, errors.New("migration 16 not recorded as successful")
		}
		return map[string]string{"before": before, "after": after}, nil
	})
}

func (s *suite) verifyGraph(dump *model.DBDump) error {
	for _, table := range []struct {
		name string
		rows any
	}{
		{"channels", dump.Channels}, {"channel_keys", dump.ChannelKeys}, {"channel_models", dump.ChannelModels},
		{"channel_grants", dump.ChannelGrants}, {"groups", dump.Groups}, {"group_items", dump.GroupItems},
		{"api_keys", dump.APIKeys}, {"webhook_endpoints", dump.WebhookEndpoints},
	} {
		rows := reflect.ValueOf(table.rows)
		var count int64
		if err := db.GetDB().Table(table.name).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(rows.Len()) {
			return &assertionFailure{fmt.Sprintf("restored %s count = %d, want %d", table.name, count, rows.Len())}
		}
		for i := 0; i < rows.Len(); i++ {
			id := rows.Index(i).FieldByName("ID").Int()
			var found int64
			if err := db.GetDB().Table(table.name).Where("id = ?", id).Count(&found).Error; err != nil {
				return err
			}
			if found != 1 {
				return &assertionFailure{fmt.Sprintf("restored %s ID %d is missing", table.name, id)}
			}
		}
	}
	for _, key := range dump.ChannelKeys {
		var got model.ChannelKey
		if err := db.GetDB().First(&got, key.ID).Error; err != nil || got.ChannelID != key.ChannelID || got.Name != key.Name || got.Key != key.Key || got.Enabled != key.Enabled {
			return &assertionFailure{"restored key content or ownership differs"}
		}
	}
	for _, grant := range dump.ChannelGrants {
		var got model.ChannelGrant
		if err := db.GetDB().First(&got, grant.ID).Error; err != nil || got.ChannelKeyID != grant.ChannelKeyID || got.ChannelModelID != grant.ChannelModelID || got.Protocols != grant.Protocols {
			return &assertionFailure{"restored grant association differs"}
		}
	}
	for _, item := range dump.GroupItems {
		var got model.GroupItem
		if err := db.GetDB().First(&got, item.ID).Error; err != nil || got.GroupID != item.GroupID || got.ChannelGrantID != item.ChannelGrantID || got.Priority != item.Priority {
			return &assertionFailure{"restored group membership differs"}
		}
	}
	return nil
}

func (s *suite) saveAutoAdd(dump *model.DBDump) error {
	id := dump.Channels[0].ID
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		return err
	}
	detail.MaxConcurrency = 7
	if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{AutoAddGroups: true}, s.ctx); err != nil {
		return err
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, id).Error; err != nil || channel.MaxConcurrency != 7 {
		return errors.New("first save did not persist channel")
	}
	cached, err := op.ChannelDetailGet(id)
	if err != nil || cached.MaxConcurrency != 7 {
		return errors.New("first save did not publish channel cache")
	}
	var count int64
	if err := db.GetDB().Model(&model.GroupItem{}).Where("group_id = ?", dump.Groups[0].ID).Count(&count).Error; err != nil || count != 3 {
		return errors.New("first save did not append exactly one member")
	}
	if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{AutoAddGroups: true}, s.ctx); err != nil {
		return err
	}
	if err := db.GetDB().Model(&model.GroupItem{}).Where("group_id = ?", dump.Groups[0].ID).Count(&count).Error; err != nil || count != 3 {
		return errors.New("repeated save duplicated members")
	}
	return nil
}

func (s *suite) createEntities() (any, error) {
	before, err := maxima(db.GetDB())
	if err != nil {
		return nil, err
	}
	detail := model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "b01-created-channel", Dialect: model.DialectGeneric, BaseURL: "http://127.0.0.1:1", Enabled: true}, Keys: []model.ChannelKeyConfig{{Name: "b01-created-key", Key: "synthetic-created-value", Enabled: true}}, Models: []string{"b01-created-model"}, Grants: []model.ChannelGrantConfig{{ModelName: "b01-created-model", KeyName: "b01-created-key", Protocols: model.ProtocolOpenAIChatCompletion}}}
	channel, err := op.ChannelCreate(&detail, s.ctx)
	if err != nil {
		return nil, err
	}
	var key model.ChannelKey
	var cm model.ChannelModel
	var grant model.ChannelGrant
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&key).Error; err != nil {
		return nil, err
	}
	if err := db.GetDB().Where("channel_id = ?", channel.ID).First(&cm).Error; err != nil {
		return nil, err
	}
	if err := db.GetDB().Where("channel_key_id = ? AND channel_model_id = ?", key.ID, cm.ID).First(&grant).Error; err != nil {
		return nil, err
	}
	category, err := op.GroupCategoryCreate("B01 created category", s.ctx)
	if err != nil {
		return nil, err
	}
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "b01-created-group", CategoryID: &category.ID, Mode: model.GroupModeManual, Items: []model.GroupItemInput{{ChannelGrantID: grant.ID}}}, s.ctx)
	if err != nil {
		return nil, err
	}
	apiKey := model.APIKey{Name: "b01-created-api-key", APIKey: "synthetic-created-value", Enabled: true}
	hook := model.WebhookEndpoint{Name: "b01-created-hook", URL: "http://127.0.0.1:1", Headers: map[string]string{}, BodyTemplate: "{}"}
	if err := db.GetDB().Create(&apiKey).Error; err != nil {
		return nil, err
	}
	if err := db.GetDB().Create(&hook).Error; err != nil {
		return nil, err
	}
	if len(group.Items) != 1 {
		return nil, errors.New("new group did not persist its member")
	}
	ids := map[string]int{"channels": channel.ID, "channel_keys": key.ID, "channel_models": cm.ID, "channel_grants": grant.ID, "group_categories": category.ID, "groups": group.ID, "group_items": group.Items[0].ID, "api_keys": apiKey.ID, "webhook_endpoints": hook.ID}
	for table, id := range ids {
		if int64(id) <= before[table] {
			return nil, fmt.Errorf("new %s ID did not advance", table)
		}
		var count int64
		if err := db.GetDB().Table(table).Where("id = ?", id).Count(&count).Error; err != nil || count != 1 {
			return nil, fmt.Errorf("new %s row did not commit", table)
		}
	}
	return ids, nil
}

func (s *suite) verifyRollback(dump *model.DBDump) (any, error) {
	before, err := snapshot()
	if err != nil {
		return nil, err
	}
	cached, err := op.ChannelDetailGet(dump.Channels[0].ID)
	if err != nil {
		return nil, err
	}
	bad := sparseDump(dump, 100000)
	bad.Channels[0].Name = "b01-rollback-channel"
	bad.Groups[0].Name = "b01-rollback-group"
	bad.GroupCategories[0] = model.BackupGroupCategory{ID: dump.GroupCategories[0].ID, Name: "B01 rollback category"}
	bad.Groups[0].CategoryID = &bad.GroupCategories[0].ID
	// C08 has its own input-graph rejection case. Keep this a real SQL failure
	// after earlier entity writes, so prevalidation cannot erase rollback coverage.
	const callback = "backup_acceptance:late_write_failure"
	reached := false
	priorWrites := int64(0)
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "webhook_endpoints" {
			return
		}
		reached = true
		check := tx.Session(&gorm.Session{NewDB: true}).Model(&model.GroupItem{}).Where("id = ?", bad.GroupItems[0].ID).Count(&priorWrites)
		if check.Error != nil {
			tx.AddError(check.Error)
			return
		}
		tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO b03_missing_fault_table (id) VALUES (1)").Error)
	}); err != nil {
		return nil, err
	}
	defer db.GetDB().Callback().Create().Remove(callback)
	published := false
	if _, err := op.DBImportIncrementalAndPublish(s.ctx, bad, func() { published = true }); !errors.Is(err, op.ErrBackupWrite) || !reached || priorWrites != 1 || published {
		return nil, errors.New("late SQL failure did not roll back after earlier entity writes")
	}
	after, err := snapshot()
	if err != nil {
		return nil, err
	}
	afterCached, err := op.ChannelDetailGet(dump.Channels[0].ID)
	if err != nil || before != after || !reflect.DeepEqual(cached, afterCached) {
		return nil, errors.New("late import failure changed database or cache")
	}
	return map[string]any{"before": before, "after": after, "late_sql_reached": reached, "prior_member_write_observed": priorWrites == 1, "success_publication": published, "sequence_side_effects": "number gaps are allowed; business rows and cache must roll back"}, nil
}

func (s *suite) verifyCategories(dump *model.DBDump) error {
	base := dump.GroupCategories[0]
	var highest int
	if err := db.GetDB().Model(&model.GroupCategory{}).Select("MAX(id)").Scan(&highest).Error; err != nil {
		return err
	}
	reserved := highest + 10000
	mergeID := highest + 20000
	groupBase := base.ID + 300000
	input := &model.DBDump{Product: "octopus-roam", Version: 1003, GroupCategories: []model.BackupGroupCategory{{ID: base.ID, Name: "B01 collision category"}, {ID: reserved, Name: "B01 reserved category"}, {ID: mergeID, Name: strings.ToUpper(base.Name)}}}
	for i, ref := range []int{base.ID, reserved, mergeID} {
		id := ref
		input.Groups = append(input.Groups, model.Group{ID: groupBase + i, Name: fmt.Sprintf("b01-category-group-%d", i), CategoryID: &id, Mode: model.GroupModeManual})
	}
	if _, err := op.DBImportIncremental(s.ctx, input); err != nil {
		return err
	}
	if err := op.InitCache(); err != nil {
		return err
	}
	var original model.GroupCategory
	if err := db.GetDB().First(&original, base.ID).Error; err != nil || original.Name != base.Name {
		return errors.New("category merge overwrote local name")
	}
	var groups []model.Group
	if err := db.GetDB().Where("id IN ?", []int{groupBase, groupBase + 1, groupBase + 2}).Order("id").Find(&groups).Error; err != nil || len(groups) != 3 {
		return errors.New("category groups not imported")
	}
	if groups[0].CategoryID == nil || *groups[0].CategoryID <= reserved || groups[1].CategoryID == nil || *groups[1].CategoryID != reserved || groups[2].CategoryID == nil || *groups[2].CategoryID != base.ID {
		return errors.New("category collision/reservation mapping differs")
	}
	return nil
}
