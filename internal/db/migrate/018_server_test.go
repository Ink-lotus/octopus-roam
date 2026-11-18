package migrate

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Each case owns a new schema/database inside a disposable loopback server.
// Ordinary go test never discovers application profiles or real databases.
func serverMigration18DB(t *testing.T) *gorm.DB {
	t.Helper()
	dialect, dsn := os.Getenv("ROAM_HOURLY18_DIALECT"), os.Getenv("ROAM_HOURLY18_DSN")
	if dialect == "" {
		t.Skip("optional disposable MySQL/PostgreSQL integration")
	}
	if os.Getenv("ROAM_HOURLY18_ALLOW_ISOLATED_WRITE") != "1" {
		t.Fatal("explicit isolated database permission required")
	}
	open := func(value string) *gorm.DB {
		var driver gorm.Dialector
		if dialect == "postgres" {
			driver = postgres.Open(value)
		} else {
			driver = mysql.Open(value)
		}
		db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatal("cannot open isolated integration database")
		}
		return db
	}
	name := fmt.Sprintf("roam_hourly18_%d", time.Now().UnixNano())
	var database *gorm.DB
	switch dialect {
	case "postgres":
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil || cfg.Host != "127.0.0.1" || cfg.Database != "roam_hourly18" {
			t.Fatal("PostgreSQL target is not the isolated fixture")
		}
		for _, fallback := range cfg.Fallbacks {
			if fallback.Host != "127.0.0.1" {
				t.Fatal("non-loopback fallback rejected")
			}
		}
		base := open(dsn)
		migrationExec(t, base, "CREATE SCHEMA "+schemaIdentifier(base, name))
		cfg.RuntimeParams["search_path"] = name
		database = open(cfg.ConnString())
		pool, err := database.DB()
		if err != nil {
			t.Fatal("cannot open isolated PostgreSQL pool")
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		if err := database.Exec("SET search_path TO " + schemaIdentifier(database, name)).Error; err != nil {
			t.Fatal("cannot select isolated PostgreSQL schema")
		}
		t.Cleanup(func() {
			pool, _ := database.DB()
			_ = pool.Close()
			migrationExec(t, base, "DROP SCHEMA "+schemaIdentifier(base, name)+" CASCADE")
			pool, _ = base.DB()
			_ = pool.Close()
		})
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil || cfg.Net != "tcp" || cfg.DBName != "roam_hourly18" {
			t.Fatal("MySQL target is not the isolated fixture")
		}
		host, _, err := net.SplitHostPort(cfg.Addr)
		if err != nil || host != "127.0.0.1" {
			t.Fatal("non-loopback MySQL target rejected")
		}
		base := open(dsn)
		migrationExec(t, base, "CREATE DATABASE "+schemaIdentifier(base, name))
		cfg.DBName = name
		// Exercise the path most likely to accidentally select text results.
		cfg.InterpolateParams = true
		database = open(cfg.FormatDSN())
		t.Cleanup(func() {
			pool, _ := database.DB()
			_ = pool.Close()
			migrationExec(t, base, "DROP DATABASE "+schemaIdentifier(base, name))
			pool, _ = base.DB()
			_ = pool.Close()
		})
	default:
		t.Fatal("unsupported isolated integration database")
	}
	return database
}

