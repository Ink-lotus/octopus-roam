package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func TestStatisticsBusinessKeyDeclarationsRetainEntityGeneration(t *testing.T) {
	db := migrationTestDB(t)
	for _, tc := range []struct {
		value any
		auto  bool
	}{
		{&model.StatsTotal{}, false}, {&model.StatsHourly{}, false}, {&model.StatsAPIKey{}, false}, {&MigrationRecord{}, false},
		{&model.ChannelRefreshRecord{}, false}, {&model.Channel{}, true}, {&model.ChannelKey{}, true},
		{&model.ChannelModel{}, true}, {&model.ChannelGrant{}, true}, {&model.GroupCategory{}, true},
		{&model.Group{}, true}, {&model.GroupItem{}, true}, {&model.APIKey{}, true}, {&model.WebhookEndpoint{}, true}, {&model.User{}, true},
	} {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(tc.value); err != nil {
			t.Fatal(err)
		}
		if len(stmt.Schema.PrimaryFields) != 1 || stmt.Schema.PrimaryFields[0].AutoIncrement != tc.auto {
			t.Fatalf("%s generated-key declaration changed", stmt.Table)
		}
	}
}

func oldSQLiteBusinessKeys(t *testing.T, db *gorm.DB) []statisticsBusinessKey {
	t.Helper()
	keys, err := statisticsBusinessKeys(db)
	if err != nil {
		t.Fatal(err)
	}
	migrationExec(t, db, `CREATE TABLE b02_parent (id integer PRIMARY KEY)`)
	migrationExec(t, db, `INSERT INTO b02_parent VALUES (1)`)
	migrationExec(t, db, `CREATE TABLE b02_trigger_log (table_name text)`)
	for _, key := range keys {
		q := schemaIdentifier(db, key.table)
		migrationExec(t, db, "CREATE TABLE "+q+" ("+schemaIdentifier(db, key.column)+" integer PRIMARY KEY AUTOINCREMENT, payload text NOT NULL DEFAULT 'AUTOINCREMENT', parent_id integer NOT NULL REFERENCES b02_parent(id))")
		migrationExec(t, db, "CREATE UNIQUE INDEX "+schemaIdentifier(db, "b02_"+key.table)+" ON "+q+" (payload)")
		migrationExec(t, db, "CREATE TRIGGER "+schemaIdentifier(db, "b02_"+key.table+"_trigger")+" AFTER INSERT ON "+q+" BEGIN INSERT INTO b02_trigger_log VALUES ('"+key.table+"'); END")
		id := 1
		if key.column == "hour" {
			id = 0
		}
		migrationExec(t, db, "INSERT INTO "+q+" VALUES (?, ?, 1)", id, "kept-"+key.table)
	}
	return keys
}

