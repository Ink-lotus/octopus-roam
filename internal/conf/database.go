package conf

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

// ConnectionPath registers custom MySQL trust in the process that opens the
// database. A TLS registry name from another process cannot supply its CA.
func (d Database) ConnectionPath() (string, error) {
	if d.MySQLCAFile == "" {
		return d.Path, nil
	}
	invalid := errors.New("invalid MySQL CA configuration")
	if !strings.EqualFold(d.Type, "mysql") {
		return "", invalid
	}
	path := d.Path
	// Preserve initMySQL's defaults before the custom TLS option adds a query.
	if !strings.Contains(path, "?") {
		path += "?charset=utf8mb4&parseTime=True&loc=Local"
	}
	config, err := mysqlDriver.ParseDSN(path)
	if err != nil || config.Net != "tcp" {
		return "", invalid
	}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil || host == "" {
		return "", invalid
	}
	certificate, err := os.ReadFile(d.MySQLCAFile)
	if err != nil {
		return "", invalid
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return "", invalid
	}
	digest := sha256.Sum256(append([]byte(host+"\x00"), certificate...))
	name := fmt.Sprintf("octopus-roam-runtime-%x", digest[:16])
	if err := mysqlDriver.RegisterTLSConfig(name, &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
		return "", invalid
	}
	config.TLS, config.TLSConfig, config.AllowFallbackToPlaintext = nil, name, false
	return config.FormatDSN(), nil
}