func serverHourly18Fixture(t *testing.T) *gorm.DB {
	t.Helper()
	db := serverMigration18DB(t)
	if err := db.AutoMigrate(&legacyHourly18{}, &model.StatsHourlyArchive{}, &MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for hour := 1; hour < 24; hour++ {
		if err := db.Create(&legacyHourly18{Hour: hour, Date: "20261116", StatsMetrics: model.StatsMetrics{InputToken: int64(hour), InputCost: 0.1}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if db.Dialector.Name() == "postgres" {
		migrationExec(t, db, "SELECT setval(pg_get_serial_sequence('stats_hourlies','hour'),23,true)")
	}
	for i := 1; i <= 32; i++ {
		rows := []legacyHourly18{{Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: int64(i * 3), InputCost: 0.1, OutputCost: 1.23456789012345}}}
		if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hour"}}, UpdateAll: true}).Create(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if rows[0].Hour != 23+i {
			t.Fatal("authentic midnight writer did not reproduce overflow")
		}
	}
	migrationExec(t, db, "UPDATE stats_hourlies SET wait_time=NULL WHERE hour=24")
	return db
}

func TestServerMigration18UpgradePreservesNativeEvidence(t *testing.T) {
	for _, completed17 := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed17-%t", completed17), func(t *testing.T) {
			db := serverHourly18Fixture(t)
			if err := migrateStatisticsBusinessKeys(db); err != nil {
				t.Fatal(err)
			}
			if completed17 {
				if err := upsertMigrationRecord(db, 17, MigrationRecordStatusSuccess); err != nil {
					t.Fatal(err)
				}
				if err := upsertMigrationRecord(db, 18, MigrationRecordStatusFailed); err != nil {
					t.Fatal(err)
				}
			}
			before, err := readHourlyArchives(db)
			if err != nil || len(before) != 32 {
				t.Fatal("native snapshot", err)
			}
			var cells []model.HourlyArchiveCell
			if json.Unmarshal([]byte(before[0].Payload), &cells) != nil || cells[6].Type != "null" {
				t.Fatal("NULL lost")
			}
			if db.Dialector.Name() == "postgres" && (cells[4].Type != "real32" || cells[4].Value != "3dcccccd") {
				t.Fatal("PostgreSQL native REAL bits changed")
			}
			if db.Dialector.Name() == "mysql" && (cells[4].Type != "real" || cells[4].Value != "3fb999999999999a") {
				t.Fatal("MySQL native DOUBLE bits changed")
			}
			var legal []model.StatsHourly
			if err := db.Where("hour<24").Order("hour").Find(&legal).Error; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := runHourly18(db); err != nil {
					t.Fatal(err)
				}
			}
			var actual []model.StatsHourlyArchive
			if err := db.Order("original_hour").Find(&actual).Error; err != nil || !reflect.DeepEqual(actual, before) {
				t.Fatal("native evidence changed", err)
			}
			for _, row := range actual {
				if row.Format != 2 || row.Source != db.Dialector.Name()+"-migration-18" || row.Validate() != nil {
					t.Fatal("incorrect archive provenance")
				}
			}
			var after []model.StatsHourly
			if err := db.Order("hour").Find(&after).Error; err != nil || !reflect.DeepEqual(after, legal) {
				t.Fatal("legal statistics changed", err)
			}
			if err := db.Transaction(migrateLegacyHourly); err != nil {
				t.Fatal("replay", err)
			}
		})
	}
}

