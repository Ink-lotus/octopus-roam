package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidGroupCategory = errors.New("invalid group category")

// GroupCategory 只有一层，NameKey 是内部唯一自然键，不能由管理接口写入。
type GroupCategory struct {
	ID      int    `json:"id" gorm:"primaryKey"`
	Name    string `json:"name" gorm:"not null;size:256"`
	NameKey string `json:"-" gorm:"not null;size:256;uniqueIndex"`
}

func NormalizeGroupCategoryName(name string) (display, key string, err error) {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 64 {
		return "", "", fmt.Errorf("%w: name must contain 1 to 64 Unicode characters", ErrInvalidGroupCategory)
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return "", "", fmt.Errorf("%w: name cannot contain control characters", ErrInvalidGroupCategory)
		}
	}
	return name, strings.ToLower(name), nil
}

func ValidateGroupCategoryID(id *int) error {
	if id != nil && *id <= 0 {
		return fmt.Errorf("%w: category_id must be a positive integer or null", ErrInvalidGroupCategory)
	}
	return nil
}

// GroupCategoryReference 区分 PATCH 的省略和显式 null；Go 调用方也显式标记 Present。
type GroupCategoryReference struct {
	Present bool
	ID      *int
}

func (ref GroupCategoryReference) IsZero() bool { return !ref.Present }

func (ref GroupCategoryReference) MarshalJSON() ([]byte, error) {
	return json.Marshal(ref.ID)
}

func (ref *GroupCategoryReference) UnmarshalJSON(data []byte) error {
	var id *int
	if err := json.Unmarshal(data, &id); err != nil {
		return fmt.Errorf("%w: category_id must be a positive integer or null", ErrInvalidGroupCategory)
	}
	if err := ValidateGroupCategoryID(id); err != nil {
		return err
	}
	*ref = GroupCategoryReference{Present: true, ID: id}
	return nil
}
