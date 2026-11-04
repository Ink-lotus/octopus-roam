package op

import (
	"fmt"
	"sort"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

// 校验整个备份的引用，不用本地碰巧相同的 ID 猜测缺失的分类描述。
func validateBackupCategories(dump *model.DBDump) error {
	ids := make(map[int]bool, len(dump.GroupCategories))
	keys := make(map[string]bool, len(dump.GroupCategories))
	for i := range dump.GroupCategories {
		category := &dump.GroupCategories[i]
		if category.ID <= 0 || ids[category.ID] {
			return fmt.Errorf("invalid platform categories: group_categories[%d].id must be positive and unique", i)
		}
		name, key, err := model.NormalizeGroupCategoryName(category.Name)
		if err != nil {
			return fmt.Errorf("invalid platform categories: group_categories[%d].name: %w", i, err)
		}
		if keys[key] {
			return fmt.Errorf("invalid platform categories: duplicate normalized name at group_categories[%d]", i)
		}
		category.Name = name
		ids[category.ID], keys[key] = true, true
	}
	for i, group := range dump.Groups {
		if group.CategoryID == nil {
			continue
		}
		if !ids[*group.CategoryID] {
			return fmt.Errorf("invalid platform categories: groups[%d].category_id must reference an ID described in group_categories", i)
		}
	}
	return nil
}

// 先按自然键归并，再保留所有可用备份 ID，最后为数值 ID 冲突分配新 ID。
// 全部步骤处在原导入事务内，不覆盖本地分类名。
func importGroupCategories(tx *gorm.DB, input []model.BackupGroupCategory) (map[int]int, int64, error) {
	var existing []model.GroupCategory
	if err := tx.Find(&existing).Error; err != nil {
		return nil, 0, err
	}
	byKey := make(map[string]int, len(existing))
	used := make(map[int]bool, len(existing))
	for _, category := range existing {
		byKey[category.NameKey], used[category.ID] = category.ID, true
	}
	mapping := make(map[int]int, len(input))
	pending := make([]model.BackupGroupCategory, 0)
	var inserted int64
	// 输入已统一验证；按 ID 定序保证相同样本的冲突分配可复核。
	input = append([]model.BackupGroupCategory(nil), input...)
	sort.Slice(input, func(i, j int) bool { return input[i].ID < input[j].ID })
	for _, category := range input {
		name, key, err := model.NormalizeGroupCategoryName(category.Name)
		if err != nil {
			return nil, 0, err
		}
		if id, exists := byKey[key]; exists {
			mapping[category.ID] = id
			continue
		}
		if used[category.ID] {
			pending = append(pending, category)
			continue
		}
		row := model.GroupCategory{ID: category.ID, Name: name, NameKey: key}
		if err := tx.Create(&row).Error; err != nil {
			return nil, 0, err
		}
		inserted++
		mapping[category.ID], byKey[key], used[category.ID] = row.ID, row.ID, true
	}
	// PostgreSQL 的显式 ID 插入不会推进 serial 序列。为冲突分配或后续 CRUD
	// 保留可用序号；nextval 保证不因已有删除记录而把序列倒退。
	if inserted > 0 && tx.Dialector.Name() == "postgres" {
		if err := tx.Exec("SELECT setval(pg_get_serial_sequence('group_categories', 'id'), GREATEST((SELECT MAX(id) FROM group_categories), nextval(pg_get_serial_sequence('group_categories', 'id'))), true)").Error; err != nil {
			return nil, 0, err
		}
	}
	for _, category := range pending {
		name, key, err := model.NormalizeGroupCategoryName(category.Name)
		if err != nil {
			return nil, 0, err
		}
		row := model.GroupCategory{Name: name, NameKey: key}
		if err := tx.Create(&row).Error; err != nil {
			return nil, 0, err
		}
		inserted++
		mapping[category.ID] = row.ID
	}
	return mapping, inserted, nil
}

func mapBackupGroupCategories(groups []model.Group, mapping map[int]int) ([]model.Group, error) {
	rows := append([]model.Group(nil), groups...)
	for i := range rows {
		rows[i].Category = nil
		rows[i].MetadataVersion = ""
		if rows[i].CategoryID == nil {
			continue
		}
		id, exists := mapping[*rows[i].CategoryID]
		if !exists {
			return nil, fmt.Errorf("invalid platform categories: missing mapping for groups[%d].category_id", i)
		}
		rows[i].CategoryID = &id
	}
	return rows, nil
}
