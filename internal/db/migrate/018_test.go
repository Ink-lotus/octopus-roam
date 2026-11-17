package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// This is the actual v0.1.1/v0.1.2 key declaration and writer shape. Every save
// starts from the cached hour=0 snapshot; the generated ID never reaches cache.
type legacyHourly18 struct {
	Hour int    `gorm:"primaryKey"`
	Date string `gorm:"not null"`
	model.StatsMetrics
}

func (legacyHourly18) TableName() string { return "stats_hourlies" }

func hourly18Fixture(t *testing.T) *gorm.DB {
	t.Helper()
	db := migrationTestDB(t)
	if err := db.AutoMigrate(&legacyHourly18{}, &model.StatsHourlyArchive{}, &MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for hour := 1; hour < 24; hour++ {
		row := legacyHourly18{Hour: hour, Date: "20261116", StatsMetrics: model.StatsMetrics{InputToken: int64(hour), InputCost: 0.125}}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 32; i++ {
		rows := []legacyHourly18{{Hour: 0, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: int64(i * 3), InputCost: 0.375}}}
		if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hour"}}, UpdateAll: true}).Create(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if rows[0].Hour != 23+i {
			t.Fatalf("old midnight bug was not reproduced: generated=%d", rows[0].Hour)
		}
	}
	// Preserve real legacy nullable storage, not merely ORM zero values.
	migrationExec(t, db, "UPDATE stats_hourlies SET wait_time = NULL WHERE hour = 24")
	return db
}

func hourly18Snapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var rows []map[string]any
	if err := db.Table("stats_hourlies").Order("hour").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func runHourly18(db *gorm.DB) error {
	return runMigrationsWithRecord(db, []Migration{{Version: 18, Up: migrateLegacyHourly, Atomic: true}})
}

