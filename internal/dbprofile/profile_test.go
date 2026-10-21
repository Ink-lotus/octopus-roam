package dbprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func writeProfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db-profiles.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validMySQLProfile() string {
	return `{"roam-mysql-test":{"type":"mysql","host":"db.example.test","port":3306,"database":"roam_test","restore_database":"roam_restore","user":"tester","password":"synthetic password","tls":true}}`
}

func validPostgresProfile() string {
	return `{"roam-postgres-test":{"type":"postgres","host":"db.example.test","port":5432,"database":"roam_test","user":"tester","password":"synthetic password","sslmode":"verify-full","channel_binding":"require"}}`
}

func TestLoadFileValidatesExactAliasWithoutEchoingSecret(t *testing.T) {
	path := writeProfile(t, validMySQLProfile())
	profile, err := LoadFile(path, "roam-mysql-test")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Type != "mysql" || profile.Port != 3306 || profile.Password == "" {
		t.Fatalf("profile was not loaded: %+v", profile)
	}
	spec, err := profile.BuildConnectionSpec()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Type != "mysql" || !strings.Contains(spec.DSN, "db.example.test") || !strings.Contains(spec.DSN, "roam_test") {
		t.Fatalf("unexpected mysql DSN: %q", spec.DSN)
	}
}

func TestLoadFileRejectsDuplicateAndUnknownFields(t *testing.T) {
	for _, body := range []string{
		`{"alias": {"type":"mysql","type":"mysql","host":"db","port":3306,"database":"db","user":"u","password":"p","tls":true}}`,
		`{"alias": {"type":"mysql","host":"db","port":3306,"database":"db","user":"u","password":"p","tls":true,"unexpected":true}}`,
		`{"alias": {"type":"mysql","host":"db","port":3306,"database":"db","user":"u","password":"p","tls":true}} trailing`,
	} {
		if _, err := LoadFile(writeProfile(t, body), "alias"); err == nil {
			t.Fatalf("invalid profile accepted: %s", body)
		}
	}
}

func TestLoadFileRejectsPlaceholdersAndInsecureTLS(t *testing.T) {
	for _, body := range []string{
		`{"alias":{"type":"mysql","host":"<host>","port":3306,"database":"db","user":"u","password":"p","tls":true}}`,
		`{"alias":{"type":"postgres","host":"db","port":5432,"database":"db","user":"u","password":"p","sslmode":"disable"}}`,
		`{"alias":{"type":"mysql","host":"db","port":3306,"database":"db","user":"u","password":"p","tls":false}}`,
	} {
		if _, err := LoadFile(writeProfile(t, body), "alias"); err == nil {
			t.Fatalf("unsafe profile accepted: %s", body)
		}
	}
}

func TestPostgresDSNPreservesTLSAndChannelBinding(t *testing.T) {
	profile, err := LoadFile(writeProfile(t, validPostgresProfile()), "roam-postgres-test")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := profile.BuildConnectionSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"sslmode=verify-full", "channel_binding=require", "db.example.test", "roam_test"} {
		if !strings.Contains(spec.DSN, value) {
			t.Fatalf("DSN lost %q: %q", value, spec.DSN)
		}
	}
}

func TestMySQLDSNPreservesDriverDefaults(t *testing.T) {
	profile, err := LoadFile(writeProfile(t, validMySQLProfile()), "roam-mysql-test")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := profile.BuildConnectionSpec()
	if err != nil {
		t.Fatal(err)
	}
	config, err := mysqlDriver.ParseDSN(spec.DSN)
	if err != nil {
		t.Fatal(err)
	}
	if !config.AllowNativePasswords || !config.CheckConnLiveness {
		t.Fatalf("driver defaults were lost: allowNative=%v liveness=%v", config.AllowNativePasswords, config.CheckConnLiveness)
	}
	if config.TLSConfig != "true" || !config.ParseTime || config.Loc.String() != "Local" || !strings.Contains(spec.DSN, "charset=utf8mb4") {
		t.Fatalf("mysql connection parameters were not preserved: %+v", config)
	}
}

func TestLoadFileRejectsMissingExactAlias(t *testing.T) {
	if _, err := LoadFile(writeProfile(t, validMySQLProfile()), "roam-postgres-test"); err != ErrAliasMissing {
		t.Fatalf("error=%v, want ErrAliasMissing", err)
	}
}
