package op

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/Ink-lotus/octopus-roam/internal/db/sequence"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// Lock every range before reading the local graph. PostgreSQL uses B01's table
// locks and READ COMMITTED; InnoDB uses full primary-key next-key ranges at
// REPEATABLE READ, including empty ranges; SQLite reserves its single writer.
// External transactions can still deadlock/lose a serialization race: abort the
// whole import, never retry a partially observed plan or claim multiwriter support.
func lockBackupTables(tx *gorm.DB) error {
	if err := sequence.LockForImport(tx); err != nil {
		return backupDatabaseError("entity_sequences", 0, err)
	}
	switch tx.Dialector.Name() {
	case "sqlite":
		return tx.Exec("UPDATE channels SET id = id WHERE 1 = 0").Error
	case "postgres":
		return tx.Exec("LOCK TABLE llm_infos, settings, stats_totals, stats_dailies, stats_hourlies, stats_api_keys IN SHARE ROW EXCLUSIVE MODE").Error
	case "mysql":
		// Match the parent/child order of sequence.LockForImport and ordinary writes.
		for _, row := range []any{&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{}, &model.GroupCategory{}, &model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.WebhookEndpoint{}, &model.LLMInfo{}, &model.Setting{}, &model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{}} {
			s, err := backupSchema(tx, row)
			if err != nil {
				return err
			}
			values := reflect.New(reflect.SliceOf(reflect.TypeOf(row).Elem())).Interface()
			if err := tx.Model(row).Select(s.PrimaryFields[0].DBName).Order(clause.OrderByColumn{Column: clause.Column{Name: s.PrimaryFields[0].DBName}}).
				Clauses(clause.Locking{Strength: "UPDATE"}).Find(values).Error; err != nil {
				return backupDatabaseError(s.Table, 0, err)
			}
		}
	}
	return nil
}

type backupWritePlan struct {
	table    backupTable
	schema   *schema.Schema
	existing map[any]reflect.Value
	dialect  string
}

