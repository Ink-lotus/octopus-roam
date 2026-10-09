package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
)

var (
	privacyWriteMu     sync.Mutex // Lock after groupWriteMu whenever both are needed.
	privacyRulesState  *privacy.Policy
	ErrPrivacyConflict = errors.New("privacy rules changed; reload and review before saving")
	ErrPrivacyNotFound = errors.New("privacy rule not found")
)

type PrivacyBuiltin struct {
	privacy.Builtin
	Enabled bool `json:"enabled"`
}

type PrivacyRules struct {
	Revision    string           `json:"revision"`
	Enabled     bool             `json:"enabled"`
	Builtins    []PrivacyBuiltin `json:"builtins"`
	CustomRules []privacy.Rule   `json:"custom_rules"`
	Limits      privacy.Limits   `json:"limits"`
}

func privacyRulesLocked() (PrivacyRules, error) {
	if privacyRulesState == nil {
		return PrivacyRules{}, fmt.Errorf("privacy rules have not been loaded")
	}
	config := privacyRulesState.Config()
	enabled, err := SettingGetBool(model.SettingKeyPrivacyProtectionEnabled)
	if err != nil {
		return PrivacyRules{}, err
	}
	view := PrivacyRules{Revision: config.Revision, Enabled: enabled, CustomRules: config.CustomRules, Limits: privacy.RuleLimits()}
	for _, builtin := range privacy.Builtins() {
		view.Builtins = append(view.Builtins, PrivacyBuiltin{builtin, config.BuiltinEnabled[builtin.ID]})
	}
	return view, nil
}

func PrivacyRulesGet() (PrivacyRules, error) {
	privacyWriteMu.Lock()
	defer privacyWriteMu.Unlock()
	return privacyRulesLocked()
}

func PrivacyPreview(request privacy.PreviewRequest) (privacy.PreviewResult, error) {
	privacyWriteMu.Lock()
	if privacyRulesState == nil {
		privacyWriteMu.Unlock()
		return privacy.PreviewResult{}, fmt.Errorf("privacy rules have not been loaded")
	}
	config := privacyRulesState.Config()
	privacyWriteMu.Unlock()
	if request.Revision != config.Revision {
		return privacy.PreviewResult{}, ErrPrivacyConflict
	}
	// Compilation and matching do not hold a settings lock or use real channel
	// credentials. A preview only owns this temporary configuration and input.
	return privacy.Preview(config, request)
}

// Decode each action into its own shape. Supplying a definition/field on a
// toggle or delete is an error, even if a client also supplies a valid ID.
type PrivacyMutation struct {
	Revision string
	Action   string
	ID       string
	Rule     privacy.Rule
	Enabled  bool
}

func ParsePrivacyMutation(data []byte) (PrivacyMutation, error) {
	var discriminator map[string]json.RawMessage
	if err := privacy.DecodeStrict(data, &discriminator); err != nil {
		return PrivacyMutation{}, err
	}
	var action string
	if json.Unmarshal(discriminator["action"], &action) != nil {
		return PrivacyMutation{}, privacy.Invalid("action", "required", "an action is required")
	}
	var mutation PrivacyMutation
	switch action {
	case "create", "update":
		var wire struct {
			Revision string        `json:"revision"`
			Action   string        `json:"action"`
			ID       string        `json:"id,omitempty"`
			Rule     *privacy.Rule `json:"rule"`
		}
		if err := privacy.DecodeStrict(data, &wire); err != nil {
			return mutation, err
		}
		if wire.Rule == nil || wire.Rule.ID != "" || (action == "create" && wire.ID != "") {
			return mutation, privacy.Invalid("rule", "definition", "provide a custom rule form without a rule ID")
		}
		mutation = PrivacyMutation{Revision: wire.Revision, Action: action, ID: wire.ID, Rule: *wire.Rule}
	case "toggle":
		var wire struct {
			Revision string `json:"revision"`
			Action   string `json:"action"`
			ID       string `json:"id"`
			Enabled  *bool  `json:"enabled"`
		}
		if err := privacy.DecodeStrict(data, &wire); err != nil {
			return mutation, err
		}
		if wire.Enabled == nil {
			return mutation, privacy.Invalid("enabled", "required", "enabled must be a boolean")
		}
		mutation = PrivacyMutation{Revision: wire.Revision, Action: action, ID: wire.ID, Enabled: *wire.Enabled}
	case "delete":
		var wire struct {
			Revision string `json:"revision"`
			Action   string `json:"action"`
			ID       string `json:"id"`
		}
		if err := privacy.DecodeStrict(data, &wire); err != nil {
			return mutation, err
		}
		mutation = PrivacyMutation{Revision: wire.Revision, Action: action, ID: wire.ID}
	default:
		return mutation, privacy.Invalid("action", "unknown", "unsupported rule action")
	}
	if mutation.Revision == "" {
		return mutation, privacy.Invalid("revision", "required", "revision is required")
	}
	if action != "create" && mutation.ID == "" {
		return mutation, privacy.Invalid("id", "required", "rule ID is required")
	}
	return mutation, nil
}

