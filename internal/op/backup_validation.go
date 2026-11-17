package op

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	webhook "github.com/Ink-lotus/octopus-roam/internal/webhook/config"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var backupMetricColumns = []string{"input_token", "output_token", "input_cost", "output_cost", "wait_time", "request_success", "request_failed"}

type backupTable struct {
	name     string
	rows     any
	key      string
	unique   [][]string
	updates  []string
	preserve []string
	replace  bool
}

// Every persisted table has an explicit conflict policy. No entity write uses
// an ORM upsert: MySQL cannot limit ON DUPLICATE KEY to the requested key.
func backupTables(d *model.DBDump) []backupTable {
	return []backupTable{
		{"channels", d.Channels, "id", [][]string{{"name"}}, backupMetricColumns, []string{"enabled", "follow_global_auto_update", "max_concurrency"}, false},
		{"channel_keys", d.ChannelKeys, "id", [][]string{{"channel_id", "name"}}, append(slices.Clone(backupMetricColumns), "enabled"), nil, false},
		{"channel_models", d.ChannelModels, "id", [][]string{{"channel_id", "name"}}, backupMetricColumns, nil, false},
		{"channel_grants", d.ChannelGrants, "id", [][]string{{"channel_model_id", "channel_key_id"}}, nil, nil, false},
		{"groups", d.Groups, "id", [][]string{{"name"}}, nil, []string{"category_id", "active_item_id"}, false},
		{"group_items", d.GroupItems, "id", [][]string{{"group_id", "channel_grant_id"}}, nil, []string{"priority"}, false},
		{"api_keys", d.APIKeys, "id", nil, nil, []string{"enabled"}, false},
		{"webhook_endpoints", d.WebhookEndpoints, "id", nil, []string{"enabled"}, nil, false},
		{"llm_infos", d.LLMInfos, "name", nil, nil, nil, true},
		{"settings", d.Settings, "key", nil, nil, nil, true},
		{"stats_totals", d.StatsTotal, "id", nil, nil, nil, true},
		{"stats_dailies", d.StatsDaily, "date", nil, nil, nil, true},
		{"stats_hourlies", d.StatsHourly, "hour", nil, nil, nil, true},
		{"stats_api_keys", d.StatsAPIKey, "api_key_id", nil, nil, nil, true},
		{"stats_hourly_archives", d.StatsHourlyArchives, "id", nil, nil, nil, false},
	}
}

func normalizeBackupRows(d *model.DBDump) error {
	for i := range d.Channels {
		row := &d.Channels[i]
		config, err := normalizeChannelConfig(row.ChannelConfig)
		if err != nil {
			return backupProblem(ErrBackupInput, "channels", row.ID, "invalid channel configuration")
		}
		row.ChannelConfig = config
	}
	for i := range d.Groups {
		g := &d.Groups[i]
		g.Name = strings.TrimSpace(g.Name)
		if g.Name == "" {
			return backupProblem(ErrBackupInput, "groups", g.ID, "name is required")
		}
		if g.Mode == "" {
			g.Mode = model.GroupModeManual
		}
		g.ModelMatchMode = model.NormalizeGroupModelMatchMode(g.ModelMatchMode)
		if model.ValidateGroupMode(g.Mode) != nil || model.ValidateGroupModelMatchMode(g.ModelMatchMode) != nil || model.ValidateGroupRelayConfig(g.RelayConfig, g.Mode) != nil {
			return backupProblem(ErrBackupInput, "groups", g.ID, "invalid group configuration")
		}
		model.NormalizeGroupRelayConfigForMode(&g.RelayConfig, g.Mode)
	}
	for i := range d.APIKeys {
		if len(d.APIKeys[i].SupportedModels) == 0 {
			d.APIKeys[i].SupportedModels = nil
		}
	}
	for i := range d.WebhookEndpoints {
		if err := webhook.NormalizeAndValidate(&d.WebhookEndpoints[i]); err != nil {
			return backupProblem(ErrBackupInput, "webhook_endpoints", d.WebhookEndpoints[i].ID, "invalid delivery configuration")
		}
	}
	for i := range d.LLMInfos {
		d.LLMInfos[i].Name = strings.ToLower(strings.TrimSpace(d.LLMInfos[i].Name))
		if d.LLMInfos[i].Name == "" {
			return backupProblem(ErrBackupInput, "llm_infos", 0, "name is required")
		}
	}
	return nil
}

