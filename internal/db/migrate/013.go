package migrate

import (
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 13,
		Up:      migrateWebhookSettings,
	})
}

func migrateWebhookSettings(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	defaults := []model.Setting{
		{Key: model.SettingKeyWebhookManualFailureThreshold, Value: "10"},
		{Key: model.SettingKeyWebhookFailoverChannelThreshold, Value: "3"},
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&defaults).Error; err != nil {
		return fmt.Errorf("failed to create webhook settings: %w", err)
	}
	return nil
}
