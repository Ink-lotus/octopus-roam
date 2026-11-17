// The backup acceptance entry point uses synthetic, isolated databases only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/glebarez/sqlite"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var sourceCommit = "unbound"
var sourceTreeSHA256 = "unbound"
var sourceCommitExact = "false"

const fixturePassword = "synthetic-backup-sequence-reproduction-only"
const fixtureChannel = "backup-sequence-repro-channel"
const fixtureModel = "backup-sequence-repro-model"
const unusedModel = "backup-sequence-repro-unused-model"

type options struct {
	stage, dialect, sqlitePath, backupPath, reportPath string
	scenario, crossBackup                              string
	allowWrites                                        bool
}

type check struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details any    `json:"details,omitempty"`
}

type report struct {
	SourceCommit      string  `json:"source_commit"`
	SourceCommitExact bool    `json:"source_commit_exact"`
	SourceTreeSHA256  string  `json:"source_tree_sha256"`
	Stage             string  `json:"stage"`
	Dialect           string  `json:"dialect"`
	EngineVersion     string  `json:"engine_version,omitempty"`
	BackupSHA256      string  `json:"backup_sha256,omitempty"`
	Checks            []check `json:"checks"`
	Passed            bool    `json:"passed"`
	Failure           string  `json:"failure,omitempty"`
}

type suite struct {
	ctx    context.Context
	opts   options
	dsn    string
	scope  string
	report *report
}

