package migrate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func migrationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.ToSlash(filepath.Join(t.TempDir(), "migration.db"))+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func migrationExec(t *testing.T, db *gorm.DB, query string, args ...any) {
	t.Helper()
	if err := db.Exec(query, args...).Error; err != nil {
		t.Fatal(err)
	}
}

func TestMigration5AcceptsCurrentSchemaWithoutChannelKey(t *testing.T) {
	for _, pragma := range []string{"OFF", "ON"} {
		t.Run("case_sensitive_like_"+pragma, func(t *testing.T) {
			db := migrationTestDB(t)
			migrationExec(t, db, "PRAGMA case_sensitive_like = "+pragma)
			if err := db.AutoMigrate(&model.Channel{}, &model.ChannelGrant{}, &model.Group{}, &model.GroupItem{}); err != nil {
				t.Fatal(err)
			}
			migrationExec(t, db, `CREATE TABLE unrelated_keys (id integer PRIMARY KEY, "key" text)`)
			migrationExec(t, db, `CREATE INDEX channel_key_index ON channels (name)`)
			if hasPhysicalColumn(db, "channels", "key") {
				t.Fatal("fixture unexpectedly has channels.key")
			}
			channel := model.Channel{ID: 7, ChannelConfig: model.ChannelConfig{Name: "synthetic", BaseURL: "https://upstream.example.test"}}
			key := model.ChannelKey{ID: 17, ChannelID: 7, ChannelKeyConfig: model.ChannelKeyConfig{Name: "kept", Key: "synthetic-current-key", Enabled: true}}
			channelModel := model.ChannelModel{ID: 23, ChannelID: 7, Name: "synthetic-model"}
			grant := model.ChannelGrant{ID: 31, ChannelModelID: 23, ChannelKeyID: 17, Protocols: model.ProtocolAll}
			group := model.Group{ID: 41, Name: "synthetic-group", ActiveItemID: 47}
			item := model.GroupItem{ID: 47, GroupID: 41, ChannelGrantID: 31, Priority: 4}
			for _, row := range []any{&channel, &key, &channelModel, &grant, &group, &item} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := migrateChannelToSingleURLAndKey(db); err != nil {
				t.Fatal(err)
			}
			var gotKey model.ChannelKey
			if err := db.First(&gotKey, 17).Error; err != nil || gotKey.Key != key.Key {
				t.Fatalf("current credential changed: err=%v", err)
			}
			var gotItem model.GroupItem
			if err := db.First(&gotItem, 47).Error; err != nil || gotItem.ChannelGrantID != 31 || gotItem.Priority != 4 {
				t.Fatalf("current group item changed: %+v, err=%v", gotItem, err)
			}
			assertMigrationForeignKeys(t, db)
		})
	}
}

func TestMigration5LegacyAddressAndKeySelection(t *testing.T) {
	for _, hasTargets := range []bool{false, true} {
		name := "original_multi_value_schema"
		if hasTargets {
			name = "partially_converted_schema"
		}
		t.Run(name, func(t *testing.T) {
			db := migrationTestDB(t)
			targets := ""
			if hasTargets {
				targets = `, base_url text, "key" text`
			}
			migrationExec(t, db, `CREATE TABLE channels (id integer PRIMARY KEY, name text, type text, base_urls text`+targets+`)`)
			migrationExec(t, db, `CREATE TABLE channel_keys (id integer PRIMARY KEY, channel_id integer, channel_key text, FOREIGN KEY(channel_id) REFERENCES channels(id) ON DELETE CASCADE)`)
			migrationExec(t, db, `INSERT INTO channels (id, name, type, base_urls) VALUES (7, 'first', 'openai', ?), (8, 'second', 'anthropic', ?), (9, 'empty-first-url', 'openai', ?)`,
				`[{"url":"https://first.example.test"},{"url":"https://second.example.test"}]`, `[{"url":"https://other.example.test"}]`, `[{"url":" "},{"url":"https://unused.example.test"}]`)
			migrationExec(t, db, `INSERT INTO channel_keys (id, channel_id, channel_key) VALUES (13, 7, 'synthetic-last'), (12, 7, 'synthetic-first'), (11, 7, ' '), (21, 8, 'synthetic-other')`)
			if hasTargets {
				migrationExec(t, db, `UPDATE channels SET base_url = 'https://kept.example.test', "key" = 'synthetic-kept' WHERE id = 8`)
			}
			if err := migrateChannelToSingleURLAndKey(db); err != nil {
				t.Fatal(err)
			}
			type channelValue struct {
				ID      int
				BaseURL string
				Key     string
			}
			var rows []channelValue
			if err := db.Table("channels").Order("id").Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			wantOtherURL, wantOtherKey := "https://other.example.test", "synthetic-other"
			if hasTargets {
				wantOtherURL, wantOtherKey = "https://kept.example.test", "synthetic-kept"
			}
			if len(rows) != 3 || rows[0] != (channelValue{7, "https://first.example.test", "synthetic-first"}) ||
				rows[1] != (channelValue{8, wantOtherURL, wantOtherKey}) || rows[2] != (channelValue{ID: 9}) {
				t.Fatalf("legacy selection or channel IDs changed: %+v", rows)
			}
			if hasPhysicalColumn(db, "channels", "base_urls") || db.Migrator().HasTable("channel_keys") {
				t.Fatal("obsolete multi-value schema remains")
			}
			// A retry after DDL completed must leave the selected values intact.
			if err := migrateChannelToSingleURLAndKey(db); err != nil {
				t.Fatal(err)
			}
			assertMigrationForeignKeys(t, db)
		})
	}
}

