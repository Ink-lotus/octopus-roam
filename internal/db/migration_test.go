package db

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestInitDBRunsMigrationsForEveryDatabase(t *testing.T) {
	for _, name := range []string{"first.db", "second.db"} {
		t.Run(name, func(t *testing.T) {
			if err := InitDB("sqlite", filepath.Join(t.TempDir(), name), false); err != nil {
				t.Fatal(err)
			}
			defer Close()
			var records []migrate.MigrationRecord
			if err := GetDB().Order("version").Find(&records).Error; err != nil {
				t.Fatal(err)
			}
			if len(records) != 11 {
				t.Fatalf("got %d migration records, want all versions 3 through 13", len(records))
			}
			for i, record := range records {
				if record.Version != i+3 || record.Status != migrate.MigrationRecordStatusSuccess {
					t.Fatalf("unexpected migration record: %+v", record)
				}
			}
			var columns []struct{ Name string }
			if err := GetDB().Raw("PRAGMA table_info(channels)").Scan(&columns).Error; err != nil {
				t.Fatal(err)
			}
			for _, column := range columns {
				if column.Name == "key" || column.Name == "type" {
					t.Fatalf("new database has obsolete channel column %q", column.Name)
				}
			}
		})
	}
}

func TestInitDBPreservesLegacyChannelRelationships(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	fixture, err := gorm.Open(sqlite.Open(filepath.ToSlash(path)+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := fixture.DB()
	sqlDB.SetMaxOpenConns(1)
	// Historical multi-value channels have neither single-value target column.
	// Existing models and group item IDs also exercise upgrades with migration 8 data.
	for _, query := range []string{
		`CREATE TABLE channels (id integer PRIMARY KEY AUTOINCREMENT, name text NOT NULL UNIQUE, type text, enabled numeric DEFAULT true, base_urls text, auto_sync numeric DEFAULT false)`,
		`CREATE TABLE channel_keys (id integer PRIMARY KEY AUTOINCREMENT, channel_id integer, channel_key text, CONSTRAINT fk_channels_keys FOREIGN KEY(channel_id) REFERENCES channels(id) ON DELETE CASCADE)`,
		`CREATE TABLE channel_models (id integer PRIMARY KEY AUTOINCREMENT, channel_id integer NOT NULL, name text NOT NULL, source text NOT NULL DEFAULT 'auto', input_token integer, output_token integer, input_cost real, output_cost real, wait_time integer, request_success integer, request_failed integer, CONSTRAINT fk_channels_models FOREIGN KEY(channel_id) REFERENCES channels(id) ON DELETE CASCADE)`,
		`CREATE UNIQUE INDEX idx_channel_model_name ON channel_models(channel_id, name)`,
		`CREATE TABLE stats_channels (channel_id integer PRIMARY KEY, input_token integer, output_token integer, input_cost real, output_cost real, wait_time integer, request_success integer, request_failed integer)`,
		`CREATE TABLE groups (id integer PRIMARY KEY AUTOINCREMENT, name text NOT NULL UNIQUE, mode text NOT NULL DEFAULT 'manual', active_item_id integer NOT NULL DEFAULT 0, relay_config text)`,
		`CREATE TABLE group_items (id integer PRIMARY KEY AUTOINCREMENT, group_id integer NOT NULL, channel_model_id integer NOT NULL, priority integer NOT NULL, CONSTRAINT fk_groups_items FOREIGN KEY(group_id) REFERENCES groups(id) ON DELETE CASCADE, CONSTRAINT fk_group_items_channel_model FOREIGN KEY(channel_model_id) REFERENCES channel_models(id) ON DELETE CASCADE)`,
		`CREATE UNIQUE INDEX idx_group_channel_model ON group_items(group_id, channel_model_id)`,
		`INSERT INTO channels (id, name, type, base_urls) VALUES (7, 'synthetic-old', 'anthropic', '[{"url":"https://first.example.test"},{"url":"https://unused.example.test"}]')`,
		`INSERT INTO channel_keys (id, channel_id, channel_key) VALUES (19, 7, 'synthetic-last'), (17, 7, 'synthetic-first')`,
		`INSERT INTO channel_models (id, channel_id, name, source, input_token, request_success) VALUES (23, 7, 'synthetic-model', 'manual', 123, 4)`,
		`INSERT INTO stats_channels (channel_id, input_token, output_token, input_cost, output_cost, wait_time, request_success, request_failed) VALUES (7, 345, 67, 1.25, 0.5, 8, 6, 2)`,
		`INSERT INTO groups (id, name, active_item_id) VALUES (41, 'synthetic-group', 47)`,
		`INSERT INTO group_items (id, group_id, channel_model_id, priority) VALUES (47, 41, 23, 6)`,
	} {
		if err := fixture.Exec(query).Error; err != nil {
			_ = sqlDB.Close()
			t.Fatal(err)
		}
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB("sqlite", path, false); err != nil {
		_ = Close()
		t.Fatal(err)
	}
	defer Close()
	conn := GetDB()
	var channel model.Channel
	if err := conn.First(&channel, 7).Error; err != nil || channel.BaseURL != "https://first.example.test" ||
		channel.InputToken != 345 || channel.OutputToken != 67 || channel.InputCost != 1.25 || channel.OutputCost != 0.5 ||
		channel.WaitTime != 8 || channel.RequestSuccess != 6 || channel.RequestFailed != 2 {
		t.Fatalf("channel address, ID or historical statistics changed: err=%v", err)
	}
	var keys []model.ChannelKey
	if err := conn.Where("channel_id = ?", 7).Find(&keys).Error; err != nil || len(keys) != 1 || keys[0].Key != "synthetic-first" {
		t.Fatalf("selected legacy credential lost: count=%d, err=%v", len(keys), err)
	}
	var channelModel model.ChannelModel
	if err := conn.First(&channelModel, 23).Error; err != nil || channelModel.Name != "synthetic-model" || channelModel.InputToken != 123 || channelModel.RequestSuccess != 4 {
		t.Fatalf("model ID or statistics changed: %+v, err=%v", channelModel, err)
	}
	var grants []model.ChannelGrant
	if err := conn.Find(&grants).Error; err != nil || len(grants) != 1 || grants[0].ChannelModelID != 23 || grants[0].ChannelKeyID != keys[0].ID || grants[0].Protocols != model.ProtocolAnthropicMessage {
		t.Fatalf("grant relationships or protocol lost: %+v, err=%v", grants, err)
	}
	var group model.Group
	if err := conn.First(&group, 41).Error; err != nil || group.ActiveItemID != 47 {
		t.Fatalf("active group item changed: %+v, err=%v", group, err)
	}
	var item model.GroupItem
	if err := conn.First(&item, 47).Error; err != nil || item.Priority != 6 || item.ChannelGrantID != grants[0].ID {
		t.Fatalf("group item ID, order or grant lost: %+v, err=%v", item, err)
	}
	var columns []struct{ Name string }
	if err := conn.Raw("PRAGMA table_info(channels)").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if column.Name == "base_urls" || column.Name == "key" || column.Name == "type" || column.Name == "auto_sync" {
			t.Fatalf("obsolete channel column remains: %s", column.Name)
		}
	}
	var violations []struct{ Table string }
	if err := conn.Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil || len(violations) != 0 {
		t.Fatalf("foreign key violations: %+v, err=%v", violations, err)
	}
	var enabled int
	if err := conn.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil || enabled != 1 {
		t.Fatalf("foreign keys disabled: enabled=%d, err=%v", enabled, err)
	}
	for _, version := range []int{5, 8, 11} {
		var record migrate.MigrationRecord
		if err := conn.First(&record, "version = ?", version).Error; err != nil || record.Status != migrate.MigrationRecordStatusSuccess {
			t.Fatalf("legacy upgrade was not recorded: %+v, err=%v", record, err)
		}
	}
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if err := InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	var restartedKey model.ChannelKey
	if err := GetDB().First(&restartedKey, keys[0].ID).Error; err != nil || restartedKey.Key != keys[0].Key {
		t.Fatalf("restart reran credential conversion: err=%v", err)
	}
	var restartedItem model.GroupItem
	if err := GetDB().First(&restartedItem, 47).Error; err != nil || restartedItem.ChannelGrantID != grants[0].ID || restartedItem.Priority != 6 {
		t.Fatalf("restart changed group membership: %+v, err=%v", restartedItem, err)
	}
}

func TestInitDBUpgradesRecordedSingleValueSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single-value.db")
	fixture, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := fixture.DB()
	for _, query := range []string{
		`CREATE TABLE channels (id integer PRIMARY KEY AUTOINCREMENT, name text NOT NULL UNIQUE, type text, base_url text, "key" text)`,
		`INSERT INTO channels (id, name, type, base_url, "key") VALUES (7, 'synthetic-single', 'openai_responses', 'https://single.example.test', 'synthetic-single-key')`,
		`CREATE TABLE migration_records (version integer PRIMARY KEY, status integer)`,
		`INSERT INTO migration_records (version, status) VALUES (5, 1)`,
	} {
		if err := fixture.Exec(query).Error; err != nil {
			_ = sqlDB.Close()
			t.Fatal(err)
		}
	}
	_ = sqlDB.Close()
	if err := InitDB("sqlite", path, false); err != nil {
		_ = Close()
		t.Fatal(err)
	}
	defer Close()
	var channel model.Channel
	if err := GetDB().First(&channel, 7).Error; err != nil || channel.BaseURL != "https://single.example.test" {
		t.Fatalf("single-value channel changed: err=%v", err)
	}
	var keys []model.ChannelKey
	if err := GetDB().Where("channel_id = ?", 7).Find(&keys).Error; err != nil || len(keys) != 1 || keys[0].Key != "synthetic-single-key" {
		t.Fatalf("single-value credential changed: count=%d, err=%v", len(keys), err)
	}
}

func TestInitDBRejectsIncompleteLegacyBeforeAutoMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incomplete.db")
	fixture, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := fixture.DB()
	if err := fixture.Exec(`CREATE TABLE channels (id integer PRIMARY KEY, name text, type text, base_url text)`).Error; err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	err = InitDB("sqlite", path, false)
	defer Close()
	if err == nil || !strings.Contains(err.Error(), "incomplete channel migration schema") {
		t.Fatalf("incomplete legacy schema was accepted: %v", err)
	}
	var record migrate.MigrationRecord
	if err := GetDB().First(&record, "version = ?", 5).Error; err != nil || record.Status != migrate.MigrationRecordStatusFailed {
		t.Fatalf("migration 5 failure not recorded: %+v, err=%v", record, err)
	}
	if GetDB().Migrator().HasTable("channel_keys") {
		t.Fatal("incomplete source reached migration 11 or AutoMigrate")
	}
}