func PrivacyRulesMutate(ctx context.Context, mutation PrivacyMutation) (PrivacyRules, error) {
	privacyWriteMu.Lock()
	defer privacyWriteMu.Unlock()
	if privacyRulesState == nil {
		return PrivacyRules{}, fmt.Errorf("privacy rules have not been loaded")
	}
	config := privacyRulesState.Config()
	if mutation.Revision != config.Revision {
		return PrivacyRules{}, ErrPrivacyConflict
	}
	if _, builtin := config.BuiltinEnabled[mutation.ID]; builtin {
		if mutation.Action != "toggle" {
			return PrivacyRules{}, privacy.Invalid("id", "builtin", "builtin rules only support toggling enabled")
		}
		config.BuiltinEnabled[mutation.ID] = mutation.Enabled
	} else {
		index := -1
		for i, rule := range config.CustomRules {
			if rule.ID == mutation.ID {
				index = i
				break
			}
		}
		if mutation.Action != "create" && index == -1 {
			return PrivacyRules{}, ErrPrivacyNotFound
		}
		switch mutation.Action {
		case "create":
			mutation.Rule.ID = privacy.NewRuleID()
			config.CustomRules = append(config.CustomRules, mutation.Rule)
		case "update":
			mutation.Rule.ID = mutation.ID
			config.CustomRules[index] = mutation.Rule
		case "toggle":
			config.CustomRules[index].Enabled = mutation.Enabled
		case "delete":
			config.CustomRules = append(config.CustomRules[:index], config.CustomRules[index+1:]...)
		default:
			return PrivacyRules{}, privacy.Invalid("action", "unknown", "unsupported rule action")
		}
	}
	config.Revision = privacy.NewRevision()
	policy, err := privacy.Compile(config)
	if err != nil {
		return PrivacyRules{}, err
	}
	oldValue, err := SettingGetString(model.SettingKeyPrivacyProtectionRules)
	if err != nil {
		return PrivacyRules{}, err
	}
	raw, _ := json.Marshal(policy.Config())
	result := db.GetDB().WithContext(ctx).Model(&model.Setting{}).
		Where("key = ? AND value = ?", model.SettingKeyPrivacyProtectionRules, oldValue).Update("value", string(raw))
	if result.Error != nil {
		return PrivacyRules{}, fmt.Errorf("could not save privacy rules")
	}
	if result.RowsAffected != 1 {
		return PrivacyRules{}, ErrPrivacyConflict
	}
	settingCache.Set(model.SettingKeyPrivacyProtectionRules, string(raw))
	privacyRulesState = policy
	enabled, _ := SettingGetBool(model.SettingKeyPrivacyProtectionEnabled)
	privacy.Publish(enabled, policy)
	return privacyRulesLocked()
}

func setPrivacyEnabled(value string) error {
	privacyWriteMu.Lock()
	defer privacyWriteMu.Unlock()
	if privacyRulesState == nil {
		return fmt.Errorf("privacy rules have not been loaded")
	}
	previous, err := SettingGetString(model.SettingKeyPrivacyProtectionEnabled)
	if err != nil {
		return err
	}
	if previous != value {
		result := db.GetDB().Model(&model.Setting{}).
			Where("key = ? AND value = ?", model.SettingKeyPrivacyProtectionEnabled, previous).Update("value", value)
		if result.Error != nil {
			return fmt.Errorf("could not save privacy setting")
		}
		if result.RowsAffected != 1 {
			return ErrPrivacyConflict
		}
	}
	settingCache.Set(model.SettingKeyPrivacyProtectionEnabled, value)
	privacy.Publish(value == "true", privacyRulesState)
	return nil
}

// Called before changing caches or committing an import. Missing rows are
// supplied by the caller, using either saved target settings or defaults.
func compilePrivacySettings(settings []model.Setting) (bool, *privacy.Policy, error) {
	var raw, enabled string
	for _, setting := range settings {
		switch setting.Key {
		case model.SettingKeyPrivacyProtectionEnabled:
			if err := setting.Validate(); err != nil {
				return false, nil, err
			}
			enabled = setting.Value
		case model.SettingKeyPrivacyProtectionRules:
			raw = setting.Value
		}
	}
	if raw == "" || enabled == "" {
		return false, nil, fmt.Errorf("privacy settings are missing")
	}
	config, err := privacy.ParseConfig(raw)
	if err != nil {
		return false, nil, err
	}
	policy, err := privacy.Compile(config)
	return enabled == "true", policy, err
}
