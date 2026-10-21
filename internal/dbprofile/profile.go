package dbprofile

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

const maxProfileBytes = 1 << 20

var (
	ErrInvalidProfile = errors.New("database profile is invalid")
	ErrProfileMissing = errors.New("database profile is missing")
	ErrAliasMissing   = errors.New("database profile alias is missing")
)

// Profile contains one short-lived, test-only database connection profile.
// Password is kept in memory and is never included in validation results.
type Profile struct {
	Type            string
	Host            string
	Port            int
	Database        string
	RestoreDatabase string
	User            string
	Password        string
	TLS             bool
	SSLMode         string
	ChannelBinding  string
	CAFile          string
}

// ConnectionSpec is the in-memory driver configuration used by the database
// acceptance command. DSN must never be written to logs or command arguments.
type ConnectionSpec struct {
	Type    string
	DSN     string
	TLSName string
}

// LoadFile reads one exact alias from a JSON profile file and validates it.
// The parser rejects duplicate object keys and unknown fields before returning.
func LoadFile(filename, alias string) (Profile, error) {
	if alias == "" || strings.ContainsAny(alias, `/\\`) || strings.ContainsAny(alias, "\x00\r\n") {
		return Profile{}, ErrAliasMissing
	}
	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return Profile{}, ErrProfileMissing
		}
		return Profile{}, ErrInvalidProfile
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > maxProfileBytes {
		return Profile{}, ErrInvalidProfile
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return Profile{}, ErrInvalidProfile
	}
	root, err := decodeObject(data)
	if err != nil {
		return Profile{}, ErrInvalidProfile
	}
	raw, ok := root[alias]
	if !ok {
		return Profile{}, ErrAliasMissing
	}
	profile, err := decodeProfile(raw)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrInvalidProfile, err)
	}
	return profile, nil
}

func decodeObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalidProfile
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || key == "" {
			return nil, ErrInvalidProfile
		}
		if _, exists := result[key]; exists {
			return nil, ErrInvalidProfile
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, ErrInvalidProfile
		}
		result[key] = raw
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalidProfile
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, ErrInvalidProfile
	}
	return result, nil
}

func decodeProfile(raw json.RawMessage) (Profile, error) {
	fields, err := decodeObject(raw)
	if err != nil {
		return Profile{}, err
	}
	allowed := map[string]struct{}{
		"type": {}, "host": {}, "port": {}, "database": {}, "restore_database": {},
		"user": {}, "password": {}, "tls": {}, "sslmode": {}, "channel_binding": {}, "ca_file": {},
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			return Profile{}, ErrInvalidProfile
		}
	}
	profile := Profile{}
	for key, target := range map[string]*string{
		"type": &profile.Type, "host": &profile.Host, "database": &profile.Database,
		"restore_database": &profile.RestoreDatabase, "user": &profile.User, "password": &profile.Password,
		"sslmode": &profile.SSLMode, "channel_binding": &profile.ChannelBinding, "ca_file": &profile.CAFile,
	} {
		value, err := stringField(fields, key, key != "restore_database" && key != "sslmode" && key != "channel_binding" && key != "ca_file")
		if err != nil {
			return Profile{}, err
		}
		*target = value
	}
	profile.Port, err = requiredInt(fields, "port")
	if err != nil {
		return Profile{}, ErrInvalidProfile
	}
	if rawTLS, ok := fields["tls"]; ok {
		if err := json.Unmarshal(rawTLS, &profile.TLS); err != nil {
			return Profile{}, ErrInvalidProfile
		}
	}
	if err := profile.Validate(); err != nil {
		return Profile{}, fmt.Errorf("validate: %w", err)
	}
	profile.Type = strings.ToLower(strings.TrimSpace(profile.Type))
	return profile, nil
}

func stringField(fields map[string]json.RawMessage, key string, required bool) (string, error) {
	raw, ok := fields[key]
	if !ok {
		if required {
			return "", ErrInvalidProfile
		}
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", ErrInvalidProfile
	}
	return value, nil
}

func requiredInt(fields map[string]json.RawMessage, key string) (int, error) {
	raw, ok := fields[key]
	if !ok {
		return 0, ErrInvalidProfile
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, ErrInvalidProfile
	}
	return value, nil
}

