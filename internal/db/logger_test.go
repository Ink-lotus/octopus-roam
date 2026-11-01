package db

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDebugSQLKeepsStatementScopeAndOriginalErrors(t *testing.T) {
	var output bytes.Buffer
	sqlLogger := debugLogger(log.New(&output, "", 0)).LogMode(logger.Info)
	conn, err := initSQLite(filepath.Join(t.TempDir(), "log.db"), &gorm.Config{Logger: sqlLogger})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := conn.Exec("CREATE TABLE log_values (value TEXT UNIQUE)").Error; err != nil {
		t.Fatal(err)
	}
	const value = "fixture-database-value"
	if err := conn.Exec("INSERT INTO log_values (value) VALUES (?)", value).Error; err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := conn.Raw("SELECT value FROM log_values WHERE value = ?", value).Row().Scan(&stored); err != nil || stored != value {
		t.Fatal("logging changed the stored value", err)
	}
	if strings.Contains(output.String(), value) || !strings.Contains(output.String(), "INSERT INTO log_values") || !strings.Contains(output.String(), "[rows:") {
		t.Fatal("SQL logging changed its statement/parameter collection scope")
	}
	output.Reset()
	// GORM 1.31.2 Scan uses its own recorder before invoking the configured
	// logger. It already expanded parameters with the previous ParamsFilter
	// wrapper too; retaining the native logger does not broaden that path.
	if err := conn.Raw("SELECT value FROM log_values WHERE value = ?", value).Scan(&stored).Error; err != nil || stored != value {
		t.Fatal("native Scan failed", err)
	}
	if !strings.Contains(output.String(), value) {
		t.Fatal("GORM Scan recorder behavior changed; reassess its collection scope")
	}
	output.Reset()
	message := "driver rejected value " + value
	sqlLogger.Trace(context.Background(), time.Now(), func() (string, int64) {
		return "INSERT INTO log_values (value) VALUES (?)", 0
	}, errors.New(message))
	sqlLogger.Error(context.Background(), "initialization failed: %v", errors.New(message))
	if strings.Count(output.String(), message) != 2 || strings.Contains(output.String(), "[redacted:") {
		t.Fatal("database error diagnostics were replaced")
	}
}
