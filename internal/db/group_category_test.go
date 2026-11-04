package db

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 与 W07 提交的 Group 声明一致，以原有 GORM 标签生成真实升级前结构。
type groupBeforeCategories struct {
	ID             int                       `gorm:"primaryKey"`
	Name           string                    `gorm:"unique;not null"`
	Mode           model.GroupMode           `gorm:"not null;default:manual"`
	ModelMatchMode model.GroupModelMatchMode `gorm:"not null;default:keyword"`
	ActiveItemID   int                       `gorm:"not null;default:0"`
	RelayConfig    model.GroupRelayConfig    `gorm:"serializer:json"`
	Items          []model.GroupItem         `gorm:"foreignKey:GroupID;constraint:OnDelete:CASCADE"`
}

func (groupBeforeCategories) TableName() string { return "groups" }

func TestGroupCategoryUpgradeAndRepeatedStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-category.db")
	fixture, err := gorm.Open(sqlite.Open(filepath.ToSlash(path)+"?_pragma=foreign_keys(1)"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := fixture.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := fixture.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{}, &groupBeforeCategories{}, &model.GroupItem{}, &migrate.MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	if fixture.Migrator().HasColumn("groups", "category_id") || fixture.Migrator().HasTable("group_categories") {
		t.Fatal("pre-category fixture unexpectedly contains the new schema")
	}
	for version := 3; version <= 14; version++ {
		if err := fixture.Create(&migrate.MigrationRecord{Version: version, Status: migrate.MigrationRecordStatusSuccess}).Error; err != nil {
			t.Fatal(err)
		}
	}
	config := model.DefaultGroupRelayConfig()
	config.ManualMaxAttempts = 7
	rawConfig, _ := json.Marshal(config)
	channel := model.Channel{ID: 301, ChannelConfig: model.ChannelConfig{Name: "fixture-upgrade", Enabled: true, BaseURL: "https://fixture.example"}}
	channel.RequestFailed = 17
	for _, row := range []any{
		&channel,
		&model.ChannelKey{ID: 401, ChannelID: 301, ChannelKeyConfig: model.ChannelKeyConfig{Name: "a", Key: "fixture-key-a", Enabled: true}},
		&model.ChannelKey{ID: 402, ChannelID: 301, ChannelKeyConfig: model.ChannelKeyConfig{Name: "b", Key: "fixture-key-b", Enabled: true}},
		&model.ChannelModel{ID: 501, ChannelID: 301, Name: "fixture-model"},
		&model.ChannelGrant{ID: 601, ChannelKeyID: 401, ChannelModelID: 501, Protocols: model.ProtocolAll},
		&model.ChannelGrant{ID: 602, ChannelKeyID: 402, ChannelModelID: 501, Protocols: model.ProtocolAll},
	} {
		if err := fixture.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.Exec(`INSERT INTO groups (id, name, mode, model_match_mode, active_item_id, relay_config) VALUES (701, 'fixture-group', 'manual', 'exact', 801, ?)`, string(rawConfig)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.Exec(`INSERT INTO group_items (id, group_id, channel_grant_id, priority) VALUES (801,701,601,9), (802,701,602,3)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	for startup := range 3 {
		if err := InitDB("sqlite", path, false); err != nil {
			t.Fatal(err)
		}
		func() {
			defer Close()
			var group model.Group
			if err := GetDB().Preload("Items", func(tx *gorm.DB) *gorm.DB { return tx.Order("priority") }).First(&group, 701).Error; err != nil {
				t.Fatal(err)
			}
			if group.Name != "fixture-group" || group.ActiveItemID != 801 || group.ModelMatchMode != model.GroupModelMatchModeExact || !reflect.DeepEqual(group.RelayConfig, config) || len(group.Items) != 2 || group.Items[0].ID != 802 || group.Items[1].ID != 801 {
				t.Fatalf("upgrade/restart changed routing data at startup %d: name=%q active=%d match=%q config=%+v items=%+v", startup, group.Name, group.ActiveItemID, group.ModelMatchMode, group.RelayConfig, group.Items)
			}
			if startup == 0 {
				if group.CategoryID != nil {
					t.Fatal("old group did not default to uncategorized")
				}
				if err := GetDB().Create(&model.GroupCategory{ID: 71, Name: "Fixture", NameKey: "fixture"}).Error; err != nil {
					t.Fatal(err)
				}
				if err := GetDB().Model(&group).Update("category_id", 71).Error; err != nil {
					t.Fatal(err)
				}
			} else if group.CategoryID == nil || *group.CategoryID != 71 {
				t.Fatal("restart lost category assignment")
			}
			var persisted model.Channel
			if err := GetDB().First(&persisted, 301).Error; err != nil || persisted.RequestFailed != 17 || persisted.BaseURL != channel.BaseURL {
				t.Fatalf("upgrade changed channel data: %v", err)
			}
			var violations []struct{ Table string }
			if err := GetDB().Raw("PRAGMA foreign_key_check").Scan(&violations).Error; err != nil || len(violations) != 0 {
				t.Fatalf("foreign key violations: %v, %v", violations, err)
			}
			var record migrate.MigrationRecord
			if err := GetDB().First(&record, 15).Error; err != nil || record.Status != migrate.MigrationRecordStatusSuccess {
				t.Fatalf("migration 15 not recorded: %+v, %v", record, err)
			}
		}()
	}
}
