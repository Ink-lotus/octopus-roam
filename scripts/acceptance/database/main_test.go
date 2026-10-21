package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/dbprofile"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/glebarez/sqlite"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMySQLRuntimeConfigDoesNotDependOnParentTLSRegistry(t *testing.T) {
	const name = "synthetic-parent-process-tls"
	if err := mysqlDriver.RegisterTLSConfig(name, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: "db.example.test"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlDriver.DeregisterTLSConfig(name) })
	s := suite{kind: "mysql", dsn: "synthetic:synthetic-password@tcp(db.example.test:3306)/fixture?tls=" + name, caFile: "synthetic-ca.pem"}
	config, err := s.productDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	// Emulate the fresh process: the parent-only TLS name no longer exists.
	mysqlDriver.DeregisterTLSConfig(name)
	if _, err := mysqlDriver.ParseDSN(s.dsn); err == nil {
		t.Fatal("synthetic parent registry dependency was not reproduced")
	}
	parsed, err := mysqlDriver.ParseDSN(config["path"])
	if err != nil || parsed.TLSConfig != "true" || config["mysql_ca_file"] != s.caFile || parsed.Passwd != "synthetic-password" {
		t.Fatal("child config must carry ordinary verified TLS plus its CA file")
	}
}

func TestConnectFlagPrecedesAnyProfileRead(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	os.Args = []string{"acceptance", "--profile", "must-not-be-opened", "--alias", "roam-mysql-test"}
	r, code := execute()
	if code == 0 || r.Status != "not_run" || r.Error != "connect_flag_required" {
		t.Fatal("missing connection gate")
	}
}

func TestResultBoundaryRejectsUntrustedFields(t *testing.T) {
	valid := result{Status: "passed", Alias: "roam-mysql-test", Type: "mysql", Stage: "seed", EngineVersion: "8.4.2", Checks: []check{{Name: "synthetic_case", Status: "passed"}}}
	if !validResult(valid, valid.Alias, valid.Stage) {
		t.Fatal("valid result rejected")
	}
	cases := []result{valid, valid, valid, valid}
	cases[0].Error = "synthetic private driver error"
	cases[1].EngineVersion = "8.4.2-private-host"
	cases[2].Alias = "another-target"
	cases[3].Checks = []check{{Name: "synthetic private value", Status: "passed"}}
	for _, r := range cases {
		if validResult(r, valid.Alias, valid.Stage) {
			t.Fatal("untrusted field accepted")
		}
	}
}

func TestErrorClassificationContainsNoOriginalMessage(t *testing.T) {
	for _, message := range []string{"password for synthetic-private-user rejected", "x509: synthetic-private-host", "deadline exceeded at synthetic-private-host", "unknown synthetic-private-error"} {
		got := classify(errors.New(message))
		if !safeError(got) || got == message {
			t.Fatal("error output is not allowlisted")
		}
	}
}

func TestMalformedProfileDoesNotExposeSubmittedValues(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "synthetic-profiles.json")
	if err := os.WriteFile(filename, []byte(`{"roam-mysql-test":{"password":"synthetic-private-marker",broken}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := dbprofile.LoadFile(filename, "roam-mysql-test")
	if err == nil || classify(err) != "profile_invalid" {
		t.Fatal("malformed profile was not rejected safely")
	}
}

func TestRealTLSFailureStaysInsideOutputBoundary(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"synthetic-private-host.invalid"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		// PostgreSQL SSLRequest followed by a real, untrusted TLS endpoint.
		var request [8]byte
		if _, err := io.ReadFull(conn, request[:]); err != nil {
			return
		}
		if _, err := conn.Write([]byte("S")); err != nil {
			return
		}
		secure := tls.Server(conn, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
		_ = secure.Handshake()
	}()
	profile := dbprofile.Profile{Type: "postgres", Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Database: "synthetic-private-database", User: "synthetic-private-user", Password: "synthetic-private-marker", SSLMode: "verify-full", ChannelBinding: "prefer"}
	r := runStage(options{alias: "roam-postgres-test", stage: "preflight", connect: true}, profile)
	if r.Status != "failed" || r.Error != "tls_validation_failed" || !validResult(r, "roam-postgres-test", "preflight") {
		t.Fatal("TLS failure was not classified safely")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"synthetic-private-host", "synthetic-private-database", "synthetic-private-user", "synthetic-private-marker"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("private marker reached result output")
		}
	}
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("TLS fixture did not finish")
	}
}

func TestNonEmptyTargetIsRejectedBeforeMigration(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "nonempty.db")
	conn, err := gorm.Open(sqlite.Open(filename), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := conn.Exec("CREATE TABLE preserved (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatal(err)
	}
	if err := assertEmpty(context.Background(), "sqlite", filename); !errors.Is(err, errNotEmpty) {
		t.Fatal("nonempty target accepted")
	}
	var count int64
	if err := conn.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'").Scan(&count).Error; err != nil || count != 1 {
		t.Fatal("preflight changed the schema")
	}
}

func TestCanonicalIgnoresOnlyExpectedBackupDifferences(t *testing.T) {
	first := &model.DBDump{Product: "octopus-roam", Version: 1000, ExportedAt: time.Now(), Channels: []model.Channel{{ID: 1, ChannelConfig: model.ChannelConfig{Name: "fixture", Enabled: false}}}, Settings: []model.Setting{{Key: model.SettingKeyPrivacyProtectionRules, Value: privacy.DefaultConfigJSON()}}}
	a, err := canonical(first)
	if err != nil {
		t.Fatal(err)
	}
	first.ExportedAt = first.ExportedAt.Add(time.Hour)
	first.Settings[0].Value = privacy.DefaultConfigJSON()
	b, err := canonical(first)
	if err != nil || string(a) != string(b) {
		t.Fatal("expected metadata differences affected comparison")
	}
	first.Channels[0].Enabled = true
	c, err := canonical(first)
	if err != nil || string(b) == string(c) {
		t.Fatal("configuration corruption was ignored")
	}
}
