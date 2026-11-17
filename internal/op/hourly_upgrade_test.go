package op

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm/clause"
)

type legacyHourlyUpgrade struct {
	Hour int    `gorm:"primaryKey"`
	Date string `gorm:"not null"`
	model.StatsMetrics
}

func (legacyHourlyUpgrade) TableName() string { return "stats_hourlies" }

func TestHourlyArchiveStartupFromPre17AndCompleted17(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	for _, completed17 := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed17-%t", completed17), func(t *testing.T) {
			path := openRetiredSettingsDatabase(t)
			seed := hourlyArchiveDump(t)
			seed.StatsHourly = seed.StatsHourly[1:3] // No midnight slot, as in the real affected database.
			b03Import(t, seed)
			before, err := DBExportAll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			conn := db.GetDB()
			for _, statement := range []string{
				"ALTER TABLE stats_hourlies RENAME TO hourly_upgrade_fixed_fixture",
				"DELETE FROM migration_records WHERE version IN (16,17,18)",
			} {
				if err := conn.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := conn.AutoMigrate(&legacyHourlyUpgrade{}); err != nil {
				t.Fatal(err)
			}
			if err := conn.Exec("INSERT INTO stats_hourlies SELECT * FROM hourly_upgrade_fixed_fixture").Error; err != nil {
				t.Fatal(err)
			}
			if err := conn.Exec("DROP TABLE hourly_upgrade_fixed_fixture").Error; err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 32; i++ {
				rows := []legacyHourlyUpgrade{{Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: int64(i * 4)}}}
				if err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hour"}}, UpdateAll: true}).Create(&rows).Error; err != nil {
					t.Fatal(err)
				}
			}
			if completed17 {
				if err := migrate.BeforeAutoMigrate(conn); err != nil {
					t.Fatal(err)
				}
				if err := conn.Create(&migrate.MigrationRecord{Version: 16, Status: migrate.MigrationRecordStatusSuccess}).Error; err != nil {
					t.Fatal(err)
				}
				if err := statsRefreshCache(t.Context()); err == nil {
					t.Fatal("completed17 fixture did not reproduce startup failure")
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := db.InitDB("sqlite", path, false); err != nil {
					t.Fatal(err)
				}
				if err := InitCache(); err != nil {
					t.Fatal(err)
				}
				after, err := DBExportAll(t.Context())
				if err != nil || len(after.StatsHourlyArchives) != 32 {
					t.Fatal("startup/archive", err)
				}
				archives := after.StatsHourlyArchives
				for _, archive := range archives {
					if archive.Validate() != nil {
						t.Fatal("invalid persisted evidence")
					}
				}
				after.StatsHourlyArchives = nil
				after.ExportedAt = before.ExportedAt
				// Compare every exported table, including credentials without logging them.
				left, _ := json.Marshal(before)
				right, _ := json.Marshal(after)
				if !reflect.DeepEqual(left, right) {
					t.Fatal("upgrade changed legitimate statistics or business configuration")
				}
				if _, err := DBImportIncremental(t.Context(), before); err != nil {
					t.Fatal("local archive blocked ordinary import", err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// openRetiredSettingsDatabase expects an open owned connection on entry.
			if err := db.InitDB("sqlite", path, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
