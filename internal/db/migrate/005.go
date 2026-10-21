package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func init() {
	// 必须在 migration 11 消费 channels.key / 创建新版 channel_keys 之前搬运旧数据。
	RegisterBeforeAutoMigration(Migration{
		Version: 5,
		Up:      migrateChannelToSingleURLAndKey,
	})
}

var errIncompleteChannelSchema = errors.New("incomplete channel migration schema")

// migration 5 的单值目标快照，不能引用已经移除了 Key 的当前 Channel 模型。
type channelSingleValuesV5 struct {
	BaseURL string
	Key     string
}

func (channelSingleValuesV5) TableName() string { return "channels" }

func currentChannelKeyColumns(columns physicalColumns) bool {
	return columns.has("id", "channel_id", "name", "key", "enabled") && !columns["channel_key"]
}

// migrateChannelToSingleURLAndKey 将多地址、多凭据渠道收敛为单地址、单凭据，并删除旧结构。
func migrateChannelToSingleURLAndKey(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	columns, err := physicalTableColumns(db, "channels")
	if err != nil {
		return err
	}
	keyColumns, err := physicalTableColumns(db, "channel_keys")
	if err != nil {
		return err
	}
	if columns == nil {
		if keyColumns != nil {
			return fmt.Errorf("%w: channel_keys exists without channels", errIncompleteChannelSchema)
		}
		return nil
	}
	legacyKeys := keyColumns.has("id", "channel_id", "channel_key") && !keyColumns["key"]
	currentKeys := currentChannelKeyColumns(keyColumns)
	if !columns.has("id", "name") || (keyColumns != nil && !legacyKeys && !currentKeys) {
		return fmt.Errorf("%w: missing channel identity or unknown channel_keys layout", errIncompleteChannelSchema)
	}
	if !columns["type"] {
		if columns["key"] || columns["base_urls"] || !currentKeys ||
			!columns.has("base_url", "dialect", "openai_chat_completion_path", "openai_response_path", "anthropic_message_path") {
			return fmt.Errorf("%w: current channel layout required when channels.type is absent", errIncompleteChannelSchema)
		}
		return nil
	}
	// 完整旧来源才能补目标列；无来源的缺列及混合新旧凭据表均在写入前失败。
	if currentKeys || (!columns["base_url"] && !columns["base_urls"]) || (!columns["key"] && !legacyKeys) {
		return fmt.Errorf("%w: legacy address and credential sources required", errIncompleteChannelSchema)
	}
	for _, target := range []struct{ column, field string }{{"base_url", "BaseURL"}, {"key", "Key"}} {
		if !columns[target.column] {
			if err := db.Migrator().AddColumn(&channelSingleValuesV5{}, target.field); err != nil {
				return fmt.Errorf("failed to add channels.%s: %w", target.column, err)
			}
		}
	}

	if columns["base_urls"] {
		type legacyBaseURL struct {
			URL string `json:"url"` // 旧地址值。
		}
		type legacyChannel struct {
			ID       int    `gorm:"column:id"`        // 渠道主键。
			BaseURLs string `gorm:"column:base_urls"` // 旧地址数组 JSON。
		}

		rows := make([]legacyChannel, 0)
		if err := db.Table("channels").Select("id, base_urls").Find(&rows).Error; err != nil {
			return fmt.Errorf("failed to read channels.base_urls: %w", err)
		}
		for _, row := range rows {
			if strings.TrimSpace(row.BaseURLs) == "" || strings.TrimSpace(row.BaseURLs) == "null" {
				continue
			}
			urls := make([]legacyBaseURL, 0)
			if err := json.Unmarshal([]byte(row.BaseURLs), &urls); err != nil {
				return fmt.Errorf("failed to decode channels.base_urls for id=%d: %w", row.ID, err)
			}
			if len(urls) == 0 || strings.TrimSpace(urls[0].URL) == "" {
				continue
			}
			if err := db.Table("channels").Where("id = ? AND (base_url IS NULL OR base_url = '')", row.ID).Update("base_url", urls[0].URL).Error; err != nil {
				return fmt.Errorf("failed to migrate channels.base_urls for id=%d: %w", row.ID, err)
			}
		}
	}

	if legacyKeys {
		type legacyChannelKey struct {
			ChannelID  int    `gorm:"column:channel_id"`  // 所属渠道主键。
			ChannelKey string `gorm:"column:channel_key"` // 旧凭据值。
		}

		keys := make([]legacyChannelKey, 0)
		// 按旧记录主键顺序读取，每个渠道只保留第一项。
		if err := db.Table("channel_keys").
			Select("channel_id, channel_key").
			Where("channel_key <> ''").
			Order("channel_id ASC, id ASC").
			Find(&keys).Error; err != nil {
			return fmt.Errorf("failed to read channel_keys: %w", err)
		}
		selected := make(map[int]struct{})
		var quotedKey strings.Builder
		db.Dialector.QuoteTo(&quotedKey, "key")
		emptyKeyCondition := fmt.Sprintf("(%s IS NULL OR %s = '')", quotedKey.String(), quotedKey.String())
		for _, key := range keys {
			if _, ok := selected[key.ChannelID]; ok || strings.TrimSpace(key.ChannelKey) == "" {
				continue
			}
			if err := db.Table("channels").Where("id = ? AND "+emptyKeyCondition, key.ChannelID).Update("key", key.ChannelKey).Error; err != nil {
				return fmt.Errorf("failed to migrate channel key for channel_id=%d: %w", key.ChannelID, err)
			}
			selected[key.ChannelID] = struct{}{}
		}
	}

	// 仅删除已验证且已经搬运的旧凭据表，不能按 channels.type 猜测任意同名表。
	if legacyKeys {
		if err := db.Migrator().DropTable("channel_keys"); err != nil {
			return fmt.Errorf("failed to drop channel_keys: %w", err)
		}
	}
	if columns["base_urls"] {
		return dropColumnIfExists(db, &channelSingleValuesV5{}, "channels", "base_urls")
	}
	return nil
}