func main() {
	var o options
	flag.StringVar(&o.stage, "stage", "verify", "fixture, fixture-json, verify, upgrade, integrity, statistics, statistics-prepare, statistics-upgrade, matrix, cross-export, or cross-import")
	flag.StringVar(&o.scenario, "scenario", "", "isolated B04 matrix case C01 through C08")
	flag.StringVar(&o.crossBackup, "cross-backup", "", "new export or existing synthetic cross-database encrypted backup")
	flag.StringVar(&o.dialect, "db", "sqlite", "sqlite, postgres, or mysql")
	flag.StringVar(&o.sqlitePath, "sqlite-path", "", "new synthetic SQLite database")
	flag.StringVar(&o.backupPath, "backup", "", "synthetic encrypted fixture")
	flag.StringVar(&o.reportPath, "report", "", "new JSON acceptance report")
	flag.BoolVar(&o.allowWrites, "allow-isolated-write", false, "allow writes to the isolated synthetic database")
	flag.Parse()
	r := report{SourceCommit: sourceCommit, SourceCommitExact: sourceCommitExact == "true", SourceTreeSHA256: sourceTreeSHA256, Stage: o.stage, Dialect: o.dialect, Checks: []check{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s := suite{ctx: ctx, opts: o, report: &r}
	err := s.run()
	r.Passed = err == nil
	if err != nil {
		name := ""
		if len(r.Checks) > 0 {
			name = r.Checks[len(r.Checks)-1].Name
		}
		r.Failure = failureMessage(err, name)
	}
	if o.reportPath == "" || writeJSON(o.reportPath, &r) != nil {
		fmt.Fprintln(os.Stderr, "could not create a new acceptance report")
		os.Exit(2)
	}
	_ = json.NewEncoder(os.Stdout).Encode(&r)
	if err != nil {
		os.Exit(1)
	}
}

// Only synthetic invariant descriptions and SQLSTATE codes may enter reports.
// Driver errors and string-wrapped import errors can contain private values.
type assertionFailure struct{ reason string }

func (e *assertionFailure) Error() string { return e.reason }

func failureMessage(err error, name string) string {
	message := "acceptance failed"
	if name != "" {
		message += ": " + name
	}
	var assertion *assertionFailure
	if errors.As(err, &assertion) {
		message += ": " + assertion.reason
	}
	var pgError *pgconn.PgError
	code := ""
	if errors.As(err, &pgError) {
		code = pgError.Code
	} else if match := regexp.MustCompile(`\bSQLSTATE ([0-9A-Z]{5})\b`).FindStringSubmatch(err.Error()); len(match) == 2 {
		code = match[1]
	}
	if regexp.MustCompile(`^[0-9A-Z]{5}$`).MatchString(code) {
		message += " (SQLSTATE " + code + ")"
	}
	return message
}

func (s *suite) step(name string, fn func() (any, error)) error {
	if s.scope != "" {
		name = s.scope + "/" + name
	}
	details, err := fn()
	s.report.Checks = append(s.report.Checks, check{Name: name, Passed: err == nil, Details: details})
	fmt.Printf("check=%s passed=%t\n", name, err == nil)
	return err
}

func (s *suite) run() error {
	if !s.opts.allowWrites || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(sourceCommit) ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(sourceTreeSHA256) || s.opts.reportPath == "" || s.opts.backupPath == "" {
		return errors.New("isolated-write flag, source bindings, backup and report are required")
	}
	if _, err := os.Lstat(s.opts.reportPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("report must be new")
	}
	if s.opts.stage != "fixture" && s.opts.stage != "verify" && s.opts.stage != "upgrade" && s.opts.stage != "integrity" &&
		s.opts.stage != "statistics" && s.opts.stage != "statistics-prepare" && s.opts.stage != "statistics-upgrade" &&
		s.opts.stage != "matrix" && s.opts.stage != "cross-export" && s.opts.stage != "cross-import" && s.opts.stage != "fixture-json" {
		return errors.New("unknown stage")
	}
	if err := s.step("isolated-target", func() (any, error) {
		var err error
		s.dsn, err = isolatedDSN(s.opts)
		return nil, err
	}); err != nil {
		return err
	}
	if s.opts.stage == "fixture" {
		return s.makeFixture()
	}
	raw, err := os.ReadFile(s.opts.backupPath)
	if err != nil {
		return err
	}
	dump, err := op.DecryptBackup(raw, fixturePassword)
	if err != nil {
		return err
	}
	if dump.Product != "octopus-roam" || (dump.Version != 1003 && dump.Version != 1004) || len(dump.Channels) != 1 ||
		dump.Channels[0].ID != 1 || dump.Channels[0].Name != fixtureChannel || len(dump.ChannelKeys) != 3 ||
		len(dump.ChannelModels) != 2 || len(dump.ChannelGrants) != 3 || len(dump.Groups) != 1 ||
		dump.Groups[0].Name != fixtureModel || len(dump.GroupItems) != 2 || len(dump.GroupCategories) != 1 {
		return errors.New("backup is not the synthetic acceptance fixture")
	}
	s.report.BackupSHA256 = digest(raw)
	if s.opts.stage == "fixture-json" {
		return s.step("export-known-synthetic-fixture-json", func() (any, error) {
			if s.opts.crossBackup == "" {
				return nil, errors.New("new synthetic JSON output path is required")
			}
			return nil, writeJSON(s.opts.crossBackup, dump)
		})
	}
	if s.opts.stage == "statistics-prepare" {
		return s.prepareStatisticsUpgrade(dump)
	}
	if s.opts.stage == "statistics-upgrade" {
		return s.verifyStatisticsUpgrade(dump)
	}
	if s.opts.stage == "upgrade" {
		if s.opts.dialect != "postgres" {
			return errors.New("legacy sequence upgrade requires PostgreSQL")
		}
		return s.verifyLegacyUpgrade(dump)
	}
	if err := assertEmpty(s.ctx, s.opts.dialect, s.dsn); err != nil {
		return err
	}
	if s.opts.stage == "matrix" {
		return s.verifyJointCase(dump)
	}
	if s.opts.stage == "cross-export" || s.opts.stage == "cross-import" {
		return s.verifyCrossDatabase(dump)
	}
	if s.opts.stage == "statistics" {
		return s.verifyStatistics()
	}
	if s.opts.stage == "integrity" {
		return s.verifyIntegrity(dump)
	}
	if s.opts.dialect == "postgres" {
		return s.verifyPostgres(dump)
	}
	return s.verifyCommon(s.dsn, dump)
}

func isolatedDSN(o options) (string, error) {
	if o.dialect == "sqlite" {
		if o.sqlitePath == "" {
			return "", errors.New("new SQLite path required")
		}
		path, err := filepath.Abs(o.sqlitePath)
		if err != nil {
			return "", err
		}
		if s := o.stage; s == "statistics-prepare" || s == "statistics-upgrade" {
			if filepath.Base(path) != "legacy-statistics.sqlite" {
				return "", errors.New("statistics upgrade requires its dedicated legacy SQLite fixture")
			}
			if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() {
				return "", errors.New("legacy statistics SQLite fixture missing")
			}
		} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return "", errors.New("SQLite path must be new")
		}
		return path, nil
	}
	dsn := os.Getenv("ROAM_BACKUP_TEST_DSN")
	if dsn == "" {
		return "", errors.New("synthetic DSN environment variable missing")
	}
	switch o.dialect {
	case "postgres":
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil || cfg.Host != "127.0.0.1" || (cfg.Database != "roam_backup_b01" && cfg.Database != "roam_backup_repro" && cfg.Database != "roam_backup_b02" && cfg.Database != "roam_backup_b02_upgrade" && cfg.Database != "roam_backup_b03" && !jointDatabaseName(cfg.Database)) {
			return "", errors.New("PostgreSQL target must be the isolated loopback acceptance database")
		}
		for _, fallback := range cfg.Fallbacks {
			if fallback.Host != "127.0.0.1" {
				return "", errors.New("PostgreSQL fallback must also be loopback")
			}
		}
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil || cfg.Net != "tcp" || (cfg.DBName != "roam_backup_b01" && cfg.DBName != "roam_backup_b02" && cfg.DBName != "roam_backup_b02_upgrade" && cfg.DBName != "roam_backup_b03" && !jointDatabaseName(cfg.DBName)) {
			return "", errors.New("invalid isolated MySQL target")
		}
		host, _, err := net.SplitHostPort(cfg.Addr)
		if err != nil || host != "127.0.0.1" {
			return "", errors.New("MySQL target must be loopback")
		}
	default:
		return "", errors.New("unsupported database")
	}
	return dsn, nil
}

func postgresDSN(dsn, schemaName, role string) (string, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "postgresql", User: url.UserPassword(cfg.User, cfg.Password), Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database}
	q := url.Values{"sslmode": {"disable"}, "connect_timeout": {"10"}, "search_path": {quote(schemaName) + ", public"}}
	if role != "" {
		q.Set("role", role)
	}
	// PostgreSQL URI query values use percent encoding, not form encoding:
	// libpq/pgx treats '+' as a literal character instead of a space.
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	return u.String(), nil
}

