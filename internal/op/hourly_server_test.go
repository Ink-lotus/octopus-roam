package op

import (
	"encoding/json"
	"math"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

func hourlyServerTarget(t *testing.T) (string, string) {
	t.Helper()
	dialect, dsn := os.Getenv("ROAM_HOURLY18_DIALECT"), os.Getenv("ROAM_HOURLY18_DSN")
	if dialect == "" {
		t.Skip("optional disposable server backup integration")
	}
	if os.Getenv("ROAM_HOURLY18_ALLOW_ISOLATED_WRITE") != "1" {
		t.Fatal("explicit isolated permission required")
	}
	switch dialect {
	case "postgres":
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil || cfg.Host != "127.0.0.1" || cfg.Database != "roam_hourly18" {
			t.Fatal("PostgreSQL target is not isolated")
		}
		for _, fallback := range cfg.Fallbacks {
			if fallback.Host != "127.0.0.1" {
				t.Fatal("non-loopback fallback")
			}
		}
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil || cfg.Net != "tcp" || cfg.DBName != "roam_hourly18" {
			t.Fatal("MySQL target is not isolated")
		}
		host, _, err := net.SplitHostPort(cfg.Addr)
		if err != nil || host != "127.0.0.1" {
			t.Fatal("non-loopback target")
		}
	default:
		t.Fatal("unsupported isolated dialect")
	}
	return dialect, dsn
}

func TestServerHourlyArchivesEncryptedRestoreAndReplay(t *testing.T) {
	dialect, dsn := hourlyServerTarget(t)
	if runRetiredSettingsIsolated(t) {
		return
	}
	_ = db.Close()
	if err := db.InitDB(dialect, dsn, false); err != nil {
		t.Fatal("isolated boot", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	seed := b03Dump(1)
	seed.Version = 1005
	seed.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 777}}}
	seed.StatsHourly = []model.StatsHourly{{Hour: 0, Date: "20261118", StatsMetrics: model.StatsMetrics{InputToken: 333}}}
	// Include both provenances in every destination; backup/restore must be
	// portable and must not silently discard a foreign database's evidence.
	for _, source := range []string{"mysql-migration-18", "postgres-migration-18"} {
		values := []any{int64(1145), []byte("20261117"), nil, int64(math.MaxInt64), 0.1, math.Copysign(0, -1), int64(1), int64(2), int64(3)}
		types := []string{"bigint", "longtext", "bigint", "bigint", "double", "double", "bigint", "bigint", "bigint"}
		if source == "postgres-migration-18" {
			types[1], types[4], types[5] = "text", "real", "double precision"
			values[4], values[5] = []byte{0x3d, 0xcc, 0xcc, 0xcd}, []byte{0x80, 0, 0, 0, 0, 0, 0, 0}
		}
		cells := make([]model.HourlyArchiveCell, len(values))
		for i, value := range values {
			var err error
			cells[i], err = model.NewServerHourlyArchiveCell(source, model.HourlyArchiveColumns()[i], types[i], value)
			if err != nil {
				t.Fatal(err)
			}
		}
		archive, err := model.NewHourlyArchive(source, cells)
		if err != nil {
			t.Fatal(err)
		}
		seed.StatsHourlyArchives = append(seed.StatsHourlyArchives, archive)
	}
	const password = "synthetic-server-hourly-archive-password"
	encrypted, err := EncryptBackup(seed, password)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptBackup(encrypted, password)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := DBImportIncremental(t.Context(), restored); err != nil {
			t.Fatal("server restore/replay", err)
		}
	}
	// Reproduce a v0.1.4 failed-18 database using the real InitDB/InitCache
	// startup path, with both existing archives and live overflow present.
	if err := db.GetDB().Create(&model.StatsHourly{Hour: 1146, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 900, InputCost: 0.1}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE stats_hourlies SET wait_time=NULL WHERE hour=1146").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE migration_records SET status=2 WHERE version=18").Error; err != nil {
		t.Fatal(err)
	}
	if err := statsRefreshCache(t.Context()); err == nil {
		t.Fatal("overflow did not reproduce startup cache failure")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(dialect, dsn, false); err != nil {
		t.Fatal("failed-18 startup retry", err)
	}
	if err := InitCache(); err != nil {
		t.Fatal("repaired cache", err)
	}
	before, err := DBExportAll(t.Context())
	if err != nil || len(before.StatsHourlyArchives) != 3 || before.Version != 1005 {
		t.Fatal("server export", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB(dialect, dsn, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	after, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	after.ExportedAt = before.ExportedAt
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	if !reflect.DeepEqual(left, right) {
		t.Fatal("server restart changed preserved data")
	}
	report, err := integrity.Audit(t.Context(), dialect, dsn)
	if err != nil || report.Error() != nil || report.Counts["stats_hourly_archives"] != 3 {
		t.Fatal("server archive audit", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The same encrypted server export must also restore on an empty SQLite
	// instance and survive another encrypted export and restart there.
	path := filepath.Join(t.TempDir(), "portable.sqlite")
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	portable, err := EncryptBackup(before, password)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := DecryptBackup(portable, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), copy); err != nil {
		t.Fatal("portable restore", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB("sqlite", path, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	final, err := DBExportAll(t.Context())
	if err != nil || !reflect.DeepEqual(final.StatsHourlyArchives, before.StatsHourlyArchives) {
		t.Fatal("portable evidence changed", err)
	}
}
