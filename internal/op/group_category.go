package op

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

var (
	ErrGroupCategoryNotFound = errors.New("group category not found")
	ErrGroupCategoryExists   = errors.New("group category name already exists")
	groupMetadataProcess     = strconv.FormatInt(time.Now().UnixNano(), 10)
	groupMetadataSequence    uint64 // guarded by groupWriteMu，分类及归属共用序号但独立于 Relay。
	groupCategories          = struct {
		sync.RWMutex
		value GroupCategoryListSnapshot
	}{value: GroupCategoryListSnapshot{Version: groupMetadataProcess + ":0", Items: []model.GroupCategory{}}}
)

type GroupCategoryListSnapshot struct {
	Version string                `json:"version"`
	Items   []model.GroupCategory `json:"items"`
}

type GroupCategoryDeletion struct {
	ID                    int           `json:"id"`
	UncategorizedGroupIDs []int         `json:"uncategorized_group_ids"`
	Groups                []model.Group `json:"-"`
}

func nextGroupMetadataVersionLocked() string {
	groupMetadataSequence++
	return groupMetadataProcess + ":" + strconv.FormatUint(groupMetadataSequence, 10)
}

func copyCategoryID(id *int) *int {
	if id == nil {
		return nil
	}
	value := *id
	return &value
}

func equalCategoryID(a, b *int) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// setGroupCacheLocked 将归属与版本作为同一不可变缓存值发布。调用方持有 groupWriteMu。
// 渠道刷新和策略默认值重载也使用此入口，未变归属保留版本。
func setGroupCacheLocked(group model.Group) model.Group {
	old, exists := groupCache.Get(group.ID)
	if exists && old.MetadataVersion != "" && equalCategoryID(old.CategoryID, group.CategoryID) {
		group.MetadataVersion = old.MetadataVersion
	} else {
		group.MetadataVersion = nextGroupMetadataVersionLocked()
	}
	group.CategoryID = copyCategoryID(group.CategoryID)
	group.Category = nil
	groupCache.Set(group.ID, group)
	return group
}

func GroupCategoryList() GroupCategoryListSnapshot {
	groupCategories.RLock()
	defer groupCategories.RUnlock()
	return GroupCategoryListSnapshot{
		Version: groupCategories.value.Version,
		Items:   append([]model.GroupCategory{}, groupCategories.value.Items...),
	}
}

func setGroupCategoriesLocked(categories []model.GroupCategory) {
	categories = append([]model.GroupCategory{}, categories...)
	sort.Slice(categories, func(i, j int) bool {
		if categories[i].Name != categories[j].Name {
			return categories[i].Name < categories[j].Name
		}
		return categories[i].ID < categories[j].ID
	})
	groupCategories.Lock()
	defer groupCategories.Unlock()
	if !slices.Equal(categories, groupCategories.value.Items) {
		groupCategories.value = GroupCategoryListSnapshot{Version: nextGroupMetadataVersionLocked(), Items: categories}
	}
}

func groupCategoryRefreshCacheLocked(ctx context.Context) error {
	var categories []model.GroupCategory
	if err := db.GetDB().WithContext(ctx).Find(&categories).Error; err != nil {
		return err
	}
	setGroupCategoriesLocked(categories)
	return nil
}

func validateGroupCategoryReference(tx *gorm.DB, id *int) error {
	if err := model.ValidateGroupCategoryID(id); err != nil || id == nil {
		return err
	}
	var count int64
	if err := tx.Model(&model.GroupCategory{}).Where("id = ?", *id).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%w: category_id %d does not exist", model.ErrInvalidGroupCategory, *id)
	}
	return nil
}

func groupCategoryNameAvailable(tx *gorm.DB, key string, exceptID int) error {
	var count int64
	if err := tx.Model(&model.GroupCategory{}).Where("name_key = ? AND id <> ?", key, exceptID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrGroupCategoryExists
	}
	return nil
}

func GroupCategoryCreate(name string, ctx context.Context) (*model.GroupCategory, error) {
	display, key, err := model.NormalizeGroupCategoryName(name)
	if err != nil {
		return nil, err
	}
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	category := model.GroupCategory{Name: display, NameKey: key}
	var categories []model.GroupCategory
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := groupCategoryNameAvailable(tx, key, 0); err != nil {
			return err
		}
		if err := tx.Create(&category).Error; err != nil {
			return err
		}
		return tx.Find(&categories).Error
	}); err != nil {
		return nil, err
	}
	setGroupCategoriesLocked(categories)
	return &category, nil
}

func GroupCategoryUpdate(id int, name string, ctx context.Context) (*model.GroupCategory, error) {
	if err := model.ValidateGroupCategoryID(&id); err != nil {
		return nil, err
	}
	display, key, err := model.NormalizeGroupCategoryName(name)
	if err != nil {
		return nil, err
	}
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	var category model.GroupCategory
	var categories []model.GroupCategory
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&category, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGroupCategoryNotFound
			}
			return err
		}
		if err := groupCategoryNameAvailable(tx, key, id); err != nil {
			return err
		}
		category.Name, category.NameKey = display, key
		if err := tx.Model(&category).Select("name", "name_key").Updates(&category).Error; err != nil {
			return err
		}
		return tx.Find(&categories).Error
	}); err != nil {
		return nil, err
	}
	setGroupCategoriesLocked(categories)
	return &category, nil
}

func GroupCategoryDelete(id int, ctx context.Context) (*GroupCategoryDeletion, error) {
	if err := model.ValidateGroupCategoryID(&id); err != nil {
		return nil, err
	}
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	result := &GroupCategoryDeletion{ID: id, UncategorizedGroupIDs: []int{}, Groups: []model.Group{}}
	var categories []model.GroupCategory
	if err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var category model.GroupCategory
		if err := tx.First(&category, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrGroupCategoryNotFound
			}
			return err
		}
		if err := tx.Where("category_id = ?", id).Order("id").Preload("Items").Find(&result.Groups).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.Group{}).Where("category_id = ?", id).Update("category_id", nil).Error; err != nil {
			return err
		}
		if err := tx.Delete(&category).Error; err != nil {
			return err
		}
		return tx.Find(&categories).Error
	}); err != nil {
		return nil, err
	}
	for i := range result.Groups {
		group := result.Groups[i]
		group.CategoryID = nil
		sortGroupItems(group.Items)
		group = setGroupCacheLocked(group)
		result.Groups[i] = groupSnapshot(group)
		result.UncategorizedGroupIDs = append(result.UncategorizedGroupIDs, group.ID)
	}
	setGroupCategoriesLocked(categories)
	return result, nil
}
