package main

import (
	"path/filepath"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

// This fixture test catches model/table and pinned-statement contamination before
// the authentic v0.1.2 executable creates the three server upgrade fixtures.
func TestLegacyStatisticsPreparationUsesSeparateModelStatements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-statistics.sqlite")
	conn, err := rawDB("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
		&model.GroupCategory{}, &model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.WebhookEndpoint{}); err != nil {
		t.Fatal(err)
	}
	metrics := ", input_token integer, output_token integer, input_cost real, output_cost real, wait_time integer, request_success integer, request_failed integer)"
	for _, query := range []string{
		"CREATE TABLE stats_totals (id integer PRIMARY KEY AUTOINCREMENT" + metrics,
		"CREATE TABLE stats_hourlies (hour integer PRIMARY KEY AUTOINCREMENT, date text NOT NULL" + metrics,
		"CREATE TABLE stats_api_keys (api_key_id integer PRIMARY KEY AUTOINCREMENT" + metrics,
		"CREATE TABLE migration_records (version integer PRIMARY KEY AUTOINCREMENT, status integer)",
		"INSERT INTO stats_totals (id,input_token) VALUES (1,0)",
	} {
		if err := conn.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	for version := 3; version <= 15; version++ {
		if err := conn.Exec("INSERT INTO migration_records VALUES (?,1)", version).Error; err != nil {
			t.Fatal(err)
		}
	}
	categoryID := 1
	dump := &model.DBDump{
		Channels:         []model.Channel{{ID: 1, ChannelConfig: model.ChannelConfig{Name: fixtureChannel, BaseURL: "http://127.0.0.1:1", Enabled: true}}},
		ChannelKeys:      []model.ChannelKey{{ID: 1, ChannelID: 1, ChannelKeyConfig: model.ChannelKeyConfig{Name: "test", Key: "synthetic"}}},
		ChannelModels:    []model.ChannelModel{{ID: 1, ChannelID: 1, Name: fixtureModel}},
		ChannelGrants:    []model.ChannelGrant{{ID: 1, ChannelModelID: 1, ChannelKeyID: 1}},
		GroupCategories:  []model.BackupGroupCategory{{ID: 1, Name: "Fixture category"}},
		Groups:           []model.Group{{ID: 1, Name: fixtureModel, CategoryID: &categoryID}},
		GroupItems:       []model.GroupItem{{ID: 1, GroupID: 1, ChannelGrantID: 1, Priority: 1}},
		APIKeys:          []model.APIKey{{ID: 1, Name: "synthetic", APIKey: "synthetic"}},
		WebhookEndpoints: []model.WebhookEndpoint{{ID: 1, Name: "synthetic", URL: "http://127.0.0.1:1"}},
	}
	s := suite{ctx: t.Context(), opts: options{dialect: "sqlite"}, dsn: path, report: &report{}}
	if err := s.prepareStatisticsUpgrade(dump); err != nil {
		t.Fatal(err)
	}
	var total model.StatsTotal
	if err := conn.First(&total, 1).Error; err != nil || total.InputToken != 777 {
		t.Fatalf("legacy total fixture changed: %+v, %v", total, err)
	}
	var rows []model.StatsHourly
	if err := conn.Order("hour").Find(&rows).Error; err != nil || len(rows) != 35 || rows[0].Hour != 0 || rows[0].InputToken != 100 || rows[1].Hour != 1 || rows[1].InputToken != 200 || rows[2].Hour != 23 || rows[2].InputToken != 999 {
		t.Fatalf("legacy hours moved: %+v, %v", rows, err)
	}
	var key model.StatsAPIKey
	if err := conn.First(&key, "api_key_id=7").Error; err != nil || key.InputToken != 333 {
		t.Fatalf("legacy API key fixture changed: %+v, %v", key, err)
	}
	if rows[3].Hour != 24 || rows[34].Hour != 55 {
		t.Fatal("old midnight fixture did not create overflow")
	}
	if err := s.verifyStatisticsUpgrade(dump); err != nil {
		t.Fatal(err)
	}
}