func sqliteBusinessRows(t *testing.T, db *gorm.DB, keys []statisticsBusinessKey) string {
	t.Helper()
	rows := map[string][]map[string]any{}
	for _, key := range keys {
		var values []map[string]any
		if err := db.Table(key.table).Order(key.column).Find(&values).Error; err != nil {
			t.Fatal(err)
		}
		rows[key.table] = values
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMigration17SQLitePreservesRowsIndexesTriggersAndForeignKeys(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		t.Run(action, func(t *testing.T) {
			db := migrationTestDB(t)
			keys := oldSQLiteBusinessKeys(t, db)
			migrationExec(t, db, "CREATE TABLE b02_child (id integer PRIMARY KEY, hour integer REFERENCES stats_hourlies(hour) ON DELETE "+action+")")
			migrationExec(t, db, `INSERT INTO b02_child VALUES (7, 0)`)
			before := sqliteBusinessRows(t, db, keys)
			for range 2 {
				if err := migrateStatisticsBusinessKeys(db); err != nil {
					t.Fatal(err)
				}
			}
			if after := sqliteBusinessRows(t, db, keys); after != before {
				t.Fatalf("business rows changed: %s != %s", after, before)
			}
			for _, key := range keys {
				var ddl string
				if err := db.Raw("SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", key.table).Scan(&ddl).Error; err != nil {
					t.Fatal(err)
				}
				if _, rebuild, err := sqliteBusinessKeyDDL(ddl, "unused"); err != nil || rebuild || !strings.Contains(ddl, "DEFAULT 'AUTOINCREMENT'") {
					t.Fatalf("generated key or original default changed: %s, %v", ddl, err)
				}
				var count int64
				if err := db.Raw("SELECT COUNT(*) FROM sqlite_schema WHERE tbl_name=? AND name IN (?,?)", key.table, "b02_"+key.table, "b02_"+key.table+"_trigger").Scan(&count).Error; err != nil || count != 2 {
					t.Fatalf("index or trigger lost: count=%d, err=%v", count, err)
				}
				if err := db.Exec("INSERT INTO " + schemaIdentifier(db, key.table) + " VALUES (99, 'invalid-parent', 999)").Error; err == nil {
					t.Fatal("outgoing foreign key no longer enforced")
				}
				if err := db.Exec("INSERT INTO "+schemaIdentifier(db, key.table)+" VALUES (99, ?, 1)", "kept-"+key.table).Error; err == nil {
					t.Fatal("unique index no longer enforced")
				}
			}
			var child struct{ Hour int }
			if err := db.Table("b02_child").First(&child).Error; err != nil || child.Hour != 0 {
				t.Fatalf("incoming foreign key row changed: %+v, %v", child, err)
			}
			migrationExec(t, db, `INSERT INTO stats_hourlies VALUES (1, 'new-hour', 1)`)
			var count int64
			if err := db.Table("b02_trigger_log").Count(&count).Error; err != nil || count != 5 {
				t.Fatalf("trigger replayed or failed to run: count=%d, %v", count, err)
			}
			assertMigrationForeignKeys(t, db)
		})
	}
}

func TestMigration17SQLiteRejectsCascadingRebuildWithoutChangingRows(t *testing.T) {
	db := migrationTestDB(t)
	keys := oldSQLiteBusinessKeys(t, db)
	migrationExec(t, db, `CREATE TABLE b02_child (hour integer REFERENCES stats_hourlies(hour) ON DELETE CASCADE)`)
	migrationExec(t, db, `INSERT INTO b02_child VALUES (0)`)
	before := sqliteBusinessRows(t, db, keys)
	if err := migrateStatisticsBusinessKeys(db); err == nil || !strings.Contains(err.Error(), "cascading reference") {
		t.Fatalf("unsafe rebuild was not rejected: %v", err)
	}
	if sqliteBusinessRows(t, db, keys) != before {
		t.Fatal("failed rebuild changed business rows")
	}
	var count int64
	if err := db.Table("b02_child").Count(&count).Error; err != nil || count != 1 {
		t.Fatal("failed rebuild removed a child")
	}
	var ddl string
	if err := db.Raw("SELECT sql FROM sqlite_schema WHERE name='stats_totals'").Scan(&ddl).Error; err != nil {
		t.Fatal(err)
	}
	if _, rebuild, err := sqliteBusinessKeyDDL(ddl, "unused"); err != nil || !rebuild {
		t.Fatal("partial SQLite DDL did not roll back")
	}
}

func TestMigration17SQLiteFailureRecordAndRetry(t *testing.T) {
	db := migrationTestDB(t)
	if err := db.Exec(`CREATE TABLE migration_records (version integer PRIMARY KEY AUTOINCREMENT, status integer)`).Error; err != nil {
		t.Fatal(err)
	}
	for version := 3; version <= 16; version++ {
		migrationExec(t, db, `INSERT INTO migration_records VALUES (?, 1)`, version)
	}
	migrationExec(t, db, `CREATE TABLE stats_totals (id integer PRIMARY KEY AUTOINCREMENT, input_token integer)`)
	migrationExec(t, db, `CREATE TABLE stats_hourlies (hour integer PRIMARY KEY AUTOINCREMENT, date text NOT NULL)`)
	migrationExec(t, db, `INSERT INTO stats_totals VALUES (1, 100)`)
	migrationExec(t, db, `INSERT INTO stats_hourlies VALUES (0, '20261112')`)
	sentinel := errors.New("synthetic second-table failure")
	if err := db.Callback().Raw().Before("gorm:raw").Register("b02_fail_second_table", func(tx *gorm.DB) {
		if strings.HasPrefix(tx.Statement.SQL.String(), "CREATE TABLE `__b02_17_stats_hourlies`") {
			tx.AddError(sentinel)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := BeforeAutoMigrate(db); !errors.Is(err, sentinel) {
		t.Fatalf("real migration failure lost: %v", err)
	}
	var record MigrationRecord
	if err := db.First(&record, "version=17").Error; err != nil || record.Status != MigrationRecordStatusFailed {
		t.Fatalf("failed migration was marked successful: %+v, %v", record, err)
	}
	var ddl string
	if err := db.Raw("SELECT sql FROM sqlite_schema WHERE name='stats_totals'").Scan(&ddl).Error; err != nil {
		t.Fatal(err)
	}
	if _, rebuild, _ := sqliteBusinessKeyDDL(ddl, "unused"); !rebuild {
		t.Fatal("first structural step survived SQLite rollback")
	}
	if err := db.Callback().Raw().Remove("b02_fail_second_table"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := BeforeAutoMigrate(db); err != nil {
			t.Fatal(err)
		}
	}
	var records []MigrationRecord
	if err := db.Order("version").Find(&records).Error; err != nil || len(records) != 15 {
		t.Fatalf("migration records lost or duplicated: %+v, %v", records, err)
	}
	for _, rec := range records {
		if rec.Status != MigrationRecordStatusSuccess {
			t.Fatal("retry did not finish")
		}
	}
}

func TestSQLiteBusinessKeyDDLChangesOnlyKeyword(t *testing.T) {
	for _, ddl := range []string{
		`CREATE TABLE "old" ("hour" integer PRIMARY KEY AUTOINCREMENT, "note" text DEFAULT 'AUTOINCREMENT')`,
		"CREATE TABLE old (hour integer PRIMARY KEY autoincrement, note text /* AUTOINCREMENT */)",
		"CREATE TABLE old (hour integer PRIMARY KEY AUTOINCREMENT, note text DEFAULT 'it''s AUTOINCREMENT')",
		"CREATE TABLE old (hour integer PRIMARY KEY AUTOINCREMENT, note text -- AUTOINCREMENT\n)",
	} {
		result, changed, err := sqliteBusinessKeyDDL(ddl, `"new"`)
		if err != nil || !changed {
			t.Fatalf("DDL conversion failed: %v", err)
		}
		if _, changedAgain, err := sqliteBusinessKeyDDL(result, `"new"`); err != nil || changedAgain {
			t.Fatal("DDL conversion changed a literal or comment")
		}
	}
	for _, ddl := range []string{`CREATE TABLE old (hour integer PRIMARY KEY AUTOINCREMENT, note text DEFAULT 'broken)`, `CREATE TABLE old (hour integer PRIMARY KEY AUTOINCREMENT /* broken)`} {
		if _, _, err := sqliteBusinessKeyDDL(ddl, "new"); err == nil {
			t.Fatalf("malformed schema accepted: %s", fmt.Sprint(ddl))
		}
	}
}