func TestMigration5RejectsIncompleteSchemaBeforeWrites(t *testing.T) {
	tests := []struct {
		name       string
		channels   string
		keys       string
		insertKeys string
	}{
		{"missing_single_key", `id integer PRIMARY KEY, name text, type text, base_url text`, "", ""},
		{"missing_single_url", `id integer PRIMARY KEY, name text, type text, "key" text`, "", ""},
		{"missing_multi_keys", `id integer PRIMARY KEY, name text, type text, base_urls text`, "", ""},
		{"missing_current_signature", `id integer PRIMARY KEY, name text, base_url text`, "", ""},
		{"unknown_key_table", `id integer PRIMARY KEY, name text, type text, base_url text, "key" text, base_urls text`, `id integer PRIMARY KEY, value text`, `INSERT INTO channel_keys VALUES (1, 'synthetic-keep')`},
		{"missing_legacy_key_id", `id integer PRIMARY KEY, name text, type text, base_url text, "key" text, base_urls text`, `channel_id integer, channel_key text`, `INSERT INTO channel_keys VALUES (1, 'synthetic-keep')`},
		{"mixed_key_table", `id integer PRIMARY KEY, name text, type text, base_url text, "key" text, base_urls text`, `id integer PRIMARY KEY, channel_id integer, channel_key text, "key" text, name text, enabled numeric`, `INSERT INTO channel_keys VALUES (1, 1, 'synthetic-old', 'synthetic-current', 'default', 1)`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := migrationTestDB(t)
			migrationExec(t, db, "CREATE TABLE channels ("+tc.channels+")")
			migrationExec(t, db, "INSERT INTO channels (id, name) VALUES (1, 'synthetic')")
			if hasPhysicalColumn(db, "channels", "base_urls") {
				migrationExec(t, db, `UPDATE channels SET base_urls = ?`, `[{"url":"https://must-not-copy.example.test"}]`)
			}
			if tc.keys != "" {
				migrationExec(t, db, "CREATE TABLE channel_keys ("+tc.keys+")")
				migrationExec(t, db, tc.insertKeys)
			}
			err := runMigrationsWithRecord(db, []Migration{{Version: 5, Up: migrateChannelToSingleURLAndKey}})
			if err == nil || !strings.Contains(err.Error(), "incomplete channel migration schema") {
				t.Fatalf("expected stable schema error, got %v", err)
			}
			var record MigrationRecord
			if err := db.First(&record, "version = ?", 5).Error; err != nil || record.Status != MigrationRecordStatusFailed {
				t.Fatalf("failure was not recorded: %+v, err=%v", record, err)
			}
			if hasPhysicalColumn(db, "channels", "base_url") {
				var copied int64
				if err := db.Table("channels").Where("base_url IS NOT NULL AND base_url <> ''").Count(&copied).Error; err != nil || copied != 0 {
					t.Fatalf("validation wrote an address: count=%d, err=%v", copied, err)
				}
			}
			if tc.keys != "" {
				var count int64
				if err := db.Table("channel_keys").Count(&count).Error; err != nil || count != 1 {
					t.Fatalf("validation deleted old keys: count=%d, err=%v", count, err)
				}
			}
		})
	}
}

