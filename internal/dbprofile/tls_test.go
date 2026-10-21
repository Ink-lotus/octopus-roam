package dbprofile

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

func TestMySQLProfileTLSCertificateVerification(t *testing.T) {
	ca, caKey, caPEM := syntheticTLSCA(t)
	_, _, unrelatedPEM := syntheticTLSCA(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"db.example.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{leaf}, PrivateKey: privateKey}
	for _, tc := range []struct {
		name string
		host string
		ca   []byte
	}{
		{"trusted_ca", "db.example.test", caPEM},
		{"unknown_ca", "db.example.test", unrelatedPEM},
		{"hostname_mismatch", "other.example.test", caPEM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caPath := filepath.Join(t.TempDir(), "synthetic-ca.pem")
			if err := os.WriteFile(caPath, tc.ca, 0o600); err != nil {
				t.Fatal(err)
			}
			profile := Profile{Type: "mysql", Host: tc.host, Port: 3306, Database: "synthetic_test", User: "synthetic", Password: "synthetic-password", TLS: true, CAFile: caPath}
			spec, err := profile.BuildConnectionSpec()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mysqlDriver.DeregisterTLSConfig(spec.TLSName) })
			config, err := mysqlDriver.ParseDSN(spec.DSN)
			if err != nil {
				t.Fatal("could not parse synthetic connection spec")
			}
			if config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.ServerName != tc.host || config.TLS.MinVersion < tls.VersionTLS12 {
				t.Fatal("driver TLS configuration lost certificate or hostname verification")
			}
			clientPipe, serverPipe := net.Pipe()
			defer clientPipe.Close()
			defer serverPipe.Close()
			_ = clientPipe.SetDeadline(time.Now().Add(5 * time.Second))
			_ = serverPipe.SetDeadline(time.Now().Add(5 * time.Second))
			serverResult := make(chan error, 1)
			go func() {
				server := tls.Server(serverPipe, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}})
				serverResult <- server.Handshake()
			}()
			client := tls.Client(clientPipe, config.TLS.Clone())
			err = client.Handshake()
			_ = clientPipe.Close()
			serverErr := <-serverResult
			switch tc.name {
			case "trusted_ca":
				if err != nil || serverErr != nil {
					t.Fatalf("trusted synthetic certificate rejected: client=%v, server=%v", err, serverErr)
				}
			case "unknown_ca":
				var unknown x509.UnknownAuthorityError
				if !errors.As(err, &unknown) {
					t.Fatalf("expected unknown-authority rejection, got %v", err)
				}
			case "hostname_mismatch":
				var hostname x509.HostnameError
				if !errors.As(err, &hostname) {
					t.Fatalf("expected hostname rejection, got %v", err)
				}
			}
		})
	}
}

func syntheticTLSCA(t *testing.T) (*x509.Certificate, ed25519.PrivateKey, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, privateKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
