package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	mysqlDriver "gorm.io/driver/mysql"
	postgresDriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func captureDatabaseCheckOutput(t *testing.T, fn func() error) (error, []byte) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	result := fn()
	_ = writer.Close()
	os.Stdout = original
	data, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return result, data
}

func TestProfilePathDefaultsToUserDotConfig(t *testing.T) {
	original := dbProfileFile
	t.Cleanup(func() { dbProfileFile = original })
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	dbProfileFile = ""
	want := filepath.Join(home, ".config", "octopus-roam", "db-profiles.json")
	if got := profilePath(); got != want {
		t.Fatalf("profilePath()=%q, want %q", got, want)
	}
	dbProfileFile = filepath.Join(home, "explicit.json")
	if got := profilePath(); got != dbProfileFile {
		t.Fatalf("explicit profilePath()=%q, want %q", got, dbProfileFile)
	}
}

func TestDatabaseCheckReturnsErrorForMissingProfile(t *testing.T) {
	originalFile, originalAlias, originalRun := dbProfileFile, dbProfileAlias, dbProfileRun
	t.Cleanup(func() { dbProfileFile, dbProfileAlias, dbProfileRun = originalFile, originalAlias, originalRun })
	dbProfileFile = filepath.Join(t.TempDir(), "missing.json")
	dbProfileAlias = "synthetic"
	dbProfileRun = false
	err, output := captureDatabaseCheckOutput(t, func() error { return databaseCheckCmd.RunE(databaseCheckCmd, nil) })
	if !errors.Is(err, errDatabaseCheckFailed) {
		t.Fatalf("error=%v, want errDatabaseCheckFailed", err)
	}
	var result databaseCheckResult
	if decodeErr := json.Unmarshal(bytes.TrimSpace(output), &result); decodeErr != nil {
		t.Fatalf("output=%q err=%v", output, decodeErr)
	}
	if result.Status != "invalid_profile" || result.Error != "profile_missing" {
		t.Fatalf("result=%+v", result)
	}
	if !databaseCheckCmd.SilenceErrors || !databaseCheckCmd.SilenceUsage {
		t.Fatal("db-check must suppress duplicate Cobra errors and usage")
	}
}

func TestDatabaseCheckReturnsNilForValidOfflineProfile(t *testing.T) {
	originalFile, originalAlias, originalRun := dbProfileFile, dbProfileAlias, dbProfileRun
	t.Cleanup(func() { dbProfileFile, dbProfileAlias, dbProfileRun = originalFile, originalAlias, originalRun })
	dbProfileFile = filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(dbProfileFile, []byte(validOfflineMySQLProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	dbProfileAlias = "synthetic"
	dbProfileRun = false
	err, output := captureDatabaseCheckOutput(t, func() error { return databaseCheckCmd.RunE(databaseCheckCmd, nil) })
	if err != nil {
		t.Fatalf("offline profile check error=%v", err)
	}
	var result databaseCheckResult
	if decodeErr := json.Unmarshal(bytes.TrimSpace(output), &result); decodeErr != nil {
		t.Fatalf("output=%q err=%v", output, decodeErr)
	}
	if result.Status != "profile_valid" || result.Type != "mysql" {
		t.Fatalf("result=%+v", result)
	}
}

const validOfflineMySQLProfile = `{"synthetic":{"type":"mysql","host":"127.0.0.1","port":3306,"database":"octopus_roam","user":"synthetic","password":"synthetic-password","tls":true}}`

func TestSettingTouchSQLQuotesReservedColumnPerDialect(t *testing.T) {
	cases := []struct {
		name    string
		dialect gorm.Dialector
		want    string
	}{
		{name: "mysql", dialect: mysqlDriver.New(mysqlDriver.Config{DSN: "u:p@tcp(127.0.0.1:1)/db", SkipInitializeWithVersion: true}), want: "`key`"},
		{name: "postgres", dialect: postgresDriver.New(postgresDriver.Config{DSN: "postgres://u:p@127.0.0.1:1/db?sslmode=disable"}), want: `"key"`},
		{name: "sqlite", dialect: sqlite.Open(filepath.Join(t.TempDir(), "dry-run.db")), want: "`key`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql, err := buildSettingTouchSQL(tc.dialect, "10")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(strings.ToUpper(sql), "UPDATE") || !strings.Contains(sql, "settings") || !strings.Contains(sql, tc.want) {
				t.Fatalf("generated SQL=%q, want quoted key %q", sql, tc.want)
			}
		})
	}
}
