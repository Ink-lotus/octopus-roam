package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsolatedTargetsRejectRemoteFallbacksAndOtherDatabases(t *testing.T) {
	for _, tc := range []struct {
		dialect, dsn string
		allowed      bool
	}{
		{"postgres", "host=127.0.0.1 dbname=roam_backup_b01 user=synthetic sslmode=disable", true},
		{"postgres", "host=example.test dbname=roam_backup_b01 user=synthetic sslmode=disable", false},
		{"postgres", "host=127.0.0.1,example.test dbname=roam_backup_b01 user=synthetic sslmode=disable", false},
		{"postgres", "host=127.0.0.1 dbname=another_database user=synthetic sslmode=disable", false},
		{"postgres", "host=127.0.0.1 dbname=roam_backup_b04_c08 user=synthetic sslmode=disable", true},
		{"postgres", "host=127.0.0.1 dbname=roam_backup_b04_production user=synthetic sslmode=disable", false},
		{"mysql", "synthetic@tcp(127.0.0.1:13306)/roam_backup_b01", true},
		{"mysql", "synthetic@tcp(example.test:13306)/roam_backup_b01", false},
		{"mysql", "synthetic@tcp(127.0.0.1:13306)/another_database", false},
		{"mysql", "synthetic@tcp(127.0.0.1:13306)/roam_backup_b04_cross_postgres_mysql", true},
		{"mysql", "synthetic@tcp(example.test:13306)/roam_backup_b04_c01", false},
	} {
		t.Setenv("ROAM_BACKUP_TEST_DSN", tc.dsn)
		_, err := isolatedDSN(options{dialect: tc.dialect})
		if (err == nil) != tc.allowed {
			t.Fatalf("%s isolation check: err=%v, allowed=%t", tc.dialect, err, tc.allowed)
		}
	}
}

func TestFailureMessageKeepsPrivateDriverAndImportValuesOut(t *testing.T) {
	for _, err := range []error{
		&pgconn.PgError{Code: "23505", Message: "private-driver-value", Detail: "private-constraint-value"},
		fmt.Errorf("private-import-value: SQLSTATE 23505"),
	} {
		if got := failureMessage(err, "synthetic-check"); got != "acceptance failed: synthetic-check (SQLSTATE 23505)" {
			t.Fatalf("unsafe failure message: %s", got)
		}
	}
	if got := failureMessage(errors.New("private-connection-value"), ""); got != "acceptance failed" {
		t.Fatalf("unsafe connection failure: %s", got)
	}
}

func TestPostgresSchemaSearchPathRoundTrip(t *testing.T) {
	for _, name := range []string{`b01 quoted "schema`, `b01 + schema`} {
		dsn, err := postgresDSN("host=127.0.0.1 dbname=roam_backup_b01 user=synthetic sslmode=disable", name, "")
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := cfg.RuntimeParams["search_path"], quote(name)+", public"; got != want {
			t.Fatalf("synthetic search_path = %q, want %q", got, want)
		}
	}
}

func TestIsolatedSQLiteRefusesExistingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "existing.sqlite")
	if err := os.WriteFile(path, []byte("existing synthetic data"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := isolatedDSN(options{dialect: "sqlite", sqlitePath: path}); err == nil {
		t.Fatal("existing SQLite file was accepted")
	}
}
