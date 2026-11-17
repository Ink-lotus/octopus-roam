package migrate

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func init() {
	// After 17, AutoMigrate and 16; the runner commits the archive, quarantine
	// and success record together. Earlier successful migrations remain valid.
	RegisterAfterAutoMigration(Migration{Version: 18, Up: migrateLegacyHourly, Atomic: true})
}

func migrateLegacyHourly(tx *gorm.DB) error {
	tables, err := tx.Migrator().GetTables()
	if err != nil {
		return fmt.Errorf("inspect hourly archive tables: %w", err)
	}
	// The archive schema is created by AutoMigrate before this transaction.
	// This keeps MySQL DDL implicit commits out of the atomic data operation.
	var existing []model.StatsHourlyArchive
	if err := tx.Order("id").Find(&existing).Error; err != nil {
		return err
	}
	for _, row := range existing {
		if err := row.Validate(); err != nil {
			return err
		}
	}
	var count int64
	if err := tx.Table("stats_hourlies").Where("hour >= 24").Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return nil
	}
	if tx.Dialector.Name() != "sqlite" {
		return fmt.Errorf("legacy hourly quarantine is supported only for SQLite; preserve the database and request a reviewed repair")
	}
	// Custom triggers/references can change unrelated rows on DELETE. Refuse
	// those schemas rather than disable constraints or guess their effects.
	if err := checkHourlyQuarantineSchema(tx, tables); err != nil {
		return err
	}
	if err := tx.Exec("UPDATE stats_hourlies SET hour = hour WHERE 1 = 0").Error; err != nil {
		return err
	}
	before, err := readHourlyArchives(tx)
	if err != nil {
		return err
	}
	if int64(len(before)) != count {
		return fmt.Errorf("hourly snapshot changed before quarantine")
	}
	for _, row := range before {
		var saved model.StatsHourlyArchive
		result := tx.Where("id = ?", row.ID).Limit(1).Find(&saved)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("archive hourly row: %w", err)
			}
		}
		if err := tx.Where("id = ?", row.ID).Take(&saved).Error; err != nil {
			return err
		}
		if saved != row || saved.Validate() != nil {
			return fmt.Errorf("hourly archive readback mismatch; quarantine rolled back")
		}
	}
	after, err := readHourlyArchives(tx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("hourly source changed during archival; quarantine rolled back")
	}
	result := tx.Exec("DELETE FROM stats_hourlies WHERE hour >= 24")
	if result.Error != nil {
		return fmt.Errorf("quarantine archived hourly rows: %w", result.Error)
	}
	if result.RowsAffected != count {
		return fmt.Errorf("hourly quarantine row count mismatch")
	}
	var remaining int64
	if err := tx.Table("stats_hourlies").Where("hour >= 24").Count(&remaining).Error; err != nil {
		return err
	}
	if remaining != 0 {
		return fmt.Errorf("hourly quarantine incomplete")
	}
	return nil
}

func checkHourlyQuarantineSchema(tx *gorm.DB, tables []string) error {
	var enabled int
	if err := tx.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil {
		return err
	}
	if enabled != 1 {
		return fmt.Errorf("hourly quarantine requires SQLite foreign key enforcement")
	}
	var triggers int64
	if err := tx.Raw("SELECT COUNT(*) FROM sqlite_schema WHERE type='trigger' AND tbl_name IN ('stats_hourlies','stats_hourly_archives')").Scan(&triggers).Error; err != nil {
		return err
	}
	if triggers != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom triggers")
	}
	for _, table := range tables {
		var refs []struct {
			Table string `gorm:"column:table"`
		}
		if err := tx.Raw("PRAGMA foreign_key_list(" + schemaIdentifier(tx, table) + ")").Scan(&refs).Error; err != nil {
			return err
		}
		for _, ref := range refs {
			if strings.EqualFold(ref.Table, "stats_hourlies") || strings.EqualFold(ref.Table, "stats_hourly_archives") {
				return fmt.Errorf("hourly quarantine requires review of custom references")
			}
		}
	}
	var cols []struct {
		Name   string
		Hidden int
	}
	if err := tx.Raw("PRAGMA table_xinfo(stats_hourlies)").Scan(&cols).Error; err != nil {
		return err
	}
	wanted := model.HourlyArchiveColumns()
	if len(cols) != len(wanted) {
		return fmt.Errorf("hourly quarantine requires review of unknown columns")
	}
	for _, col := range cols {
		found := false
		for _, name := range wanted {
			if col.Name == name {
				found = true
			}
		}
		if !found || col.Hidden != 0 {
			return fmt.Errorf("hourly quarantine requires review of unknown columns")
		}
	}
	return nil
}

func readHourlyArchives(tx *gorm.DB) ([]model.StatsHourlyArchive, error) {
	columns := model.HourlyArchiveColumns()
	selects := make([]string, len(columns)*2)
	for i, name := range columns {
		q := schemaIdentifier(tx, name)
		selects[i], selects[len(columns)+i] = q, "typeof("+q+")"
	}
	rows, err := tx.Raw("SELECT " + strings.Join(selects, ",") + " FROM stats_hourlies WHERE hour >= 24 ORDER BY hour").Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.StatsHourlyArchive{}
	for rows.Next() {
		values := make([]any, len(columns))
		types := make([]string, len(columns))
		dest := make([]any, len(columns)*2)
		for i := range columns {
			dest[i], dest[len(columns)+i] = &values[i], &types[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		cells := make([]model.HourlyArchiveCell, len(columns))
		for i, name := range columns {
			cells[i], err = model.NewHourlyArchiveCell(name, values[i])
			if err != nil {
				return nil, err
			}
			if cells[i].Type != types[i] {
				return nil, fmt.Errorf("hourly storage class changed while reading")
			}
		}
		archive, err := model.NewHourlyArchive("sqlite-migration-18", cells)
		if err != nil {
			return nil, err
		}
		result = append(result, archive)
	}
	return result, rows.Err()
}
