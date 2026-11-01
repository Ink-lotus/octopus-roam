package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 14,
		Up:      migrateCooldownRules,
	})
}

func migrateCooldownRules(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	columns, err := physicalTableColumns(db, "channels")
	if err != nil {
		return err
	}
	if columns == nil || !columns["cooldown_rules"] {
		return nil
	}
	if err := db.Table("channels").Where("cooldown_rules IS NULL").Update("cooldown_rules", "[]").Error; err != nil {
		return fmt.Errorf("failed to backfill channels.cooldown_rules: %w", err)
	}
	return nil
}
