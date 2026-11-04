package migrate

import (
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{Version: 15, Up: migrateGroupCategories})
}

// SQLite 的 CreateConstraint 会重建 groups，DROP 旧表可能级联删除 group_items。
// 在 AutoMigrate 之前用可空列的内联外键完成增量变更，始终保持外键检查开启。
func migrateGroupCategories(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.AutoMigrate(&model.GroupCategory{}); err != nil {
			return fmt.Errorf("create group_categories: %w", err)
		}
		columns, err := physicalTableColumns(tx, "groups")
		if err != nil || columns == nil {
			return err // 新库由随后 AutoMigrate 一次创建 groups 及关联。
		}
		if !columns["category_id"] {
			if tx.Dialector.Name() == "sqlite" {
				if err := tx.Exec("ALTER TABLE `groups` ADD COLUMN `category_id` integer CONSTRAINT `fk_groups_category` REFERENCES `group_categories`(`id`) ON DELETE SET NULL").Error; err != nil {
					return fmt.Errorf("add groups.category_id: %w", err)
				}
			} else if err := tx.Migrator().AddColumn(&model.Group{}, "CategoryID"); err != nil {
				return fmt.Errorf("add groups.category_id: %w", err)
			}
		}
		var invalid int64
		if err := tx.Table("groups").Where("category_id IS NOT NULL AND (category_id <= 0 OR category_id NOT IN (SELECT id FROM group_categories))").Count(&invalid).Error; err != nil {
			return err
		}
		if invalid != 0 {
			return fmt.Errorf("groups contain %d invalid category references", invalid)
		}
		if !tx.Migrator().HasConstraint(&model.Group{}, "Category") {
			if tx.Dialector.Name() == "sqlite" {
				return fmt.Errorf("existing groups.category_id is missing its category foreign key; refusing to rebuild groups")
			}
			if err := tx.Migrator().CreateConstraint(&model.Group{}, "Category"); err != nil {
				return err
			}
		}
		if !tx.Migrator().HasIndex(&model.Group{}, "CategoryID") {
			if err := tx.Migrator().CreateIndex(&model.Group{}, "CategoryID"); err != nil {
				return err
			}
		}
		return nil
	})
}
