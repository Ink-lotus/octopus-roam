package migrate

import (
	"fmt"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type serverHourlyColumn struct {
	Name      string `gorm:"column:column_name"`
	SQLType   string `gorm:"column:sql_type"`
	Generated string `gorm:"column:generated_value"`
	Identity  string `gorm:"column:identity_kind"`
}

func serverHourlyColumns(tx *gorm.DB) ([]serverHourlyColumn, error) {
	var columns []serverHourlyColumn
	var err error
	switch tx.Dialector.Name() {
	case "mysql":
		err = tx.Raw(`SELECT column_name, LOWER(column_type) AS sql_type,
			COALESCE(generation_expression, '') AS generated_value, COALESCE(extra, '') AS identity_kind
			FROM information_schema.columns
			WHERE table_schema=DATABASE() AND table_name='stats_hourlies'
			ORDER BY ordinal_position`).Scan(&columns).Error
	case "postgres":
		err = tx.Raw(`SELECT a.attname AS column_name,
			pg_catalog.format_type(a.atttypid,a.atttypmod) AS sql_type,
			a.attgenerated AS generated, a.attidentity AS identity_kind
			FROM pg_catalog.pg_attribute a
			WHERE a.attrelid=to_regclass('stats_hourlies') AND a.attnum>0 AND NOT a.attisdropped
			ORDER BY a.attnum`).Scan(&columns).Error
	default:
		return nil, fmt.Errorf("unsupported hourly quarantine database")
	}
	if err != nil {
		// The driver error is limited to the metadata query and contains no DSN
		// or row values; retain it so an administrator can repair permissions or
		// a server-version mismatch without guessing.
		return nil, fmt.Errorf("cannot inspect hourly column metadata: %w", err)
	}
	wanted := model.HourlyArchiveColumns()
	if len(columns) != len(wanted) {
		return nil, fmt.Errorf("hourly quarantine requires review of unknown columns")
	}
	ordered := make([]serverHourlyColumn, len(wanted))
	for i, name := range wanted {
		found := false
		for _, col := range columns {
			if col.Name != name {
				continue
			}
			if found || col.Generated != "" || col.Identity != "" {
				return nil, fmt.Errorf("hourly quarantine requires review of generated columns")
			}
			if _, err := model.NewServerHourlyArchiveCell(tx.Dialector.Name()+"-migration-18", name, col.SQLType, nil); err != nil {
				return nil, err
			}
			found, ordered[i] = true, col
		}
		if !found {
			return nil, fmt.Errorf("hourly quarantine requires review of unknown columns")
		}
	}
	return ordered, nil
}

func checkServerHourlyQuarantine(tx *gorm.DB) error {
	switch tx.Dialector.Name() {
	case "mysql":
		if err := checkMySQLHourlyQuarantine(tx); err != nil {
			return err
		}
	case "postgres":
		if err := checkPostgresHourlyQuarantine(tx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported hourly quarantine database")
	}
	if _, err := serverHourlyColumns(tx); err != nil {
		return err
	}
	// Also protect existing archives against a concurrent import/change. The
	// MySQL primary-key range read retains its next-key locks until commit.
	var existing []model.StatsHourlyArchive
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Order("id").Find(&existing).Error; err != nil {
		return err
	}
	for _, row := range existing {
		if err := row.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func checkMySQLHourlyQuarantine(tx *gorm.DB) error {
	var isolation string
	if err := tx.Raw("SELECT @@transaction_isolation").Scan(&isolation).Error; err != nil {
		return err
	}
	if isolation != "REPEATABLE-READ" && isolation != "SERIALIZABLE" {
		return fmt.Errorf("hourly quarantine requires MySQL repeatable-read or serializable isolation for range locks")
	}
	var engines []struct{ Engine string }
	if err := tx.Raw(`SELECT engine FROM information_schema.tables WHERE table_schema=DATABASE()
		AND table_name IN ('stats_hourlies','stats_hourly_archives','migration_records') AND table_type='BASE TABLE'`).Scan(&engines).Error; err != nil {
		return err
	}
	if len(engines) != 3 {
		return fmt.Errorf("hourly quarantine requires three transactional base tables")
	}
	for _, row := range engines {
		if !strings.EqualFold(row.Engine, "InnoDB") {
			return fmt.Errorf("hourly quarantine requires InnoDB for source, archive and migration records")
		}
	}
	// The INFORMATION_SCHEMA trigger view hides triggers without TRIGGER
	// privilege. An empty result must never be mistaken for complete metadata.
	var visible int64
	if err := tx.Raw(`SELECT COUNT(DISTINCT table_name) FROM (
		SELECT table_name FROM information_schema.tables t WHERE t.table_schema=DATABASE()
		AND t.table_name IN ('stats_hourlies','stats_hourly_archives','migration_records')
		AND EXISTS (SELECT 1 FROM information_schema.user_privileges p WHERE p.privilege_type='TRIGGER'
		AND p.grantee=CONCAT(QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',1)),'@',QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',-1))))
		UNION ALL SELECT table_name FROM information_schema.tables t WHERE t.table_schema=DATABASE()
		AND t.table_name IN ('stats_hourlies','stats_hourly_archives','migration_records')
		AND EXISTS (SELECT 1 FROM information_schema.schema_privileges p WHERE p.table_schema=DATABASE() AND p.privilege_type='TRIGGER'
		AND p.grantee=CONCAT(QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',1)),'@',QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',-1))))
		UNION ALL SELECT table_name FROM information_schema.table_privileges p WHERE p.table_schema=DATABASE()
		AND p.table_name IN ('stats_hourlies','stats_hourly_archives','migration_records') AND p.privilege_type='TRIGGER'
		AND p.grantee=CONCAT(QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',1)),'@',QUOTE(SUBSTRING_INDEX(CURRENT_USER(),'@',-1)))
	) visible_tables`).Scan(&visible).Error; err != nil {
		return fmt.Errorf("cannot verify MySQL trigger metadata visibility")
	}
	if visible != 3 {
		return fmt.Errorf("hourly quarantine requires visible MySQL trigger metadata; review TRIGGER privileges")
	}
	var triggers int64
	if err := tx.Raw(`SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE()
		AND event_object_table IN ('stats_hourlies','stats_hourly_archives','migration_records')`).Scan(&triggers).Error; err != nil {
		return err
	}
	if triggers != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom triggers")
	}
	// INNODB_FOREIGN includes references from other databases. The ordinary
	// KEY_COLUMN_USAGE view can hide these and cannot justify a safe DELETE.
	var refs int64
	if err := tx.Raw(`SELECT COUNT(*) FROM information_schema.innodb_foreign
		WHERE ref_name IN (CONCAT(DATABASE(),'/stats_hourlies'),CONCAT(DATABASE(),'/stats_hourly_archives'))`).Scan(&refs).Error; err != nil {
		return fmt.Errorf("hourly quarantine cannot inspect all MySQL foreign keys; reviewed metadata access (PROCESS) is required")
	}
	if refs != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom references")
	}
	var primary []string
	if err := tx.Raw(`SELECT column_name FROM information_schema.statistics WHERE table_schema=DATABASE()
		AND table_name='stats_hourlies' AND index_name='PRIMARY' ORDER BY seq_in_index`).Scan(&primary).Error; err != nil {
		return err
	}
	if len(primary) != 1 || primary[0] != "hour" {
		return fmt.Errorf("hourly quarantine requires the original hourly primary key")
	}
	return nil
}

func checkPostgresHourlyQuarantine(tx *gorm.DB) error {
	// These transaction-scoped locks exclude writers and DDL, including phantoms
	// at READ COMMITTED. No LOCK TABLES or other implicit-commit DDL is used.
	if err := tx.Exec("LOCK TABLE stats_hourlies, stats_hourly_archives, migration_records IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
		return err
	}
	var unsafe int64
	if err := tx.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_class c WHERE c.oid IN
		(to_regclass('stats_hourlies'),to_regclass('stats_hourly_archives'),to_regclass('migration_records'))
		AND (c.relkind <> 'r' OR c.relrowsecurity OR c.relforcerowsecurity
		OR EXISTS (SELECT 1 FROM pg_catalog.pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid))`).Scan(&unsafe).Error; err != nil {
		return err
	}
	if unsafe != 0 {
		return fmt.Errorf("hourly quarantine requires review of PostgreSQL policies, partitions or inheritance")
	}
	if err := tx.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_trigger WHERE NOT tgisinternal AND tgrelid IN
		(to_regclass('stats_hourlies'),to_regclass('stats_hourly_archives'),to_regclass('migration_records'))`).Scan(&unsafe).Error; err != nil {
		return err
	}
	if unsafe != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom triggers")
	}
	if err := tx.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_rewrite WHERE ev_class IN
		(to_regclass('stats_hourlies'),to_regclass('stats_hourly_archives'),to_regclass('migration_records'))`).Scan(&unsafe).Error; err != nil {
		return err
	}
	if unsafe != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom rules")
	}
	if err := tx.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_constraint WHERE contype='f'
		AND confrelid IN (to_regclass('stats_hourlies'),to_regclass('stats_hourly_archives'))`).Scan(&unsafe).Error; err != nil {
		return err
	}
	if unsafe != 0 {
		return fmt.Errorf("hourly quarantine requires review of custom references")
	}
	return nil
}

