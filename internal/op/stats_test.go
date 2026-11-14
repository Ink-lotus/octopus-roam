package op

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

// The optional real-server path is consumed only by this isolated test binary.
// It never loads application profiles or accepts a non-loopback server/database.
func openStatsTestDatabase(t *testing.T) (string, string) {
	t.Helper()
	dialect, dsn := "sqlite", filepath.Join(t.TempDir(), "stats.sqlite")
	if requested := os.Getenv("ROAM_STATS_TEST_DIALECT"); requested != "" {
		if os.Getenv("ROAM_STATS_TEST_ALLOW_ISOLATED_WRITE") != "1" {
			t.Fatal("explicit synthetic statistics database permission required")
		}
		dialect, dsn = requested, os.Getenv("ROAM_STATS_TEST_DSN")
		switch dialect {
		case "postgres":
			cfg, err := pgx.ParseConfig(dsn)
			if err != nil || cfg.Host != "127.0.0.1" || cfg.Database != "roam_backup_b02_clock" {
				t.Fatal("statistics PostgreSQL target is not isolated")
			}
			for _, fallback := range cfg.Fallbacks {
				if fallback.Host != "127.0.0.1" {
					t.Fatal("statistics PostgreSQL fallback is not loopback")
				}
			}
		case "mysql":
			cfg, err := mysqldriver.ParseDSN(dsn)
			if err != nil || cfg.Net != "tcp" || cfg.DBName != "roam_backup_b02_clock" {
				t.Fatal("statistics MySQL target is not isolated")
			}
			host, _, err := net.SplitHostPort(cfg.Addr)
			if err != nil || host != "127.0.0.1" {
				t.Fatal("statistics MySQL target is not loopback")
			}
		default:
			t.Fatal("unsupported synthetic statistics database")
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(dialect, dsn, false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return dialect, dsn
}

func TestStatsBusinessKeysAndMidnight(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	dialect, dsn := openStatsTestDatabase(t)
	ctx := t.Context()
	beforeMidnight := time.Date(2026, 11, 12, 23, 59, 0, 0, time.FixedZone("test", 8*3600))
	midnight := beforeMidnight.Add(time.Minute)
	one := midnight.Add(time.Hour)
	if err := statsRefreshCacheAt(ctx, beforeMidnight); err != nil {
		t.Fatal(err)
	}
	add := func(at time.Time, tokens int64, save bool) {
		t.Helper()
		metrics := model.StatsMetrics{InputToken: tokens, RequestSuccess: 1}
		// Use the same update order as relay.finish, including a midnight
		// request before the periodic writer has saved the previous hour.
		if err := StatsTotalUpdate(metrics); err != nil {
			t.Fatal(err)
		}
		if err := statsHourlyUpdateAt(metrics, at); err != nil {
			t.Fatal(err)
		}
		if err := statsDailyUpdateAt(ctx, metrics, at); err != nil {
			t.Fatal(err)
		}
		if err := StatsAPIKeyUpdate(7, metrics); err != nil {
			t.Fatal(err)
		}
		if save {
			if err := StatsSaveDB(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	add(beforeMidnight, 50, false)
	add(midnight, 100, false)
	add(one, 200, true)
	assertHours := func() {
		t.Helper()
		var rows []model.StatsHourly
		if err := db.GetDB().Order("hour").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 || rows[0].Hour != 0 || rows[0].Date != "20261113" || rows[0].InputToken != 100 ||
			rows[1].Hour != 1 || rows[1].Date != "20261113" || rows[1].InputToken != 200 ||
			rows[2].Hour != 23 || rows[2].Date != "20261112" || rows[2].InputToken != 50 {
			t.Fatalf("midnight slots moved or overwritten: %+v", rows)
		}
		visible := statsHourlyGetAt(one)
		if len(visible) != 2 || visible[0].InputToken != 100 || visible[1].InputToken != 200 {
			t.Fatalf("midnight cache changed: %+v", visible)
		}
		var daily []model.StatsDaily
		if err := db.GetDB().Order("date").Find(&daily).Error; err != nil || len(daily) != 2 || daily[0].InputToken != 50 || daily[1].InputToken != 300 {
			t.Fatalf("midnight daily persistence changed: %+v, %v", daily, err)
		}
		if total := StatsTotalGet(); total.ID != 1 || total.InputToken != 350 {
			t.Fatalf("fixed total key changed: %+v", total)
		}
		if key := StatsAPIKeyGet(7); key.APIKeyID != 7 || key.InputToken != 350 {
			t.Fatalf("API key statistics moved: %+v", key)
		}
	}
	assertHours()
	// The ordinary writer also retains populated slots from other dates.
	if err := StatsSaveDB(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(dialect, dsn, false); err != nil {
		t.Fatal(err)
	}
	if err := statsRefreshCacheAt(ctx, one); err != nil {
		t.Fatal(err)
	}
	assertHours()
	// Reusing a slot on a later date replaces just that slot, not yesterday's
	// other slots or the accumulated total/daily statistics.
	nextMidnight := midnight.AddDate(0, 0, 1)
	if err := statsHourlyUpdateAt(model.StatsMetrics{InputToken: 17}, nextMidnight); err != nil {
		t.Fatal(err)
	}
	if err := StatsSaveDB(ctx); err != nil {
		t.Fatal(err)
	}
	var zero model.StatsHourly
	if err := db.GetDB().Where("hour=0").First(&zero).Error; err != nil || zero.InputToken != 17 || zero.Date != "20261114" {
		t.Fatalf("slot reuse failed: %+v, %v", zero, err)
	}
	if got := statsHourlyGetAt(nextMidnight); len(got) != 1 || got[0].InputToken != 17 {
		t.Fatal("next midnight included yesterday's slots")
	}
}

func TestStatisticsInvalidInputDoesNotReachRowsOrCaches(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openStatsTestDatabase(t)
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*model.DBDump{
		{StatsHourly: []model.StatsHourly{{Hour: -1}}}, {StatsHourly: []model.StatsHourly{{Hour: 24}}},
		{StatsHourly: []model.StatsHourly{{Hour: 0}, {Hour: 0}}}, {StatsTotal: []model.StatsTotal{{ID: 0}}},
		{StatsTotal: []model.StatsTotal{{ID: 2}}}, {StatsTotal: []model.StatsTotal{{ID: 1}, {ID: 1}}},
		{StatsAPIKey: []model.StatsAPIKey{{APIKeyID: 0}}}, {StatsAPIKey: []model.StatsAPIKey{{APIKeyID: -7}}},
		{StatsAPIKey: []model.StatsAPIKey{{APIKeyID: 7}, {APIKeyID: 7}}},
	} {
		bad.Product, bad.Version = dumpProduct, dbDumpVersion
		bad.Channels = []model.Channel{{ID: 777, ChannelConfig: model.ChannelConfig{Name: "rejected-statistics", BaseURL: "http://127.0.0.1:1"}}}
		if _, err := DBImportIncremental(t.Context(), bad); err == nil {
			t.Fatalf("invalid statistics accepted: %+v", bad)
		}
		for _, table := range []string{"channels", "stats_totals", "stats_hourlies", "stats_api_keys"} {
			var count int64
			if err := db.GetDB().Table(table).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("rejected input changed %s: %d, %v", table, count, err)
			}
		}
	}
	if err := StatsAPIKeyUpdate(0, model.StatsMetrics{InputToken: 1}); err == nil {
		t.Fatal("invalid API key reached cache")
	}
	StatsAPIKeyGet(0)
	if _, exists := statsAPIKeyCache.Get(0); exists {
		t.Fatal("invalid API key getter created a cache entry")
	}
	before := StatsTotalGet()
	if err := db.GetDB().Create(&model.StatsTotal{ID: 2, StatsMetrics: model.StatsMetrics{InputToken: 99}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := statsRefreshCache(t.Context()); err == nil {
		t.Fatal("invalid stored total entered cache")
	}
	if !reflect.DeepEqual(before, StatsTotalGet()) {
		t.Fatal("failed refresh published partial statistics")
	}
}

func TestBackupStatisticsKeepAllHoursAndReplay(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openStatsTestDatabase(t)
	for _, hours := range [][]int{{0}, {0, 1}, {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}} {
		input := &model.DBDump{Product: dumpProduct, Version: dbDumpVersion}
		for _, hour := range hours {
			input.StatsHourly = append(input.StatsHourly, model.StatsHourly{Hour: hour, Date: "20261112", StatsMetrics: model.StatsMetrics{InputToken: int64((hour + 1) * 100)}})
		}
		for range 2 {
			raw, _ := json.Marshal(input)
			var copy model.DBDump
			if err := json.Unmarshal(raw, &copy); err != nil {
				t.Fatal(err)
			}
			if _, err := DBImportIncremental(t.Context(), &copy); err != nil {
				t.Fatal(err)
			}
			var rows []model.StatsHourly
			if err := db.GetDB().Order("hour").Find(&rows).Error; err != nil || !reflect.DeepEqual(rows, input.StatsHourly) {
				t.Fatalf("hour data moved or duplicated: %+v, %v", rows, err)
			}
		}
	}
}