func TestChannelMigrationsPropagateMetadataErrors(t *testing.T) {
	for _, migration := range []struct {
		name string
		up   func(*gorm.DB) error
	}{{"migration5", migrateChannelToSingleURLAndKey}, {"migration11", migrateChannelGrants}} {
		for _, columnsOnly := range []bool{false, true} {
			name := migration.name + "/table_lookup"
			if columnsOnly {
				name = migration.name + "/column_lookup"
			}
			t.Run(name, func(t *testing.T) {
				db := migrationTestDB(t)
				migrationExec(t, db, `CREATE TABLE channels (id integer PRIMARY KEY, type text, base_url text, "key" text)`)
				sentinel := errors.New("synthetic metadata failure")
				pool := metadataFailurePool{ConnPool: db.ConnPool, failure: sentinel, columnsOnly: columnsOnly}
				db.ConnPool = pool
				db.Statement.ConnPool = pool
				if err := migration.up(db); !errors.Is(err, sentinel) {
					t.Fatalf("metadata error was swallowed: %v", err)
				}
			})
		}
	}
}

type metadataFailurePool struct {
	gorm.ConnPool
	failure     error
	columnsOnly bool
}

func (p metadataFailurePool) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "sqlite_master") && (!p.columnsOnly || strings.Contains(query, "SELECT sql")) {
		return nil, p.failure
	}
	return p.ConnPool.QueryContext(ctx, query, args...)
}

func TestMigration5RejectsOrphanKeyTable(t *testing.T) {
	db := migrationTestDB(t)
	migrationExec(t, db, `CREATE TABLE channel_keys (id integer PRIMARY KEY, channel_id integer, channel_key text)`)
	if err := migrateChannelToSingleURLAndKey(db); err == nil || !strings.Contains(err.Error(), "incomplete channel migration schema") {
		t.Fatalf("orphan key table accepted as a new database: %v", err)
	}
}

func TestMigration11DoesNotResetAlreadyRewiredGroupItems(t *testing.T) {
	db := migrationTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.ChannelGrant{}, &model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`ALTER TABLE channels ADD COLUMN type text`,
		`ALTER TABLE channels ADD COLUMN "key" text`,
		`INSERT INTO channels (id, name, type, "key") VALUES (7, 'synthetic', 'openai', 'synthetic-source')`,
		`INSERT INTO channel_keys (id, channel_id, name, "key") VALUES (17, 7, 'kept', 'synthetic-keep')`,
		`INSERT INTO channel_models (id, channel_id, name) VALUES (23, 7, 'synthetic-model')`,
		`INSERT INTO channel_grants (id, channel_model_id, channel_key_id, protocols) VALUES (31, 23, 17, 2)`,
		`INSERT INTO groups (id, name, active_item_id) VALUES (41, 'synthetic-group', 47)`,
		`INSERT INTO group_items (id, group_id, channel_grant_id, priority) VALUES (47, 41, 31, 6)`,
	} {
		migrationExec(t, db, query)
	}
	if err := migrateChannelGrants(db); err == nil || !strings.Contains(err.Error(), "incomplete channel migration schema") {
		t.Fatalf("partially replaced migration source accepted: %v", err)
	}
	var item model.GroupItem
	if err := db.First(&item, 47).Error; err != nil || item.ChannelGrantID != 31 || item.Priority != 6 {
		t.Fatalf("retry destroyed existing group items: %+v, err=%v", item, err)
	}
	assertMigrationForeignKeys(t, db)
}

func TestMigration11RejectsMissingSourceBeforeReset(t *testing.T) {
	db := migrationTestDB(t)
	if err := db.AutoMigrate(&model.Channel{}, &model.ChannelGrant{}); err != nil {
		t.Fatal(err)
	}
	migrationExec(t, db, `ALTER TABLE channels ADD COLUMN type text`)
	migrationExec(t, db, `INSERT INTO channels (id, name, type) VALUES (7, 'synthetic', 'openai')`)
	migrationExec(t, db, `INSERT INTO channel_keys (id, channel_id, name, "key") VALUES (17, 7, 'kept', 'synthetic-keep')`)
	err := migrateChannelGrants(db)
	if err == nil || !strings.Contains(err.Error(), "incomplete channel migration schema") {
		t.Fatalf("missing source credential accepted: %v", err)
	}
	var row model.ChannelKey
	if err := db.First(&row, 17).Error; err != nil || row.Key != "synthetic-keep" {
		t.Fatalf("migration 11 reset credentials before source validation: err=%v", err)
	}
}

func assertMigrationForeignKeys(t *testing.T, db *gorm.DB) {
	t.Helper()
	var enabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil || enabled != 1 {
		t.Fatalf("foreign keys were disabled: enabled=%d, err=%v", enabled, err)
	}
	var violations []struct{ Table string }
	if err := db.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil || len(violations) != 0 {
		t.Fatalf("broken foreign keys: %+v, err=%v", violations, err)
	}
}
