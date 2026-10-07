package db

import (
	"path/filepath"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestChannelAutomationMigratesExistingRows(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := conn.DB()
	defer sqlDB.Close()
	if err := conn.Exec(`CREATE TABLE channels (id integer PRIMARY KEY, name text, enabled numeric, base_url text)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`INSERT INTO channels (id, name, enabled, base_url) VALUES (1, 'legacy', 0, 'http://127.0.0.1')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelRefreshRecord{}); err != nil {
		t.Fatal(err)
	}
	var channel model.Channel
	if err := conn.First(&channel, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !channel.FollowsGlobalAutoUpdate() || channel.MaxConcurrency != 0 || channel.Enabled {
		t.Fatalf("legacy defaults changed: %+v", channel.ChannelConfig)
	}
	if err := conn.Model(&channel).Updates(map[string]any{"follow_global_auto_update": false, "max_concurrency": 3}).Error; err != nil {
		t.Fatal(err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelRefreshRecord{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.First(&channel, 1).Error; err != nil {
		t.Fatal(err)
	}
	if channel.FollowsGlobalAutoUpdate() || channel.MaxConcurrency != 3 {
		t.Fatal("second startup lost explicit settings")
	}
}
