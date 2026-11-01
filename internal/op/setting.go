package op

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/utils/cache"
)

var settingCache = cache.New[model.SettingKey, string](16)

func SettingList(ctx context.Context) ([]model.Setting, error) {
	settings := make([]model.Setting, 0, settingCache.Len())
	for key, value := range settingCache.GetAll() {
		if model.IsRetiredSetting(key) {
			continue
		}
		settings = append(settings, model.Setting{
			Key:   key,
			Value: value,
		})
	}
	return settings, nil
}

func SettingGetString(key model.SettingKey) (string, error) {
	if model.IsRetiredSetting(key) {
		return "", model.ErrSettingRetired
	}
	setting, ok := settingCache.Get(key)
	if !ok {
		return "", fmt.Errorf("setting not found")
	}
	return setting, nil
}

func SettingSetString(key model.SettingKey, value string) error {
	if model.IsRetiredSetting(key) {
		return model.ErrSettingRetired
	}
	if err := (&model.Setting{Key: key, Value: value}).Validate(); err != nil {
		return err
	}
	if key == model.SettingKeyGroupRouteDefaults {
		return fmt.Errorf("use /api/v1/group/route-defaults to update route defaults")
	}
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	if valueCache == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, value)
	if isChannelAutomationSetting(key) {
		automationSettingsSignal.notify()
	}
	return nil
}

func SettingGetInt(key model.SettingKey) (int, error) {
	setting, err := SettingGetString(key)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(setting)
}

func SettingGetBool(key model.SettingKey) (bool, error) {
	setting, err := SettingGetString(key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(setting)
}

func SettingSetInt(key model.SettingKey, value int) error {
	if isChannelAutomationSetting(key) || model.IsRetiredSetting(key) {
		return SettingSetString(key, strconv.Itoa(value))
	}
	if key == model.SettingKeyGroupRouteDefaults {
		return fmt.Errorf("use /api/v1/group/route-defaults to update route defaults")
	}
	valueCache, ok := settingCache.Get(key)
	if !ok {
		return fmt.Errorf("setting not found")
	}
	valueCacheNum, err := strconv.Atoi(valueCache)
	if err != nil {
		return fmt.Errorf("failed to set setting: %w", err)
	}
	if valueCacheNum == value {
		return nil
	}
	result := db.GetDB().Model(&model.Setting{Key: key}).Update("Value", value)
	if result.Error != nil {
		return fmt.Errorf("failed to set setting: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("failed to set setting, key not found")
	}
	settingCache.Set(key, strconv.Itoa(value))
	return nil
}

func settingRefreshCache(ctx context.Context) error {
	groupWriteMu.Lock()
	defer groupWriteMu.Unlock()
	db := db.GetDB().WithContext(ctx)

	var settings []model.Setting
	if err := db.Find(&settings).Error; err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}
	settings = withoutRetiredSettings(settings)
	settingCache.Del(model.SettingKeyPrivacyProtectionEnabled)
	settingCache.Del(model.SettingKeyPrivacyProtectionRules)

	existingKeys := make(map[model.SettingKey]bool)
	for _, setting := range settings {
		existingKeys[setting.Key] = true
	}

	defaultSettings := model.DefaultSettings()
	missingSettings := make([]model.Setting, 0, len(defaultSettings))

	for _, defaultSetting := range defaultSettings {
		if !existingKeys[defaultSetting.Key] {
			missingSettings = append(missingSettings, defaultSetting)
		}
	}

	settings = append(settings, missingSettings...)
	if len(missingSettings) > 0 {
		if err := db.CreateInBatches(missingSettings, len(missingSettings)).Error; err != nil {
			return fmt.Errorf("failed to create missing settings: %w", err)
		}
	}
	automationChanged := false
	for _, setting := range settings {
		if isChannelAutomationSetting(setting.Key) {
			previous, exists := settingCache.Get(setting.Key)
			automationChanged = automationChanged || !exists || previous != setting.Value
		}
		settingCache.Set(setting.Key, setting.Value)
	}
	if automationChanged {
		automationSettingsSignal.notify()
	}
	return nil
}

// Filter before validating or merging defaults. Retired values are opaque and
// existing database rows stay untouched, including malformed legacy rule text.
func withoutRetiredSettings(settings []model.Setting) []model.Setting {
	active := make([]model.Setting, 0, len(settings))
	for _, setting := range settings {
		if !model.IsRetiredSetting(setting.Key) {
			active = append(active, setting)
		}
	}
	return active
}

func isChannelAutomationSetting(key model.SettingKey) bool {
	return key == model.SettingKeyChannelAutoUpdateEnabled || key == model.SettingKeyChannelAutoUpdateInterval || key == model.SettingKeyChannelAutoAddGroups
}
