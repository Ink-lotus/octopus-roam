package integrity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type AuditReport struct {
	ReadOnly    bool             `json:"read_only"`
	Complete    bool             `json:"complete"`
	Stage       string           `json:"stage"`
	Counts      map[string]int64 `json:"counts"`
	Issues      []Issue          `json:"issues"`
	Limitations []string         `json:"limitations"`
}

var ErrAuditUnavailable = errors.New("read-only audit could not read the complete database; no migrations or repairs were attempted")

// Audit opens an independent connection. It never calls InitDB/InitCache,
// AutoMigrate, settings defaults, sequence repair or any application writer.
// Database-enforced read-only transactions protect against accidental SQL writes.
func Audit(ctx context.Context, dialect, dsn string) (*AuditReport, error) {
	report := &AuditReport{ReadOnly: true, Counts: map[string]int64{}, Issues: []Issue{}, Limitations: []string{
		"Overwritten credentials, deleted members and plausible but incorrect hourly values cannot be reconstructed from the current database; compare a trusted independent backup.",
		"The audit reports one database snapshot and does not repair data or prove that past imports were lossless.",
	}}
	config := &gorm.Config{Logger: logger.Discard, DisableAutomaticPing: true}
	report.Stage = "open_connection"
	var conn *gorm.DB
	var err error
	switch dialect {
	case "sqlite":
		path, e := filepath.Abs(dsn)
		if e != nil {
			return report, ErrAuditUnavailable
		}
		info, e := os.Stat(path)
		if e != nil || !info.Mode().IsRegular() {
			return report, ErrAuditUnavailable
		}
		uriPath := filepath.ToSlash(path)
		if filepath.VolumeName(path) != "" {
			uriPath = "/" + uriPath
		}
		uri := url.URL{Scheme: "file", Path: uriPath}
		params := url.Values{"mode": {"ro"}, "_pragma": {"query_only(ON)", "busy_timeout(5000)"}}
		uri.RawQuery = params.Encode()
		conn, err = gorm.Open(sqlite.Open(uri.String()), config)
	case "mysql":
		conn, err = gorm.Open(mysql.Open(dsn), config)
	case "postgres", "postgresql":
		conn, err = gorm.Open(postgres.Open(dsn), config)
	default:
		return report, ErrAuditUnavailable
	}
	if err != nil {
		return report, ErrAuditUnavailable
	}
	pool, err := conn.DB()
	if err != nil {
		return report, ErrAuditUnavailable
	}
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	report.Stage = "begin_read_only"
	err = conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		report.Stage = "read_graph"
		d, err := ReadGraph(tx)
		if err != nil {
			return err
		}
		report.Issues = Inspect(d)
		for _, table := range []string{"channels", "channel_keys", "channel_models", "channel_grants", "group_categories", "groups", "group_items", "api_keys", "webhook_endpoints", "llm_infos", "settings", "stats_totals", "stats_dailies", "stats_hourlies", "stats_api_keys", "migration_records"} {
			var count int64
			if err := tx.Table(table).Count(&count).Error; err != nil {
				return err
			}
			report.Counts[table] = count
		}
		report.Stage = "read_schema"
		if err := auditBusinessKeys(tx, report); err != nil {
			return err
		}
		if tx.Dialector.Name() == "postgres" {
			report.Stage = "read_sequences"
			if err := auditSequences(tx, report); err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return report, ErrAuditUnavailable
	}
	report.Complete = true
	report.Stage = "complete"
	return report, nil
}

func auditBusinessKeys(tx *gorm.DB, r *AuditReport) error {
	for _, target := range []struct{ table, column string }{{"stats_totals", "id"}, {"stats_hourlies", "hour"}, {"stats_api_keys", "api_key_id"}, {"migration_records", "version"}} {
		automatic := false
		switch tx.Dialector.Name() {
		case "sqlite":
			var ddl string
			if err := tx.Raw("SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = ?", target.table).Scan(&ddl).Error; err != nil {
				return err
			}
			automatic = strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT")
		case "mysql":
			var extra string
			if err := tx.Raw("SELECT extra FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", target.table, target.column).Scan(&extra).Error; err != nil {
				return err
			}
			automatic = strings.Contains(strings.ToLower(extra), "auto_increment")
		case "postgres":
			var result struct{ Automatic bool }
			if err := tx.Raw(`SELECT (a.attidentity <> '' OR pg_catalog.pg_get_expr(d.adbin,d.adrelid) LIKE 'nextval(%') AS automatic FROM pg_catalog.pg_attribute a LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=pg_catalog.to_regclass(?) AND a.attname=?`, target.table, target.column).Scan(&result).Error; err != nil {
				return err
			}
			automatic = result.Automatic
		}
		if automatic {
			r.Issues = append(r.Issues, Issue{Table: target.table, Code: "business_key_auto_increment"})
		}
	}
	return nil
}

func quoteIdentifier(v string) string { return `"` + strings.ReplaceAll(v, `"`, `""`) + `"` }

func auditSequences(tx *gorm.DB, r *AuditReport) error {
	for _, table := range []string{"channels", "channel_keys", "channel_models", "channel_grants", "group_categories", "groups", "group_items", "api_keys", "webhook_endpoints"} {
		var info struct {
			Name, Schema string
			Increment    int64
		}
		if err := tx.Raw(`SELECT c.relname AS name,n.nspname AS schema,s.seqincrement AS increment FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace JOIN pg_catalog.pg_sequence s ON s.seqrelid=c.oid WHERE c.oid=pg_catalog.pg_get_serial_sequence(?, 'id')::regclass`, table).Scan(&info).Error; err != nil {
			return err
		}
		if info.Name == "" {
			r.Issues = append(r.Issues, Issue{Table: table, Code: "missing_entity_sequence"})
			continue
		}
		var state struct {
			LastValue int64
			IsCalled  bool
		}
		if err := tx.Raw("SELECT last_value,is_called FROM " + quoteIdentifier(info.Schema) + "." + quoteIdentifier(info.Name)).Scan(&state).Error; err != nil {
			return err
		}
		var highest sql.NullInt64
		if err := tx.Table(table).Select("MAX(id)").Scan(&highest).Error; err != nil {
			return err
		}
		behind := highest.Valid && (state.LastValue < highest.Int64 || state.LastValue == highest.Int64 && !state.IsCalled)
		if info.Increment <= 0 {
			r.Issues = append(r.Issues, Issue{Table: table, Code: "unsupported_sequence_increment"})
		} else if behind {
			r.Issues = append(r.Issues, Issue{Table: table, Code: "sequence_may_collide"})
		}
	}
	return nil
}

func (r *AuditReport) Error() error {
	if !r.Complete {
		return ErrAuditUnavailable
	}
	if len(r.Issues) > 0 {
		return fmt.Errorf("read-only audit found %d integrity issues", len(r.Issues))
	}
	return nil
}