func TestMigration18ArchivesRepeatedMidnightSnapshotsExactly(t *testing.T) {
	for _, already17 := range []bool{false, true} {
		t.Run(fmt.Sprintf("migration17-already-%t", already17), func(t *testing.T) {
			db := hourly18Fixture(t)
			before, err := readHourlyArchives(db)
			if err != nil || len(before) != 32 {
				t.Fatalf("fixture: %d %v", len(before), err)
			}
			if !strings.Contains(before[0].Payload, `"column":"wait_time","type":"null","value":""`) {
				t.Fatal("NULL storage was lost")
			}
			var legal []model.StatsHourly
			if err := db.Where("hour < 24").Order("hour").Find(&legal).Error; err != nil {
				t.Fatal(err)
			}
			if already17 {
				if err := migrateStatisticsBusinessKeys(db); err != nil {
					t.Fatal(err)
				}
				if err := upsertMigrationRecord(db, 17, MigrationRecordStatusSuccess); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := runMigrationsWithRecord(db, []Migration{{Version: 17, Up: migrateStatisticsBusinessKeys}}); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := runHourly18(db); err != nil {
					t.Fatal(err)
				}
			}
			var after []model.StatsHourly
			if err := db.Order("hour").Find(&after).Error; err != nil || !reflect.DeepEqual(after, legal) {
				t.Fatal("legal hourly slots changed", err)
			}
			for _, original := range before {
				var archived model.StatsHourlyArchive
				if err := db.Where("id = ?", original.ID).Take(&archived).Error; err != nil || archived != original {
					t.Fatal("archive differs from original SQLite values", err)
				}
			}
			var count int64
			if err := db.Model(&model.StatsHourlyArchive{}).Count(&count).Error; err != nil || count != 32 {
				t.Fatal("archive count/replay differs", err)
			}
			var record MigrationRecord
			if err := db.Where("version = 18").Take(&record).Error; err != nil || record.Status != MigrationRecordStatusSuccess {
				t.Fatal("success not recorded", err)
			}
			// Even with a lost external status, replay is content-idempotent.
			if err := db.Transaction(migrateLegacyHourly); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigration18FailureRollsBackRowsArchiveAndSuccess(t *testing.T) {
	for _, point := range []string{"archive-write", "archive-readback", "quarantine", "success-record"} {
		t.Run(point, func(t *testing.T) {
			db := hourly18Fixture(t)
			before := hourly18Snapshot(t, db)
			sentinel := errors.New("synthetic migration18 failure")
			fault := func(tx *gorm.DB) {
				if tx.Statement.Table == "stats_hourly_archives" && point == "archive-write" {
					tx.AddError(sentinel)
				}
				if tx.Statement.Table == "migration_records" && point == "success-record" {
					if rec, ok := tx.Statement.Dest.(*MigrationRecord); ok && rec.Status == MigrationRecordStatusSuccess {
						tx.AddError(sentinel)
					}
				}
			}
			if err := db.Callback().Create().Before("gorm:create").Register("hourly18-fault", fault); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Create().After("gorm:create").Register("hourly18-corrupt", func(tx *gorm.DB) {
				if point == "archive-readback" && tx.Statement.Table == "stats_hourly_archives" {
					// Session retains the same SQL transaction, so this is rolled back too.
					tx.Exec("UPDATE stats_hourly_archives SET payload = 'synthetic mismatch'")
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Callback().Raw().Before("gorm:raw").Register("hourly18-delete", func(tx *gorm.DB) {
				if point == "quarantine" && strings.HasPrefix(tx.Statement.SQL.String(), "DELETE FROM stats_hourlies") {
					tx.AddError(sentinel)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := runHourly18(db); err == nil {
				t.Fatal("fault succeeded")
			}
			if after := hourly18Snapshot(t, db); after != before {
				t.Fatal("failed migration changed live rows")
			}
			var count int64
			if err := db.Model(&model.StatsHourlyArchive{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("partial archive survived rollback", err)
			}
			var record MigrationRecord
			if err := db.Where("version = 18").Take(&record).Error; err != nil || record.Status != MigrationRecordStatusFailed {
				t.Fatal("failed migration not retryable", err)
			}
			for _, remove := range []func(string) error{db.Callback().Create().Remove, db.Callback().Raw().Remove} {
				for _, name := range []string{"hourly18-fault", "hourly18-corrupt", "hourly18-delete"} {
					if err := remove(name); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := runHourly18(db); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
}

func TestMigration18RefusesUnknownSchemaAndReferences(t *testing.T) {
	for _, ddl := range []string{
		"ALTER TABLE stats_hourlies ADD COLUMN unknown_evidence TEXT",
		"CREATE TRIGGER custom_hourly_delete AFTER DELETE ON stats_hourlies BEGIN SELECT 1; END",
		"CREATE TABLE custom_hourly_ref (hour integer REFERENCES stats_hourlies(hour) ON DELETE CASCADE)",
	} {
		t.Run(ddl[:12], func(t *testing.T) {
			db := hourly18Fixture(t)
			migrationExec(t, db, ddl)
			before := hourly18Snapshot(t, db)
			if err := runHourly18(db); err == nil {
				t.Fatal("unreviewed schema accepted")
			}
			if after := hourly18Snapshot(t, db); after != before {
				t.Fatal("rejection changed source")
			}
		})
	}
}

func TestMigration18NormalDatabaseUnchangedAndNegativeHourRemainsInvalid(t *testing.T) {
	db := migrationTestDB(t)
	if err := db.AutoMigrate(&model.StatsHourly{}, &model.StatsHourlyArchive{}, &MigrationRecord{}); err != nil {
		t.Fatal(err)
	}
	for _, hour := range []int{-1, 0, 1, 23} {
		if err := db.Create(&model.StatsHourly{Hour: hour, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 77}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	before := hourly18Snapshot(t, db)
	if err := runHourly18(db); err != nil {
		t.Fatal(err)
	}
	if after := hourly18Snapshot(t, db); after != before {
		t.Fatal("migration guessed a negative/valid hour")
	}
}