// Validate checks only structure and policy. It does not access the network.
func (p Profile) Validate() error {
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	if p.Type != "mysql" && p.Type != "postgres" {
		return fmt.Errorf("type: %w", ErrInvalidProfile)
	}
	if p.Port < 1 || p.Port > 65535 || p.Host == "" || strings.ContainsAny(p.Host, "/\\") || strings.ContainsRune(p.Host, '\x00') || strings.ContainsAny(p.Host, "\r\n") {
		return fmt.Errorf("host or port: %w", ErrInvalidProfile)
	}
	for _, value := range []string{p.Host, p.Database, p.RestoreDatabase, p.User, p.Password, p.SSLMode, p.ChannelBinding, p.CAFile} {
		if hasPlaceholder(value) || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("placeholder: %w", ErrInvalidProfile)
		}
	}
	if p.Database == "" || p.User == "" || p.Password == "" {
		return fmt.Errorf("required: %w", ErrInvalidProfile)
	}
	if p.RestoreDatabase == p.Database && p.RestoreDatabase != "" {
		return fmt.Errorf("restore: %w", ErrInvalidProfile)
	}
	if p.CAFile != "" && !filepath.IsAbs(p.CAFile) {
		return fmt.Errorf("ca path: %w", ErrInvalidProfile)
	}
	switch p.Type {
	case "mysql":
		if !p.TLS || p.SSLMode != "" || p.ChannelBinding != "" {
			return fmt.Errorf("mysql tls fields: %w", ErrInvalidProfile)
		}
	case "postgres":
		if p.TLS {
			return fmt.Errorf("postgres tls field: %w", ErrInvalidProfile)
		}
		if p.SSLMode == "" {
			p.SSLMode = "verify-full"
		}
		switch p.SSLMode {
		case "require", "verify-ca", "verify-full":
		default:
			return fmt.Errorf("ssl mode: %w", ErrInvalidProfile)
		}
		if p.ChannelBinding == "" {
			p.ChannelBinding = "prefer"
		}
		switch p.ChannelBinding {
		case "require", "prefer", "disable":
		default:
			return fmt.Errorf("channel binding: %w", ErrInvalidProfile)
		}
	}
	return nil
}

func hasPlaceholder(value string) bool {
	return strings.Contains(value, "<") || strings.Contains(value, ">")
}

// BuildConnectionSpec creates a driver DSN in memory. A CA file is parsed and
// registered for the MySQL driver; PostgreSQL receives sslrootcert directly.
func (p Profile) BuildConnectionSpec() (ConnectionSpec, error) {
	if err := p.Validate(); err != nil {
		return ConnectionSpec{}, err
	}
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	switch p.Type {
	case "mysql":
		config := mysqlDriver.NewConfig()
		config.User = p.User
		config.Passwd = p.Password
		config.Net = "tcp"
		config.Addr = net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
		config.DBName = p.Database
		config.Params = map[string]string{"charset": "utf8mb4", "parseTime": "True", "loc": "Local"}
		config.TLSConfig = "true"
		tlsName := ""
		if p.CAFile != "" {
			nameBytes := sha256.Sum256([]byte(p.Host + "\x00" + p.CAFile))
			tlsName = "octopus-roam-" + fmt.Sprintf("%x", nameBytes[:8])
			pem, err := os.ReadFile(p.CAFile)
			if err != nil {
				return ConnectionSpec{}, ErrInvalidProfile
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				return ConnectionSpec{}, ErrInvalidProfile
			}
			if err := mysqlDriver.RegisterTLSConfig(tlsName, &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: p.Host}); err != nil {
				return ConnectionSpec{}, ErrInvalidProfile
			}
			config.TLSConfig = tlsName
		}
		return ConnectionSpec{Type: p.Type, DSN: config.FormatDSN(), TLSName: tlsName}, nil
	case "postgres":
		query := url.Values{}
		sslMode := p.SSLMode
		if sslMode == "" {
			sslMode = "verify-full"
		}
		channelBinding := p.ChannelBinding
		if channelBinding == "" {
			channelBinding = "prefer"
		}
		query.Set("sslmode", sslMode)
		query.Set("channel_binding", channelBinding)
		if p.CAFile != "" {
			query.Set("sslrootcert", p.CAFile)
		}
		return ConnectionSpec{Type: p.Type, DSN: (&url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword(p.User, p.Password),
			Host:     net.JoinHostPort(p.Host, strconv.Itoa(p.Port)),
			Path:     "/" + p.Database,
			RawQuery: query.Encode(),
		}).String()}, nil
	default:
		return ConnectionSpec{}, ErrInvalidProfile
	}
}
