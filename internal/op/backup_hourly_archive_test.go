package op

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func hourlyArchiveDump(t *testing.T) *model.DBDump {
	t.Helper()
	d := b03Dump(1)
	d.Version = 1003
	d.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 900}}}
	d.StatsDaily = []model.StatsDaily{{Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 600}}}
	d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: 1, StatsMetrics: model.StatsMetrics{InputToken: 500}}}
	for _, hour := range []int{0, 1, 23, 24, 25, 1145} {
		d.StatsHourly = append(d.StatsHourly, model.StatsHourly{Hour: hour, Date: "20261117", StatsMetrics: model.StatsMetrics{InputToken: 100, InputCost: 0.125}})
	}
	return d
}

func TestHourlyArchiveEncryptedRestoreAndReplay(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	source := hourlyArchiveDump(t)
	original, _ := json.Marshal(source)
	converted, count, err := ConvertLegacyHourlyBackup(source)
	if err != nil || count != 3 {
		t.Fatal("conversion", count, err)
	}
	after, _ := json.Marshal(source)
	if string(after) != string(original) {
		t.Fatal("conversion mutated source backup")
	}
	if !reflect.DeepEqual(converted.StatsHourly, source.StatsHourly[:3]) || !reflect.DeepEqual(converted.StatsTotal, source.StatsTotal) || !reflect.DeepEqual(converted.StatsDaily, source.StatsDaily) || !reflect.DeepEqual(converted.StatsAPIKey, source.StatsAPIKey) {
		t.Fatal("conversion changed valid statistics")
	}
	if _, err := DBImportIncremental(t.Context(), source); !errors.Is(err, ErrBackupInput) {
		t.Fatal("ordinary import accepted legacy invalid hours", err)
	}
	const password = "synthetic-hourly-archive-password"
	encrypted, err := EncryptBackup(converted, password)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecryptBackup(encrypted, password)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := DBImportIncremental(t.Context(), restored); err != nil {
			t.Fatal(err)
		}
	}
	exported, err := DBExportAll(t.Context())
	if err != nil || len(exported.StatsHourlyArchives) != 3 {
		t.Fatal("archive not in export", err)
	}
	for _, archive := range converted.StatsHourlyArchives {
		found := false
		for _, actual := range exported.StatsHourlyArchives {
			if actual == archive {
				found = true
			}
		}
		if !found {
			t.Fatal("archive altered or lost")
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	emptyPath := filepath.Join(t.TempDir(), "empty-restore.db")
	if err := db.InitDB("sqlite", emptyPath, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), exported); err != nil {
		t.Fatal("empty restore", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitDB("sqlite", emptyPath, false); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	final, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]any{{final.StatsHourlyArchives, exported.StatsHourlyArchives}, {final.StatsHourly, exported.StatsHourly}, {final.StatsTotal, exported.StatsTotal}, {final.StatsDaily, exported.StatsDaily}, {final.StatsAPIKey, exported.StatsAPIKey}, {final.Channels, exported.Channels}, {final.ChannelKeys, exported.ChannelKeys}, {final.Groups, exported.Groups}} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Fatal("restore or restart changed preserved data")
		}
	}
	report, err := integrity.Audit(t.Context(), "sqlite", emptyPath)
	if err != nil || report.Error() != nil || report.Counts["stats_hourly_archives"] != 3 {
		t.Fatal("archive audit", err, report.Error())
	}
}

func TestHourlyArchiveInvalidInputAndRollback(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	converted, _, err := ConvertLegacyHourlyBackup(hourlyArchiveDump(t))
	if err != nil {
		t.Fatal(err)
	}
	before := categoryBackupFingerprint(t)
	for _, edit := range []func(*model.DBDump){
		func(d *model.DBDump) { d.StatsHourlyArchives[0].Payload += " " },
		func(d *model.DBDump) { d.StatsHourlyArchives[0].OriginalHour = 0 },
		func(d *model.DBDump) { d.StatsHourlyArchives = append(d.StatsHourlyArchives, d.StatsHourlyArchives[0]) },
		func(d *model.DBDump) { d.Version = 1003 },
		func(d *model.DBDump) {
			d.StatsHourly = append(d.StatsHourly, model.StatsHourly{Hour: 24, Date: "20261117"})
		},
		func(d *model.DBDump) { d.ChannelKeys = nil },
	} {
		d := cloneBackupInput(converted)
		edit(d)
		if _, err := DBImportIncremental(t.Context(), d); !errors.Is(err, ErrBackupInput) {
			t.Fatal("invalid archive accepted", err)
		}
		if categoryBackupFingerprint(t) != before {
			t.Fatal("invalid input wrote data")
		}
	}
	if err := db.GetDB().Exec("CREATE TRIGGER hourly_archive_abort BEFORE INSERT ON stats_hourly_archives BEGIN SELECT RAISE(ABORT, 'synthetic failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), converted); err == nil {
		t.Fatal("archive write fault accepted")
	}
	if categoryBackupFingerprint(t) != before {
		t.Fatal("archive failure did not roll back entities and statistics")
	}
	if err := db.GetDB().Exec("DROP TRIGGER hourly_archive_abort").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), converted); err != nil {
		t.Fatal(err)
	}
	if err := db.GetDB().Exec("UPDATE stats_hourly_archives SET payload = 'synthetic corruption'").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := DBExportAll(t.Context()); err == nil {
		t.Fatal("corrupt archive exported successfully")
	}
	if err := statsRefreshCache(t.Context()); err == nil {
		t.Fatal("corrupt archive ignored at startup")
	}
}

func TestHourlyArchiveConversionKeepsIllegalInputRejected(t *testing.T) {
	for _, edit := range []func(*model.DBDump){
		func(d *model.DBDump) { d.StatsHourly[3].Hour = -1 },
		func(d *model.DBDump) { d.StatsHourly[3].Hour = 23 },
		func(d *model.DBDump) { d.StatsHourly[3].Date = "20260230" },
		func(d *model.DBDump) { d.StatsHourly[3].InputToken = -1 },
		func(d *model.DBDump) { d.ChannelKeys = nil },
		func(d *model.DBDump) { d.Product = ""; d.Version = 5 },
		func(d *model.DBDump) { d.Version = 1004 },
	} {
		d := hourlyArchiveDump(t)
		edit(d)
		if _, _, err := ConvertLegacyHourlyBackup(d); err == nil {
			t.Fatal("conversion accepted invalid input")
		}
	}
}
