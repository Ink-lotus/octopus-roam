package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type BackupGroupCategory struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// DBDump is a full-database JSON export format for Octopus Roam.
// Import uses incremental semantics (insert new rows, and upsert on tables with natural keys).
type DBDump struct {
	Product    string    `json:"product"` // 产品标识，缺失仅接受已审计的上游格式。
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`

	Channels         []Channel             `json:"channels,omitempty"`       // 渠道数据。
	ChannelKeys      []ChannelKey          `json:"channel_keys,omitempty"`   // 渠道凭据数据。
	ChannelModels    []ChannelModel        `json:"channel_models,omitempty"` // 渠道模型数据。
	ChannelGrants    []ChannelGrant        `json:"channel_grants,omitempty"` // 渠道授权数据, 含统计。
	Groups           []Group               `json:"groups,omitempty"`         // 分组数据。
	GroupItems       []GroupItem           `json:"group_items,omitempty"`    // 分组成员数据。
	LLMInfos         []LLMInfo             `json:"llm_infos,omitempty"`      // 模型价格数据。
	APIKeys          []APIKey              `json:"api_keys,omitempty"`       // API Key 数据。
	Settings         []Setting             `json:"settings,omitempty"`       // 系统设置数据。
	WebhookEndpoints []WebhookEndpoint     `json:"webhook_endpoints,omitempty"`
	GroupCategories  []BackupGroupCategory `json:"group_categories"`

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`

	explicitNullCooldownRules   bool `json:"-"`
	explicitNullGroupCategories bool `json:"-"`
}

func (dump *DBDump) UnmarshalJSON(data []byte) error {
	type plainDBDump DBDump
	var decoded plainDBDump
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("backup must be a JSON object")
		}
		return err
	}

	explicitNullCooldownRules := false
	if raw, ok := fields["channels"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		var channels []json.RawMessage
		if err := json.Unmarshal(raw, &channels); err != nil {
			return err
		}
		for _, channelRaw := range channels {
			var channelFields map[string]json.RawMessage
			if err := json.Unmarshal(channelRaw, &channelFields); err != nil {
				return err
			}
			if cooldownRaw, ok := channelFields["cooldown_rules"]; ok && bytes.Equal(bytes.TrimSpace(cooldownRaw), []byte("null")) {
				explicitNullCooldownRules = true
			}
		}
	}

	explicitNullGroupCategories := false
	if raw, ok := fields["group_categories"]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		explicitNullGroupCategories = true
	}

	*dump = DBDump(decoded)
	dump.explicitNullCooldownRules = explicitNullCooldownRules
	dump.explicitNullGroupCategories = explicitNullGroupCategories
	return nil
}

// MarshalJSON writes stable empty arrays and validates the persisted rule shape.
func (dump DBDump) MarshalJSON() ([]byte, error) {
	clone := dump
	clone.GroupCategories = append([]BackupGroupCategory{}, dump.GroupCategories...)
	clone.Channels = append([]Channel{}, dump.Channels...)
	for i := range clone.Channels {
		rules, err := NormalizeCooldownRules(clone.Channels[i].CooldownRules)
		if err != nil {
			return nil, err
		}
		clone.Channels[i].CooldownRules = rules
	}
	type plainDBDump DBDump
	return json.Marshal(plainDBDump(clone))
}

func (dump *DBDump) HasExplicitNullCooldownRules() bool {
	return dump != nil && dump.explicitNullCooldownRules
}

func (dump *DBDump) HasExplicitNullGroupCategories() bool {
	return dump != nil && dump.explicitNullGroupCategories
}

func (dump *DBDump) HasNonNullGroupCategoryReference() bool {
	if dump != nil {
		for _, group := range dump.Groups {
			if group.CategoryID != nil {
				return true
			}
		}
	}
	return false
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
}
