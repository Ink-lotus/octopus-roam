package migrate

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func init() {
	// Repair physical keys before AutoMigrate: the drivers skip primary-key
	// changes, or rebuild SQLite tables without retaining all schema objects.
	RegisterBeforeAutoMigration(Migration{Version: 17, Up: migrateStatisticsBusinessKeys})
}

type statisticsBusinessKey struct{ table, column string }

func statisticsBusinessKeys(db *gorm.DB) ([]statisticsBusinessKey, error) {
	keys := make([]statisticsBusinessKey, 0, 4)
	for _, value := range []any{&model.StatsTotal{}, &model.StatsHourly{}, &model.StatsAPIKey{}, &MigrationRecord{}} {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(value); err != nil {
			return nil, err
		}
		if len(stmt.Schema.PrimaryFields) != 1 || stmt.Schema.PrimaryFields[0].AutoIncrement {
			return nil, fmt.Errorf("%s must have one non-generated business key", stmt.Table)
		}
		keys = append(keys, statisticsBusinessKey{stmt.Table, stmt.Schema.PrimaryFields[0].DBName})
	}
	return keys, nil
}

func schemaIdentifier(db *gorm.DB, name string) string {
	var quoted strings.Builder
	db.Dialector.QuoteTo(&quoted, name)
	return quoted.String()
}

func migrateStatisticsBusinessKeys(db *gorm.DB) error {
	if db == nil || db.Dialector == nil {
		return fmt.Errorf("db is nil")
	}
	keys, err := statisticsBusinessKeys(db)
	if err != nil {
		return err
	}
	repair := func(conn *gorm.DB) error {
		for _, key := range keys {
			columns, err := physicalTableColumns(conn, key.table)
			if err != nil {
				return err
			}
			if columns == nil {
				continue // A new database is created from the corrected Go tags.
			}
			if !columns[key.column] {
				return fmt.Errorf("%s is missing business key %s", key.table, key.column)
			}
			switch conn.Dialector.Name() {
			case "postgres":
				err = repairPostgresBusinessKey(conn, key)
			case "mysql":
				err = repairMySQLBusinessKey(conn, key)
			case "sqlite":
				err = repairSQLiteBusinessKey(conn, key)
			default:
				err = fmt.Errorf("unsupported business-key database")
			}
			if err != nil {
				return fmt.Errorf("repair %s.%s: %w", key.table, key.column, err)
			}
		}
		return nil
	}
	if db.Dialector.Name() == "mysql" {
		// ALTER TABLE implicitly commits in MySQL. Inspect each column again on
		// retry; never present the four DDL steps as one atomic transaction.
		return repair(db)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "sqlite" {
			var enabled int
			if err := tx.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil {
				return err
			}
			if enabled != 1 {
				return fmt.Errorf("SQLite foreign key enforcement must remain enabled")
			}
			// NO ACTION / RESTRICT references remain checked at commit while the
			// same-name table is replaced. Cascading references are checked below.
			if err := tx.Exec("PRAGMA defer_foreign_keys = ON").Error; err != nil {
				return err
			}
		}
		if err := repair(tx); err != nil {
			return err
		}
		if tx.Dialector.Name() == "sqlite" {
			rows, err := tx.Raw("PRAGMA foreign_key_check").Rows()
			if err != nil {
				return err
			}
			if rows.Next() {
				_ = rows.Close()
				return fmt.Errorf("SQLite foreign key check failed; business-key changes rolled back")
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
			// A same-name rebuild leaves SQLite's deferred DROP counters even
			// after the referenced rows exist again. Validate the complete final
			// graph above before restoring immediate enforcement. foreign_keys
			// stays ON throughout; subsequent statements are checked normally.
			return tx.Exec("PRAGMA defer_foreign_keys = OFF").Error
		}
		return nil
	})
}

