package op

import (
	"encoding/json"
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/privacy"
	"gorm.io/gorm"
)

type privacyImport struct {
	previous map[model.SettingKey]string
	settings []model.Setting
	policy   *privacy.Policy
	enabled  bool
}

func isPrivacySetting(key model.SettingKey) bool {
	return key == model.SettingKeyPrivacyProtectionRules || key == model.SettingKeyPrivacyProtectionEnabled
}

// Build/compile the effective pair before the transaction writes any table.
// Old incremental backups preserve target rules, while an absent target gets
// defaults. Every completed import invalidates already-open editors.
func preparePrivacyImport(tx *gorm.DB, incoming []model.Setting) (*privacyImport, error) {
	var stored []model.Setting
	if err := tx.Where("key IN ?", []model.SettingKey{model.SettingKeyPrivacyProtectionRules, model.SettingKeyPrivacyProtectionEnabled}).Find(&stored).Error; err != nil {
		return nil, err
	}
	result := &privacyImport{previous: map[model.SettingKey]string{}}
	values := map[model.SettingKey]string{model.SettingKeyPrivacyProtectionEnabled: "false", model.SettingKeyPrivacyProtectionRules: privacy.DefaultConfigJSON()}
	for _, setting := range stored {
		result.previous[setting.Key], values[setting.Key] = setting.Value, setting.Value
	}
	for _, setting := range incoming {
		if isPrivacySetting(setting.Key) {
			values[setting.Key] = setting.Value
		}
	}
	config, err := privacy.ParseConfig(values[model.SettingKeyPrivacyProtectionRules])
	if err != nil {
		return nil, err
	}
	config.Revision = privacy.NewRevision()
	raw, _ := json.Marshal(config)
	result.settings = []model.Setting{
		{Key: model.SettingKeyPrivacyProtectionRules, Value: string(raw)},
		{Key: model.SettingKeyPrivacyProtectionEnabled, Value: values[model.SettingKeyPrivacyProtectionEnabled]},
	}
	result.enabled, result.policy, err = compilePrivacySettings(result.settings)
	return result, err
}

func (update *privacyImport) write(tx *gorm.DB) (int64, error) {
	var count int64
	for _, setting := range update.settings {
		previous, exists := update.previous[setting.Key]
		if exists {
			if previous == setting.Value {
				continue
			}
			result := tx.Model(&model.Setting{}).Where("key = ? AND value = ?", setting.Key, previous).Update("value", setting.Value)
			if result.Error != nil {
				return 0, result.Error
			}
			if result.RowsAffected != 1 {
				return 0, ErrPrivacyConflict
			}
			count += result.RowsAffected
		} else {
			if err := tx.Create(&setting).Error; err != nil {
				return 0, err
			}
			count++
		}
	}
	return count, nil
}

func (update *privacyImport) publish() {
	for _, setting := range update.settings {
		settingCache.Set(setting.Key, setting.Value)
	}
	privacyRulesState = update.policy
	privacy.Publish(update.enabled, update.policy)
}

func validatePrivacyImport(settings []model.Setting) error {
	seen := map[model.SettingKey]bool{}
	for _, setting := range settings {
		if seen[setting.Key] {
			return fmt.Errorf("duplicate setting key in backup: %s", setting.Key)
		}
		seen[setting.Key] = true
		if isPrivacySetting(setting.Key) {
			if err := setting.Validate(); err != nil {
				return fmt.Errorf("invalid %s in backup: %w", setting.Key, err)
			}
		}
	}
	return nil
}
