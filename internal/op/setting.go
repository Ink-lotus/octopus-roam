package op

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/privacy"
	"github.com/bestruirui/octopus/internal/utils/cache"
)

var settingCache = cache.New[model.SettingKey, string](16)

func SettingList(ctx context.Context) ([]model.Setting, error) {
	settings := make([]model.Setting, 0, settingCache.Len())
	for key, value := range settingCache.GetAll() {
		settings = append(settings, model.Setting{
			Key:   key,
			Value: value,
		})
	}
	return settings, nil
}

func SettingGetString(key model.SettingKey) (string, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return "", fmt.Errorf("setting not found")
	}
	return setting, nil
}

func SettingSetString(key model.SettingKey, value string) error {
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
	if key == model.SettingKeyPrivacyProtectionEnabled {
		privacy.SetDiagnosticsEnabled(value == "true")
	}
	if isChannelAutomationSetting(key) {
		automationSettingsSignal.notify()
	}
	return nil
}

func SettingGetInt(key model.SettingKey) (int, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return 0, fmt.Errorf("setting not found")
	}
	return strconv.Atoi(setting)
}

func SettingGetBool(key model.SettingKey) (bool, error) {
	setting, ok := settingCache.Get(key)
	if !ok {
		return false, fmt.Errorf("setting not found")
	}
	return strconv.ParseBool(setting)
}

func SettingSetInt(key model.SettingKey, value int) error {
	if isChannelAutomationSetting(key) || key == model.SettingKeyPrivacyProtectionEnabled {
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

	if len(missingSettings) > 0 {
		if err := db.CreateInBatches(missingSettings, len(missingSettings)).Error; err != nil {
			return fmt.Errorf("failed to create missing settings: %w", err)
		}
		settings = append(settings, missingSettings...)
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
	privacyEnabled, _ := SettingGetBool(model.SettingKeyPrivacyProtectionEnabled)
	privacy.SetDiagnosticsEnabled(privacyEnabled)
	return nil
}

func isChannelAutomationSetting(key model.SettingKey) bool {
	return key == model.SettingKeyChannelAutoUpdateEnabled || key == model.SettingKeyChannelAutoUpdateInterval || key == model.SettingKeyChannelAutoAddGroups
}
