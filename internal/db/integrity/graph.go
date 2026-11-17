// Package integrity inspects persisted relationships without repairing or guessing identities.
package integrity

import (
	"fmt"
	"sort"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

// Issue contains identifiers and fixed diagnostic codes only, never configuration or credentials.
type Issue struct {
	Table     string `json:"table"`
	ID        int    `json:"id"`
	Code      string `json:"code"`
	RelatedID int    `json:"related_id,omitempty"`
}

func (i Issue) Error() string {
	return fmt.Sprintf("%s ID %d: %s (related ID %d)", i.Table, i.ID, i.Code, i.RelatedID)
}

// ReadGraph deliberately does not select names, credentials, URLs, headers or request bodies.
func ReadGraph(conn *gorm.DB) (*model.DBDump, error) {
	d := &model.DBDump{}
	for _, query := range []struct {
		rows    any
		columns string
	}{
		{&d.Channels, "id"}, {&d.ChannelKeys, "id, channel_id"},
		{&d.ChannelModels, "id, channel_id"}, {&d.ChannelGrants, "id, channel_model_id, channel_key_id"},
		{&d.Groups, "id, category_id, active_item_id"}, {&d.GroupItems, "id, group_id, channel_grant_id"},
		{&d.APIKeys, "id"}, {&d.WebhookEndpoints, "id"},
		{&d.StatsTotal, "id"}, {&d.StatsHourly, "hour"}, {&d.StatsAPIKey, "api_key_id"},
	} {
		if err := conn.Select(query.columns).Find(query.rows).Error; err != nil {
			return nil, err
		}
	}
	var categories []model.GroupCategory
	if err := conn.Select("id").Find(&categories).Error; err != nil {
		return nil, err
	}
	for _, c := range categories {
		d.GroupCategories = append(d.GroupCategories, model.BackupGroupCategory{ID: c.ID})
	}
	// Audit old releases without creating the optional archive table.
	tables, err := conn.Migrator().GetTables()
	if err != nil {
		return nil, err
	}
	for _, table := range tables {
		if table == "stats_hourly_archives" {
			if err := conn.Order("id").Find(&d.StatsHourlyArchives).Error; err != nil {
				return nil, err
			}
		}
	}
	return d, nil
}

// Inspect validates the complete graph in the supplied snapshot. A reference can
// never be satisfied by an unrelated row outside that snapshot.
func Inspect(d *model.DBDump) []Issue {
	issues := []Issue{}
	add := func(table string, id int, code string, related int) {
		issues = append(issues, Issue{table, id, code, related})
	}
	ids := func(table string, values []int) map[int]bool {
		seen := make(map[int]bool, len(values))
		for _, id := range values {
			if id <= 0 {
				add(table, id, "invalid_id", 0)
			}
			if seen[id] {
				add(table, id, "duplicate_id", 0)
			}
			seen[id] = true
		}
		return seen
	}
	channels := ids("channels", collect(d.Channels, func(v model.Channel) int { return v.ID }))
	keys := ids("channel_keys", collect(d.ChannelKeys, func(v model.ChannelKey) int { return v.ID }))
	models := ids("channel_models", collect(d.ChannelModels, func(v model.ChannelModel) int { return v.ID }))
	grants := ids("channel_grants", collect(d.ChannelGrants, func(v model.ChannelGrant) int { return v.ID }))
	groups := ids("groups", collect(d.Groups, func(v model.Group) int { return v.ID }))
	items := ids("group_items", collect(d.GroupItems, func(v model.GroupItem) int { return v.ID }))
	categories := ids("group_categories", collect(d.GroupCategories, func(v model.BackupGroupCategory) int { return v.ID }))
	apiKeys := ids("api_keys", collect(d.APIKeys, func(v model.APIKey) int { return v.ID }))
	ids("webhook_endpoints", collect(d.WebhookEndpoints, func(v model.WebhookEndpoint) int { return v.ID }))
	keyParents, modelParents, itemParents := map[int]int{}, map[int]int{}, map[int]int{}
	for _, k := range d.ChannelKeys {
		keyParents[k.ID] = k.ChannelID
		if !channels[k.ChannelID] {
			add("channel_keys", k.ID, "missing_channel", k.ChannelID)
		}
	}
	for _, m := range d.ChannelModels {
		modelParents[m.ID] = m.ChannelID
		if !channels[m.ChannelID] {
			add("channel_models", m.ID, "missing_channel", m.ChannelID)
		}
	}
	for _, g := range d.ChannelGrants {
		if !models[g.ChannelModelID] {
			add("channel_grants", g.ID, "missing_model", g.ChannelModelID)
		}
		if !keys[g.ChannelKeyID] {
			add("channel_grants", g.ID, "missing_key", g.ChannelKeyID)
		}
		if models[g.ChannelModelID] && keys[g.ChannelKeyID] && modelParents[g.ChannelModelID] != keyParents[g.ChannelKeyID] {
			add("channel_grants", g.ID, "cross_channel_grant", g.ChannelKeyID)
		}
	}
	for _, i := range d.GroupItems {
		itemParents[i.ID] = i.GroupID
		if !groups[i.GroupID] {
			add("group_items", i.ID, "missing_group", i.GroupID)
		}
		if !grants[i.ChannelGrantID] {
			add("group_items", i.ID, "missing_grant", i.ChannelGrantID)
		}
	}
	for _, g := range d.Groups {
		if g.CategoryID != nil && !categories[*g.CategoryID] {
			add("groups", g.ID, "missing_category", *g.CategoryID)
		}
		if g.ActiveItemID != 0 && (!items[g.ActiveItemID] || itemParents[g.ActiveItemID] != g.ID) {
			add("groups", g.ID, "invalid_active_item", g.ActiveItemID)
		}
	}
	seenHours := map[int]bool{}
	for _, h := range d.StatsHourly {
		if h.Hour < 0 || h.Hour > 23 {
			add("stats_hourlies", h.Hour, "invalid_hour", 0)
		}
		if seenHours[h.Hour] {
			add("stats_hourlies", h.Hour, "duplicate_hour", 0)
		}
		seenHours[h.Hour] = true
	}
	if len(d.StatsTotal) > 1 {
		add("stats_totals", 1, "duplicate_total", 0)
	}
	for _, t := range d.StatsTotal {
		if t.ID != 1 {
			add("stats_totals", t.ID, "invalid_total_id", 0)
		}
	}
	seenStats := map[int]bool{}
	for _, s := range d.StatsAPIKey {
		if s.APIKeyID <= 0 {
			add("stats_api_keys", s.APIKeyID, "invalid_id", 0)
		}
		if seenStats[s.APIKeyID] {
			add("stats_api_keys", s.APIKeyID, "duplicate_id", 0)
		}
		seenStats[s.APIKeyID] = true
		if !apiKeys[s.APIKeyID] {
			add("stats_api_keys", s.APIKeyID, "missing_api_key", s.APIKeyID)
		}
	}
	archiveIDs := map[string]bool{}
	for _, archive := range d.StatsHourlyArchives {
		if archive.Validate() != nil {
			add("stats_hourly_archives", 0, "invalid_archive", 0)
		}
		if archiveIDs[archive.ID] {
			add("stats_hourly_archives", 0, "duplicate_archive", 0)
		}
		archiveIDs[archive.ID] = true
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Table != issues[j].Table {
			return issues[i].Table < issues[j].Table
		}
		if issues[i].ID != issues[j].ID {
			return issues[i].ID < issues[j].ID
		}
		return issues[i].Code < issues[j].Code
	})
	return issues
}

func collect[T any](rows []T, id func(T) int) []int {
	result := make([]int, len(rows))
	for i, v := range rows {
		result[i] = id(v)
	}
	return result
}
