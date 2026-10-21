package conf

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func syntheticDatabaseCA(t *testing.T) (string, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"db.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "synthetic-ca.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	return caFile, der
}

func TestDatabaseMySQLCARegistersVerifiedTLS(t *testing.T) {
	caFile, der := syntheticDatabaseCA(t)
	for _, mode := range []string{"true", "preferred", "skip-verify", "false"} {
		t.Run(mode, func(t *testing.T) {
			database := Database{Type: "mysql", Path: "synthetic:synthetic-password@tcp(db.example.test:3306)/fixture?parseTime=true&tls=" + mode, MySQLCAFile: caFile}
			dsn, err := database.ConnectionPath()
			if err != nil {
				t.Fatal(err)
			}
			config, err := mysqlDriver.ParseDSN(dsn)
			if err != nil {
				t.Fatal("could not parse synthetic connection")
			}
			t.Cleanup(func() { mysqlDriver.DeregisterTLSConfig(config.TLSConfig) })
			if config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.ServerName != "db.example.test" || config.TLS.MinVersion < tls.VersionTLS12 || config.AllowFallbackToPlaintext {
				t.Fatal("custom CA must enforce hostname verification and TLS without plaintext fallback")
			}
			if config.User != "synthetic" || config.Passwd != "synthetic-password" || config.DBName != "fixture" || !config.ParseTime {
				t.Fatal("registering trust changed ordinary connection options")
			}
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := certificate.Verify(x509.VerifyOptions{Roots: config.TLS.RootCAs, DNSName: config.TLS.ServerName}); err != nil {
				t.Fatal("trusted synthetic server rejected")
			}
			if _, err := certificate.Verify(x509.VerifyOptions{Roots: config.TLS.RootCAs, DNSName: "wrong.example.test"}); err == nil {
				t.Fatal("hostname mismatch accepted")
			}
		})
	}
}

func TestDatabaseMySQLCAPreservesOriginalQueryDefaults(t *testing.T) {
	caFile, der := syntheticDatabaseCA(t)
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, charset string
		parseTime            bool
		location             *time.Location
	}{
		{name: "no query", parseTime: true, location: time.Local, charset: "utf8mb4"},
		{name: "explicit options", query: "?parseTime=false&loc=UTC&charset=latin1", location: time.UTC, charset: "latin1"},
		{name: "unrelated query", query: "?timeout=5s", location: time.UTC},
		{name: "empty query", query: "?", location: time.UTC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := Database{Type: "mysql", Path: "synthetic:synthetic-password@tcp(db.example.test:3306)/fixture" + tc.query, MySQLCAFile: caFile}
			dsn, err := database.ConnectionPath()
			if err != nil {
				t.Fatal(err)
			}
			config, err := mysqlDriver.ParseDSN(dsn)
			if err != nil {
				t.Fatal("could not parse synthetic connection")
			}
			t.Cleanup(func() { mysqlDriver.DeregisterTLSConfig(config.TLSConfig) })
			// mysql v1.10 stores charset internally, so inspect the formatted query.
			_, query, _ := strings.Cut(config.FormatDSN(), "?")
			params, err := url.ParseQuery(query)
			if err != nil {
				t.Fatal("could not parse synthetic connection options")
			}
			if config.ParseTime != tc.parseTime || config.Loc != tc.location || params.Get("charset") != tc.charset {
				t.Errorf("connection defaults: parseTime=%v loc=%v charset=%q; want %v %v %q", config.ParseTime, config.Loc, params.Get("charset"), tc.parseTime, tc.location, tc.charset)
			}
			if tc.query == "?timeout=5s" && config.Timeout != 5*time.Second {
				t.Error("unrelated explicit query option changed")
			}
			if config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.ServerName != "db.example.test" || config.TLS.MinVersion < tls.VersionTLS12 || config.AllowFallbackToPlaintext {
				t.Fatal("custom CA must retain strict TLS")
			}
			if _, err := certificate.Verify(x509.VerifyOptions{Roots: config.TLS.RootCAs, DNSName: config.TLS.ServerName}); err != nil {
				t.Fatal("trusted synthetic server rejected")
			}
		})
	}
}

func TestDatabaseWithoutCAReturnsOriginalDSN(t *testing.T) {
	for _, query := range []string{"", "?", "?parseTime=false&loc=UTC&charset=latin1", "?timeout=5s"} {
		database := Database{Type: "mysql", Path: "synthetic:synthetic-password@tcp(db.example.test:3306)/fixture" + query}
		if path, err := database.ConnectionPath(); err != nil || path != database.Path {
			t.Fatal("configuration without a custom CA changed")
		}
	}
}

func TestDatabaseCARejectsInvalidConfigurationSafely(t *testing.T) {
	privatePath := filepath.Join(t.TempDir(), "synthetic-private-ca.pem")
	if err := os.WriteFile(privatePath, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, database := range []Database{
		{Type: "mysql", Path: "synthetic:synthetic-password@tcp(localhost:3306)/fixture", MySQLCAFile: privatePath},
		{Type: "mysql", Path: "synthetic:synthetic-password@tcp(localhost:3306)/fixture", MySQLCAFile: privatePath + ".missing"},
		{Type: "mysql", Path: "synthetic-private-invalid-dsn", MySQLCAFile: privatePath},
		{Type: "mysql", Path: "synthetic:synthetic-password@unix(/synthetic/mysql.sock)/fixture", MySQLCAFile: privatePath},
		{Type: "mysql", Path: "synthetic:synthetic-password@tcp(:3306)/fixture", MySQLCAFile: privatePath},
		{Type: "postgres", Path: "synthetic-private-dsn", MySQLCAFile: privatePath},
	} {
		path, err := database.ConnectionPath()
		if err == nil || path != "" || strings.Contains(err.Error(), "synthetic") {
			t.Fatal("invalid CA configuration was accepted or private details escaped")
		}
	}
	for _, kind := range []string{"sqlite", "mysql", "postgres"} {
		database := Database{Type: kind, Path: "existing-connection"}
		if path, err := database.ConnectionPath(); err != nil || path != database.Path {
			t.Fatal("configuration without a custom CA changed")
		}
	}
}