func validateBackupGraph(d *model.DBDump) error {
	if issues := integrity.Inspect(d); len(issues) > 0 {
		i := issues[0]
		return backupProblem(ErrBackupInput, i.Table, i.ID, fmt.Sprintf("%s (related ID %d)", i.Code, i.RelatedID))
	}
	if d.HasNestedBackupAssociations() {
		return backupProblem(ErrBackupInput, "backup", 0, "nested associations are not allowed; use the top-level entity tables")
	}
	for _, row := range d.Channels {
		if len(row.Keys) > 0 || len(row.Models) > 0 {
			return backupProblem(ErrBackupInput, "channels", row.ID, "nested associations are not allowed")
		}
	}
	for _, row := range d.ChannelGrants {
		if row.ChannelKey != nil || row.ChannelModel != nil {
			return backupProblem(ErrBackupInput, "channel_grants", row.ID, "nested associations are not allowed")
		}
		if row.Protocols == 0 || row.Protocols & ^definedProtocols != 0 {
			return backupProblem(ErrBackupInput, "channel_grants", row.ID, "invalid protocols")
		}
	}
	for _, row := range d.Groups {
		if len(row.Items) > 0 || row.Category != nil {
			return backupProblem(ErrBackupInput, "groups", row.ID, "nested associations are not allowed")
		}
	}
	for _, row := range d.GroupItems {
		if row.ChannelGrant != nil {
			return backupProblem(ErrBackupInput, "group_items", row.ID, "nested associations are not allowed")
		}
	}
	for _, row := range d.ChannelKeys {
		if strings.TrimSpace(row.Name) == "" {
			return backupProblem(ErrBackupInput, "channel_keys", row.ID, "name is required")
		}
	}
	for _, row := range d.ChannelModels {
		if strings.TrimSpace(row.Name) == "" {
			return backupProblem(ErrBackupInput, "channel_models", row.ID, "name is required")
		}
	}
	for _, row := range d.StatsDaily {
		if !validBackupDate(row.Date, row.StatsMetrics) {
			return backupProblem(ErrBackupInput, "stats_dailies", 0, "invalid date")
		}
	}
	for _, row := range d.StatsHourly {
		if row.Date == "" || !validBackupDate(row.Date, row.StatsMetrics) {
			return backupProblem(ErrBackupInput, "stats_hourlies", row.Hour, "invalid date")
		}
	}
	return nil
}

func validBackupDate(value string, metrics model.StatsMetrics) bool {
	// Old shutdowns could persist this unused all-zero daily sentinel.
	if value == "" {
		return metrics == (model.StatsMetrics{})
	}
	t, err := time.Parse("20060102", value)
	return err == nil && t.Format("20060102") == value
}

func backupSchema(conn *gorm.DB, rows any) (*schema.Schema, error) {
	stmt := &gorm.Statement{DB: conn}
	if err := stmt.Parse(rows); err != nil {
		return nil, err
	}
	return stmt.Schema, nil
}

func backupField(s *schema.Schema, row reflect.Value, column string) any {
	return s.FieldsByDBName[column].ReflectValueOf(context.Background(), row).Interface()
}

func backupRowID(s *schema.Schema, row reflect.Value) int {
	for _, name := range []string{"id", "api_key_id", "hour"} {
		if s.FieldsByDBName[name] != nil {
			value := reflect.ValueOf(backupField(s, row, name))
			if value.Kind() >= reflect.Int && value.Kind() <= reflect.Int64 {
				return int(value.Int())
			}
		}
	}
	return 0
}

func validateBackupKeys(conn *gorm.DB, d *model.DBDump) error {
	for _, table := range backupTables(d) {
		s, err := backupSchema(conn, table.rows)
		if err != nil {
			return backupDatabaseError(table.name, 0, err)
		}
		rows := reflect.ValueOf(table.rows)
		for _, columns := range append([][]string{{table.key}}, table.unique...) {
			seen := map[string]bool{}
			for i := 0; i < rows.Len(); i++ {
				row := rows.Index(i)
				values := make([]any, len(columns))
				for j, col := range columns {
					values[j] = backupField(s, row, col)
				}
				raw, _ := json.Marshal(values)
				if seen[string(raw)] {
					return backupProblem(ErrBackupInput, table.name, backupRowID(s, row), "duplicate primary or business unique key")
				}
				seen[string(raw)] = true
			}
		}
		for i := 0; i < rows.Len(); i++ {
			row := rows.Index(i)
			for _, field := range s.Fields {
				if field.DBName == "" {
					continue
				}
				value := field.ReflectValueOf(context.Background(), row).Interface()
				if n, ok := value.(float64); ok && (math.IsNaN(n) || math.IsInf(n, 0) || n < 0) {
					return backupProblem(ErrBackupInput, table.name, backupRowID(s, row), "invalid numeric value")
				}
				if slices.Contains(backupMetricColumns, field.DBName) {
					if n, ok := value.(int64); ok && n < 0 {
						return backupProblem(ErrBackupInput, table.name, backupRowID(s, row), "negative statistics")
					}
				}
			}
		}
	}
	return nil
}

func compatibleBackupRow(s *schema.Schema, incoming, local reflect.Value, table backupTable) bool {
	for _, field := range s.Fields {
		if field.DBName == "" || slices.Contains(table.updates, field.DBName) || slices.Contains(table.preserve, field.DBName) {
			continue
		}
		a := field.ReflectValueOf(context.Background(), incoming).Interface()
		b := field.ReflectValueOf(context.Background(), local).Interface()
		if !reflect.DeepEqual(a, b) {
			return false
		}
	}
	return true
}
