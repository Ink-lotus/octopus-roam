package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Use the historical key and repeated upsert shape against the schema created
// by the authentic old executable. Legal 0/1/23 remain preservation controls.
type oldHourlyArchiveFixture struct {
	Hour int    `gorm:"primaryKey"`
	Date string `gorm:"not null"`
	model.StatsMetrics
}

func (oldHourlyArchiveFixture) TableName() string { return "stats_hourlies" }

func seedHourlyArchiveFixture(conn *gorm.DB) error {
	if conn.Dialector.Name() != "sqlite" {
		return nil
	}
	expected := []model.StatsHourlyArchive{}
	for i := 1; i <= 32; i++ {
		rows := []oldHourlyArchiveFixture{{Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: int64(3 * i), InputCost: 0.125}}}
		if err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "hour"}}, UpdateAll: true}).Create(&rows).Error; err != nil {
			return err
		}
		if rows[0].Hour != 23+i {
			return errors.New("historical midnight writer did not generate the expected overflow")
		}
		archive, err := model.ArchiveLegacyHourly(model.StatsHourly{Hour: rows[0].Hour, Date: rows[0].Date, StatsMetrics: rows[0].StatsMetrics}, 1003)
		if err != nil {
			return err
		}
		// Same cells, independently computed expected provenance for migration.
		var cells []model.HourlyArchiveCell
		if err := json.Unmarshal([]byte(archive.Payload), &cells); err != nil {
			return err
		}
		archive, err = model.NewHourlyArchive("sqlite-migration-18", cells)
		if err != nil {
			return err
		}
		expected = append(expected, archive)
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].ID < expected[j].ID })
	raw, err := json.Marshal(expected)
	if err != nil {
		return err
	}
	if err := conn.Exec("CREATE TABLE hourly18_expected_fixture (payload text NOT NULL)").Error; err != nil {
		return err
	}
	return conn.Exec("INSERT INTO hourly18_expected_fixture (payload) VALUES (?)", string(raw)).Error
}

func verifyHourlyArchiveFixture(conn *gorm.DB) error {
	if conn.Dialector.Name() != "sqlite" {
		return nil
	}
	var payload string
	if err := conn.Table("hourly18_expected_fixture").Select("payload").Take(&payload).Error; err != nil {
		return err
	}
	var expected, actual []model.StatsHourlyArchive
	if err := json.Unmarshal([]byte(payload), &expected); err != nil {
		return err
	}
	if err := conn.Order("id").Find(&actual).Error; err != nil {
		return err
	}
	if len(expected) != 32 || !reflect.DeepEqual(actual, expected) {
		return errors.New("legacy hourly evidence changed during upgrade")
	}
	var count int64
	if err := conn.Table("stats_hourlies").Where("hour >= 24").Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("legacy overflow remained in live statistics")
	}
	return nil
}