func TestServerMigration18FailureRollsBackAndRetries(t *testing.T) {
	for _, point := range []string{"archive-write", "archive-readback", "quarantine", "success-record"} {
		t.Run(point, func(t *testing.T) {
			db := serverHourly18Fixture(t)
			if err := migrateStatisticsBusinessKeys(db); err != nil {
				t.Fatal(err)
			}
			before := hourly18Snapshot(t, db)
			if err := db.Callback().Create().Before("gorm:create").Register("server18-fault", func(tx *gorm.DB) {
				if point == "archive-write" && tx.Statement.Table == "stats_hourly_archives" {
					tx.AddError(fmt.Errorf("synthetic archive write failure"))
				}
				if point == "success-record" && tx.Statement.Table == "migration_records" {
					if row, ok := tx.Statement.Dest.(*MigrationRecord); ok && row.Status == MigrationRecordStatusSuccess {
						tx.AddError(fmt.Errorf("synthetic success record failure"))
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Create().After("gorm:create").Register("server18-corrupt", func(tx *gorm.DB) {
				if point == "archive-readback" && tx.Statement.Table == "stats_hourly_archives" {
					tx.Exec("UPDATE stats_hourly_archives SET payload='synthetic mismatch'")
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Raw().Before("gorm:raw").Register("server18-delete", func(tx *gorm.DB) {
				if point == "quarantine" && strings.HasPrefix(tx.Statement.SQL.String(), "DELETE FROM stats_hourlies") {
					tx.AddError(fmt.Errorf("synthetic quarantine failure"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := runHourly18(db); err == nil {
				t.Fatal("fault accepted")
			}
			if hourly18Snapshot(t, db) != before {
				t.Fatal("source changed after rollback")
			}
			var count int64
			if err := db.Model(&model.StatsHourlyArchive{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("partial archive committed", err)
			}
			var record MigrationRecord
			if err := db.Where("version=18").Take(&record).Error; err != nil || record.Status != MigrationRecordStatusFailed {
				t.Fatal("retry status missing", err)
			}
			_ = db.Callback().Create().Remove("server18-fault")
			_ = db.Callback().Create().Remove("server18-corrupt")
			_ = db.Callback().Raw().Remove("server18-delete")
			if err := runHourly18(db); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
}

func TestServerMigration18RejectsUnreviewedSchemas(t *testing.T) {
	for _, kind := range []string{"column", "generated", "reference", "trigger", "engine-or-rls", "type"} {
		t.Run(kind, func(t *testing.T) {
			db := serverHourly18Fixture(t)
			if err := migrateStatisticsBusinessKeys(db); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "column":
				migrationExec(t, db, "ALTER TABLE stats_hourlies ADD COLUMN unknown_evidence TEXT")
			case "generated":
				migrationExec(t, db, "ALTER TABLE stats_hourlies DROP COLUMN input_token")
				migrationExec(t, db, "ALTER TABLE stats_hourlies ADD COLUMN input_token bigint GENERATED ALWAYS AS (output_token + 1) STORED")
			case "reference":
				migrationExec(t, db, "CREATE TABLE custom_hourly_ref (hour bigint, FOREIGN KEY(hour) REFERENCES stats_hourlies(hour) ON DELETE CASCADE)")
			case "trigger":
				if db.Dialector.Name() == "mysql" {
					migrationExec(t, db, "CREATE TRIGGER hourly18_custom BEFORE DELETE ON stats_hourlies FOR EACH ROW SET @hourly18_custom=1")
				} else {
					migrationExec(t, db, "CREATE FUNCTION hourly18_custom() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN RETURN OLD; END'")
					migrationExec(t, db, "CREATE TRIGGER hourly18_custom BEFORE DELETE ON stats_hourlies FOR EACH ROW EXECUTE FUNCTION hourly18_custom()")
				}
			case "engine-or-rls":
				if db.Dialector.Name() == "mysql" {
					migrationExec(t, db, "ALTER TABLE stats_hourly_archives ENGINE=MyISAM")
				} else {
					migrationExec(t, db, "ALTER TABLE stats_hourlies ENABLE ROW LEVEL SECURITY")
				}
			case "type":
				if db.Dialector.Name() == "mysql" {
					migrationExec(t, db, "ALTER TABLE stats_hourlies MODIFY COLUMN input_cost decimal(20,10)")
				} else {
					migrationExec(t, db, "ALTER TABLE stats_hourlies ALTER COLUMN input_cost TYPE numeric(20,10)")
				}
			}
			before := hourly18Snapshot(t, db)
			if err := runHourly18(db); err == nil {
				t.Fatal("unreviewed schema accepted")
			}
			if before != hourly18Snapshot(t, db) {
				t.Fatal("rejected migration changed evidence")
			}
		})
	}
}

func TestServerMigration18NormalDatabaseRemainsUnchanged(t *testing.T) {
	db := serverMigration18DB(t)
	if err := db.AutoMigrate(&model.StatsHourly{}, &model.StatsHourlyArchive{}, &MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, hour := range []int{-1, 0, 1, 23} {
		if err := db.Create(&model.StatsHourly{Hour: hour, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 7}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	before := hourly18Snapshot(t, db)
	for range 2 {
		if err := runHourly18(db); err != nil {
			t.Fatal(err)
		}
	}
	if hourly18Snapshot(t, db) != before {
		t.Fatal("migration changed normal or unrelated invalid hours")
	}
}