func readServerHourlyArchives(tx *gorm.DB) ([]model.StatsHourlyArchive, error) {
	columns, err := serverHourlyColumns(tx)
	if err != nil {
		return nil, err
	}
	selects := make([]string, len(columns))
	for i, col := range columns {
		q := schemaIdentifier(tx, col.Name)
		selects[i] = q
		if tx.Dialector.Name() == "postgres" {
			switch col.SQLType {
			case "real":
				selects[i] = "pg_catalog.float4send(" + q + ")"
			case "double precision":
				selects[i] = "pg_catalog.float8send(" + q + ")"
			default:
				if col.Name == "date" {
					selects[i] = "pg_catalog.textsend(" + q + "::text)"
				}
			}
		}
	}
	query := "SELECT " + strings.Join(selects, ",") + " FROM stats_hourlies WHERE hour >= 24 ORDER BY hour FOR UPDATE"
	// Explicit preparation forces MySQL binary results even when the caller's
	// DSN enables interpolateParams. Its text protocol rounds some FLOAT values.
	statement, err := tx.Statement.ConnPool.PrepareContext(tx.Statement.Context, query)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare native hourly snapshot")
	}
	defer statement.Close()
	rows, err := statement.QueryContext(tx.Statement.Context)
	if err != nil {
		return nil, fmt.Errorf("cannot read native hourly snapshot")
	}
	defer rows.Close()
	result := []model.StatsHourlyArchive{}
	for rows.Next() {
		values, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("cannot scan native hourly snapshot")
		}
		cells := make([]model.HourlyArchiveCell, len(columns))
		for i, col := range columns {
			cells[i], err = model.NewServerHourlyArchiveCell(tx.Dialector.Name()+"-migration-18", col.Name, col.SQLType, values[i])
			if err != nil {
				return nil, err
			}
		}
		archive, err := model.NewHourlyArchive(tx.Dialector.Name()+"-migration-18", cells)
		if err != nil {
			return nil, err
		}
		result = append(result, archive)
	}
	return result, rows.Err()
}