func repairPostgresBusinessKey(db *gorm.DB, key statisticsBusinessKey) error {
	var column struct {
		OID      uint32 `gorm:"column:table_oid"`
		Number   int    `gorm:"column:column_number"`
		Schema   string `gorm:"column:schema_name"`
		Table    string `gorm:"column:table_name"`
		Identity string `gorm:"column:identity_kind"`
	}
	result := db.Raw(`SELECT c.oid AS table_oid, a.attnum AS column_number,
		n.nspname AS schema_name, c.relname AS table_name, a.attidentity AS identity_kind
		FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
		JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
		WHERE c.oid=to_regclass(?) AND a.attname=? AND NOT a.attisdropped`, schemaIdentifier(db, key.table), key.column).Scan(&column)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("business key metadata is missing")
	}
	table := schemaIdentifier(db, column.Schema) + "." + schemaIdentifier(db, column.Table)
	if err := db.Exec("LOCK TABLE " + table + " IN ACCESS EXCLUSIVE MODE").Error; err != nil {
		return err
	}
	var sequences []struct {
		OID    uint32 `gorm:"column:sequence_oid"`
		Schema string `gorm:"column:schema_name"`
		Name   string `gorm:"column:sequence_name"`
	}
	if err := db.Raw(`SELECT s.oid AS sequence_oid, n.nspname AS schema_name, s.relname AS sequence_name
		FROM pg_catalog.pg_depend d JOIN pg_catalog.pg_class s ON s.oid=d.objid
		JOIN pg_catalog.pg_namespace n ON n.oid=s.relnamespace
		WHERE d.classid='pg_class'::regclass AND d.refclassid='pg_class'::regclass
		AND d.refobjid=? AND d.refobjsubid=? AND d.deptype IN ('a','i') AND s.relkind='S'`,
		column.OID, column.Number).Scan(&sequences).Error; err != nil {
		return err
	}
	if column.Identity != "" {
		for _, sequence := range sequences {
			var dependencies int64
			if err := db.Raw(`SELECT COUNT(*) FROM pg_catalog.pg_depend d
				WHERE d.refclassid='pg_class'::regclass AND d.refobjid=?
				AND NOT (d.classid='pg_attrdef'::regclass AND d.objid IN
				(SELECT oid FROM pg_catalog.pg_attrdef WHERE adrelid=? AND adnum=?))`,
				sequence.OID, column.OID, column.Number).Scan(&dependencies).Error; err != nil {
				return err
			}
			if dependencies != 0 {
				return fmt.Errorf("identity sequence has other dependents; refusing its implicit deletion")
			}
		}
		// PostgreSQL drops the private identity sequence with the identity.
		return db.Exec("ALTER TABLE " + table + " ALTER COLUMN " + schemaIdentifier(db, key.column) + " DROP IDENTITY").Error
	}
	if err := db.Exec("ALTER TABLE " + table + " ALTER COLUMN " + schemaIdentifier(db, key.column) + " DROP DEFAULT").Error; err != nil {
		return err
	}
	for _, sequence := range sequences {
		// Keep the serial sequence, including defaults in other objects that
		// use it. Only remove its ownership of this non-generated column.
		name := schemaIdentifier(db, sequence.Schema) + "." + schemaIdentifier(db, sequence.Name)
		if err := db.Exec("ALTER SEQUENCE " + name + " OWNED BY NONE").Error; err != nil {
			return err
		}
	}
	return nil
}

func repairMySQLBusinessKey(db *gorm.DB, key statisticsBusinessKey) error {
	var column struct {
		Type     string         `gorm:"column:column_type"`
		Extra    string         `gorm:"column:extra"`
		Nullable string         `gorm:"column:is_nullable"`
		Comment  string         `gorm:"column:column_comment"`
		Default  sql.NullString `gorm:"column:column_default"`
	}
	result := db.Raw(`SELECT column_type, extra, is_nullable, column_comment, column_default
		FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?`,
		key.table, key.column).Scan(&column)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("business key metadata is missing")
	}
	if !strings.Contains(strings.ToLower(column.Extra), "auto_increment") {
		return nil
	}
	if !regexp.MustCompile(`(?i)^(tinyint|smallint|mediumint|int|integer|bigint)(\([0-9]+\))?( unsigned)?( zerofill)?$`).MatchString(column.Type) ||
		column.Nullable != "NO" || column.Default.Valid {
		return fmt.Errorf("unexpected generated-key definition; refusing to guess its replacement")
	}
	extra := strings.TrimSpace(strings.ReplaceAll(strings.ToLower(column.Extra), "auto_increment", ""))
	if extra != "" && extra != "invisible" {
		return fmt.Errorf("unsupported generated-key attributes")
	}
	statement := "ALTER TABLE " + schemaIdentifier(db, key.table) + " MODIFY COLUMN " + schemaIdentifier(db, key.column) +
		" " + column.Type + " NOT NULL COMMENT " + db.Dialector.Explain("?", column.Comment)
	if extra != "" {
		statement += " " + extra
	}
	return db.Exec(statement).Error
}