func rawDB(dialect, dsn string) (*gorm.DB, error) {
	var driver gorm.Dialector
	if dialect == "postgres" {
		driver = postgres.Open(dsn)
	} else if dialect == "sqlite" {
		driver = sqlite.Open(dsn + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	} else {
		driver = mysql.Open(dsn)
	}
	return gorm.Open(driver, &gorm.Config{Logger: logger.Discard})
}

func assertEmpty(ctx context.Context, dialect, dsn string) error {
	if dialect == "sqlite" {
		return nil
	}
	conn, err := rawDB(dialect, dsn)
	if err != nil {
		return errors.New("isolated preflight connection failed")
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()"
	if dialect == "postgres" {
		query = "SELECT COUNT(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' AND c.relkind IN ('r','p','v','m','f','S')"
	}
	var count int64
	if err := conn.WithContext(ctx).Raw(query).Scan(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errors.New("acceptance database must be empty")
	}
	return nil
}

func (s *suite) boot(dsn string) error {
	if err := db.InitDB(s.opts.dialect, dsn, false); err != nil {
		return err
	}
	if err := op.InitCache(); err != nil {
		return err
	}
	query := "SELECT VERSION()"
	if s.opts.dialect == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	var version string
	if err := db.GetDB().WithContext(s.ctx).Raw(query).Scan(&version).Error; err != nil {
		return err
	}
	s.report.EngineVersion = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){1,3}`).FindString(version)
	if s.opts.dialect == "postgres" && !strings.HasPrefix(s.report.EngineVersion, "18.") {
		return errors.New("PostgreSQL 18 required")
	}
	if s.opts.dialect == "mysql" && !strings.HasPrefix(s.report.EngineVersion, "8.4.") {
		return errors.New("MySQL 8.4 required")
	}
	return nil
}

func writeJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeExclusive(path, append(raw, '\n'))
}

func writeExclusive(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(raw)
	return err
}