func planBackupWrites(tx *gorm.DB, incoming, local *model.DBDump) ([]backupWritePlan, error) {
	plans := []backupWritePlan{}
	localTables := backupTables(local)
	for index, table := range backupTables(incoming) {
		s, err := backupSchema(tx, table.rows)
		if err != nil {
			return nil, backupDatabaseError(table.name, 0, err)
		}
		plan := backupWritePlan{table: table, schema: s, existing: map[any]reflect.Value{}, dialect: tx.Dialector.Name()}
		localRows := reflect.ValueOf(localTables[index].rows)
		for i := 0; i < localRows.Len(); i++ {
			row := localRows.Index(i)
			plan.existing[backupField(s, row, table.key)] = row
		}
		rows := reflect.ValueOf(table.rows)
		for i := 0; i < rows.Len(); i++ {
			row := rows.Index(i)
			key := backupField(s, row, table.key)
			id := backupRowID(s, row)
			if old, ok := plan.existing[key]; ok && !table.replace && !compatibleBackupRow(s, row, old, table) {
				return nil, backupProblem(ErrBackupConflict, table.name, id, "same ID has incompatible persisted configuration or ownership")
			}
			// The database performs equality using its real collation. Even a
			// primary string key with a different spelling must not hit another row.
			for _, columns := range append([][]string{{table.key}}, table.unique...) {
				query := tx.Table(table.name)
				for _, column := range columns {
					query = query.Where(clause.Eq{Column: clause.Column{Name: column}, Value: backupField(s, row, column)})
				}
				found := reflect.New(row.Type())
				err := query.Take(found.Interface()).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				if err != nil {
					return nil, backupDatabaseError(table.name, id, err)
				}
				if !reflect.DeepEqual(backupField(s, found.Elem(), table.key), key) {
					return nil, backupProblem(ErrBackupConflict, table.name, id, "unique key belongs to another ID or spelling (database collation)")
				}
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func writeBackupRows(tx *gorm.DB, plans []backupWritePlan, result *model.DBImportResult) error {
	for _, plan := range plans {
		table, s := plan.table, plan.schema
		result.RowsAffected[backupResultTable(table.name)] = 0
		rows := reflect.ValueOf(table.rows)
		for i := 0; i < rows.Len(); i++ {
			row := rows.Index(i)
			key := backupField(s, row, table.key)
			id := backupRowID(s, row)
			_, exists := plan.existing[key]
			if !exists {
				if err := insertBackupRow(tx, row.Addr().Interface()); err != nil {
					return backupDatabaseError(table.name, id, err)
				}
				result.RowsAffected[backupResultTable(table.name)]++
				continue
			}
			columns := slices.Clone(table.updates)
			if table.replace {
				for _, f := range s.Fields {
					if f.DBName != "" && f.DBName != table.key {
						columns = append(columns, f.DBName)
					}
				}
			}
			if len(columns) == 0 {
				continue
			}
			query := tx.Model(row.Addr().Interface()).Where(clause.Eq{Column: clause.Column{Name: table.key}, Value: key}).Omit(clause.Associations)
			for _, parent := range []string{"channel_id", "channel_model_id", "channel_key_id", "group_id", "channel_grant_id"} {
				if s.FieldsByDBName[parent] != nil {
					query = query.Where(clause.Eq{Column: clause.Column{Name: parent}, Value: backupField(s, row, parent)})
				}
			}
			if err := query.Select(columns).Updates(row.Addr().Interface()).Error; err != nil {
				return backupDatabaseError(table.name, id, err)
			}
			// Keep the existing UI's channel insert count; other mutable tables
			// report logical accepted rows, independent of driver RowsAffected.
			if table.name != "channels" {
				result.RowsAffected[backupResultTable(table.name)]++
			}
		}
	}
	return nil
}

// A successful statement (or RowsAffected) is not proof that every requested
// row and value survived triggers/defaults. Check the exact write contract in
// the same transaction, including retained local state and imported metrics.
func verifyBackupWrites(plans []backupWritePlan, final *model.DBDump, categories []model.GroupCategory) error {
	finalTables := backupTables(final)
	for index, plan := range plans {
		s, table := plan.schema, plan.table
		stored := map[any]reflect.Value{}
		rows := reflect.ValueOf(finalTables[index].rows)
		for i := 0; i < rows.Len(); i++ {
			row := rows.Index(i)
			stored[backupField(s, row, table.key)] = row
		}
		input := reflect.ValueOf(table.rows)
		for i := 0; i < input.Len(); i++ {
			row := input.Index(i)
			key := backupField(s, row, table.key)
			actual, ok := stored[key]
			if !ok {
				return backupProblem(ErrBackupWrite, table.name, backupRowID(s, row), "validated row is missing after write")
			}
			old, existed := plan.existing[key]
			for _, field := range s.Fields {
				if field.DBName == "" {
					continue
				}
				wanted := row
				if existed && !table.replace && !slices.Contains(table.updates, field.DBName) {
					wanted = old
				}
				value := backupField(s, wanted, field.DBName)
				// PostgreSQL REAL persists a float4 and pgx returns float64(float32).
				// Compare the exact representable value, without a loose tolerance
				// that could hide a trigger's unexpected change to statistics.
				if plan.dialect == "postgres" && field.DataType == "real" {
					if number, ok := value.(float64); ok {
						value = float64(float32(number))
					}
				}
				if !reflect.DeepEqual(backupField(s, actual, field.DBName), value) {
					return backupProblem(ErrBackupWrite, table.name, backupRowID(s, row), "persisted field differs from validated write: "+field.DBName)
				}
			}
		}
	}
	byID := map[int]string{}
	for _, row := range final.GroupCategories {
		byID[row.ID] = row.Name
	}
	for _, row := range categories {
		if byID[row.ID] != row.Name {
			return backupProblem(ErrBackupWrite, "group_categories", row.ID, "persisted category differs from validated write")
		}
	}
	return nil
}

func backupResultTable(table string) string {
	switch table {
	case "stats_totals":
		return "stats_total"
	case "stats_dailies":
		return "stats_daily"
	case "stats_hourlies":
		return "stats_hourly"
	case "stats_api_keys":
		return "stats_api_key"
	}
	return table
}

func insertBackupRow(tx *gorm.DB, row any) error {
	switch v := row.(type) {
	case *model.Channel:
		row = &backupChannelRow{Channel: *v, Enabled: v.Enabled}
	case *model.ChannelKey:
		row = &backupChannelKeyRow{ChannelKey: *v, Enabled: v.Enabled}
	case *model.APIKey:
		row = &backupAPIKeyRow{APIKey: *v, Enabled: v.Enabled}
	}
	return tx.Omit(clause.Associations).Create(row).Error
}

// Plan category exceptions without writing rows or consuming a sequence. All
// free explicit IDs are reserved first. Collisions receive checked IDs above
// the entire reserved range; B01 synchronizes the sequence before commit.
func planBackupCategories(local, input []model.BackupGroupCategory) (map[int]int, []model.GroupCategory, error) {
	byKey, used := map[string]int{}, map[int]bool{}
	maxID := 0
	for _, c := range local {
		_, key, err := model.NormalizeGroupCategoryName(c.Name)
		if err != nil {
			return nil, nil, err
		}
		byKey[key], used[c.ID] = c.ID, true
		if c.ID > maxID {
			maxID = c.ID
		}
	}
	input = slices.Clone(input)
	sort.Slice(input, func(i, j int) bool { return input[i].ID < input[j].ID })
	mapping := map[int]int{}
	rows := []model.GroupCategory{}
	pending := []model.BackupGroupCategory{}
	for _, c := range input {
		name, key, err := model.NormalizeGroupCategoryName(c.Name)
		if err != nil {
			return nil, nil, err
		}
		if c.ID > maxID {
			maxID = c.ID
		}
		if id, ok := byKey[key]; ok {
			mapping[c.ID] = id
			continue
		}
		if used[c.ID] {
			pending = append(pending, c)
			continue
		}
		mapping[c.ID], byKey[key], used[c.ID] = c.ID, c.ID, true
		rows = append(rows, model.GroupCategory{ID: c.ID, Name: name, NameKey: key})
	}
	for _, c := range pending {
		if maxID == int(^uint(0)>>1) {
			return nil, nil, fmt.Errorf("category ID range exhausted")
		}
		maxID++
		name, key, _ := model.NormalizeGroupCategoryName(c.Name)
		mapping[c.ID] = maxID
		rows = append(rows, model.GroupCategory{ID: maxID, Name: name, NameKey: key})
	}
	return mapping, rows, nil
}

func unionBackupRows[T any](local, input []T, id func(T) int) []T {
	rows := slices.Clone(local)
	seen := map[int]bool{}
	for _, v := range local {
		seen[id(v)] = true
	}
	for _, v := range input {
		if !seen[id(v)] {
			rows = append(rows, v)
		}
	}
	return rows
}

func expectedBackupGraph(local, input *model.DBDump, categories []model.GroupCategory) *model.DBDump {
	result := *local
	result.Channels = unionBackupRows(local.Channels, input.Channels, func(v model.Channel) int { return v.ID })
	result.ChannelKeys = unionBackupRows(local.ChannelKeys, input.ChannelKeys, func(v model.ChannelKey) int { return v.ID })
	result.ChannelModels = unionBackupRows(local.ChannelModels, input.ChannelModels, func(v model.ChannelModel) int { return v.ID })
	result.ChannelGrants = unionBackupRows(local.ChannelGrants, input.ChannelGrants, func(v model.ChannelGrant) int { return v.ID })
	result.Groups = unionBackupRows(local.Groups, input.Groups, func(v model.Group) int { return v.ID })
	result.GroupItems = unionBackupRows(local.GroupItems, input.GroupItems, func(v model.GroupItem) int { return v.ID })
	result.APIKeys = unionBackupRows(local.APIKeys, input.APIKeys, func(v model.APIKey) int { return v.ID })
	result.StatsAPIKey = unionBackupRows(local.StatsAPIKey, input.StatsAPIKey, func(v model.StatsAPIKey) int { return v.APIKeyID })
	result.GroupCategories = slices.Clone(local.GroupCategories)
	for _, v := range categories {
		result.GroupCategories = append(result.GroupCategories, model.BackupGroupCategory{ID: v.ID, Name: v.Name})
	}
	return &result
}
