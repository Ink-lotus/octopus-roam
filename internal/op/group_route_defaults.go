package op

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// GroupRouteConfigChange 包含提交前后快照, 由调用层判断是否重置运行状态并发布事件。
type GroupRouteConfigChange struct {
	Before model.Group
	After  model.Group
}

func normalizeGroupRouteDefaults(config model.GroupRelayConfig) (model.GroupRelayConfig, error) {
	for _, mode := range []model.GroupMode{model.GroupModeManual, model.GroupModeFailover} {
		if err := model.ValidateGroupRelayConfig(config, mode); err != nil {
			return model.GroupRelayConfig{}, err
		}
	}
	model.NormalizeGroupRelayConfig(&config)
	return config, nil
}

func parseGroupRouteDefaults(raw string) (model.GroupRelayConfig, error) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return model.GroupRelayConfig{}, fmt.Errorf("%w: route defaults must be a JSON object", model.ErrInvalidGroupConfig)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config model.GroupRelayConfig
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("%w: malformed route defaults JSON", model.ErrInvalidGroupConfig)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, fmt.Errorf("%w: unexpected content after route defaults", model.ErrInvalidGroupConfig)
	}
	return normalizeGroupRouteDefaults(config)
}

// GroupRouteDefaultsGet 返回经过类型验证的全局默认策略; 缺键的旧备份使用内建默认值。
func GroupRouteDefaultsGet() (model.GroupRelayConfig, error) {
	raw, exists := settingCache.Get(model.SettingKeyGroupRouteDefaults)
	if !exists {
		return model.DefaultGroupRelayConfig(), nil
	}
	return parseGroupRouteDefaults(raw)
}

// GroupRouteDefaultsSet 将 Setting 和可选的分组更新放在同一事务中。
// 仅变更公共和当前模式字段; 未发生变化的组不重写, 返回实际变更的组供调用层发布。
func GroupRouteDefaultsSet(config model.GroupRelayConfig, applyToExisting bool, ctx context.Context) (model.GroupRelayConfig, []GroupRouteConfigChange, error) {
	config, err := normalizeGroupRouteDefaults(config)
	if err != nil {
		return config, nil, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return config, nil, err
	}
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	var changes []GroupRouteConfigChange
	err = db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := createUpsertSettings(tx, []model.Setting{{Key: model.SettingKeyGroupRouteDefaults, Value: string(raw)}}); err != nil {
			return err
		}
		if !applyToExisting {
			return nil
		}
		var groups []model.Group
		if err := tx.Preload("Items").Order("id").Find(&groups).Error; err != nil {
			return err
		}
		for _, group := range groups {
			if err := model.ValidateGroupMode(group.Mode); err != nil {
				return err
			}
			next := config
			if group.Mode == model.GroupModeManual {
				next.MemberMaxAttempts = group.RelayConfig.MemberMaxAttempts
				next.MemberCooldownSeconds = group.RelayConfig.MemberCooldownSeconds
			} else {
				next.ManualMaxAttempts = group.RelayConfig.ManualMaxAttempts
			}
			if next == group.RelayConfig {
				continue
			}
			updated := group
			updated.RelayConfig = next
			if err := tx.Model(&model.Group{}).Where("id = ?", group.ID).Select("relay_config").Updates(&updated).Error; err != nil {
				return err
			}
			sortGroupItems(group.Items)
			sortGroupItems(updated.Items)
			changes = append(changes, GroupRouteConfigChange{Before: group, After: updated})
		}
		return nil
	})
	if err != nil {
		return config, nil, err
	}
	settingCache.Set(model.SettingKeyGroupRouteDefaults, string(raw))
	for i := range changes {
		groupCache.Set(changes[i].After.ID, changes[i].After)
		changes[i].Before = groupSnapshot(changes[i].Before)
		changes[i].After = groupSnapshot(changes[i].After)
	}
	return config, changes, nil
}