// Only SQL keywords outside quoted strings, identifiers and comments may be
// changed. In particular a literal/default containing AUTOINCREMENT is retained.
func sqliteBusinessKeyDDL(ddl, replacement string) (string, bool, error) {
	opening, start, end := -1, -1, -1
	for i := 0; i < len(ddl); {
		c := ddl[i]
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			closing := c
			if c == '[' {
				closing = ']'
			}
			i++
			closed := false
			for i < len(ddl) {
				if ddl[i] == closing {
					if closing != ']' && i+1 < len(ddl) && ddl[i+1] == closing {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return "", false, fmt.Errorf("unterminated SQLite schema quote")
			}
			continue
		}
		if i+1 < len(ddl) && ddl[i:i+2] == "--" {
			for i < len(ddl) && ddl[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(ddl) && ddl[i:i+2] == "/*" {
			closing := strings.Index(ddl[i+2:], "*/")
			if closing < 0 {
				return "", false, fmt.Errorf("unterminated SQLite schema comment")
			}
			i += closing + 4
			continue
		}
		if c == '(' && opening < 0 {
			opening = i
		}
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
			word := i
			for i < len(ddl) && (ddl[i] == '_' || ddl[i] >= 'a' && ddl[i] <= 'z' || ddl[i] >= 'A' && ddl[i] <= 'Z' || ddl[i] >= '0' && ddl[i] <= '9') {
				i++
			}
			if opening >= 0 && strings.EqualFold(ddl[word:i], "AUTOINCREMENT") {
				if start >= 0 {
					return "", false, fmt.Errorf("ambiguous SQLite AUTOINCREMENT definition")
				}
				start, end = word, i
			}
			continue
		}
		i++
	}
	if opening < 0 {
		return "", false, fmt.Errorf("SQLite table definition is missing")
	}
	if start < 0 {
		return ddl, false, nil
	}
	return "CREATE TABLE " + replacement + " " + ddl[opening:start] + ddl[end:], true, nil
}

func repairSQLiteBusinessKey(db *gorm.DB, key statisticsBusinessKey) error {
	var ddl string
	if err := db.Raw("SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", key.table).Scan(&ddl).Error; err != nil {
		return err
	}
	temporary := "__b02_17_" + key.table
	newDDL, rebuild, err := sqliteBusinessKeyDDL(ddl, schemaIdentifier(db, temporary))
	if err != nil || !rebuild {
		return err
	}
	var tables []string
	if err := db.Raw("SELECT name FROM sqlite_schema WHERE type='table'").Scan(&tables).Error; err != nil {
		return err
	}
	for _, table := range tables {
		if table == temporary {
			return fmt.Errorf("SQLite replacement table already exists")
		}
		var references []struct {
			Table  string `gorm:"column:table"`
			Delete string `gorm:"column:on_delete"`
		}
		if err := db.Raw("PRAGMA foreign_key_list(" + schemaIdentifier(db, table) + ")").Scan(&references).Error; err != nil {
			return err
		}
		for _, reference := range references {
			if strings.EqualFold(reference.Table, key.table) && reference.Delete != "NO ACTION" && reference.Delete != "RESTRICT" {
				return fmt.Errorf("%s has a cascading reference to this key; refusing a destructive SQLite rebuild", table)
			}
		}
	}
	var objects []struct {
		SQL sql.NullString `gorm:"column:sql"`
	}
	if err := db.Raw("SELECT sql FROM sqlite_schema WHERE tbl_name=? AND type IN ('index','trigger') ORDER BY type,name", key.table).Scan(&objects).Error; err != nil {
		return err
	}
	var columns []struct {
		Name   string `gorm:"column:name"`
		Hidden int    `gorm:"column:hidden"`
	}
	if err := db.Raw("PRAGMA table_xinfo(" + schemaIdentifier(db, key.table) + ")").Scan(&columns).Error; err != nil {
		return err
	}
	names := make([]string, 0, len(columns))
	for _, column := range columns {
		if column.Hidden == 0 {
			names = append(names, schemaIdentifier(db, column.Name))
		}
	}
	var before, after int64
	table := schemaIdentifier(db, key.table)
	if err := db.Raw("SELECT COUNT(*) FROM " + table).Scan(&before).Error; err != nil {
		return err
	}
	if err := db.Exec(newDDL).Error; err != nil {
		return err
	}
	fields := strings.Join(names, ",")
	if err := db.Exec("INSERT INTO " + schemaIdentifier(db, temporary) + " (" + fields + ") SELECT " + fields + " FROM " + table).Error; err != nil {
		return err
	}
	if err := db.Exec("DROP TABLE " + table).Error; err != nil {
		return err
	}
	if err := db.Exec("ALTER TABLE " + schemaIdentifier(db, temporary) + " RENAME TO " + table).Error; err != nil {
		return err
	}
	for _, object := range objects {
		if object.SQL.Valid {
			if err := db.Exec(object.SQL.String).Error; err != nil {
				return err
			}
		}
	}
	if err := db.Raw("SELECT COUNT(*) FROM " + table).Scan(&after).Error; err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("SQLite business-key rebuild changed the row count")
	}
	return nil
}
