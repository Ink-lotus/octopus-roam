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

func TestDebugSQLDoesNotExpandCredentials(t *testing.T) {
	var output bytes.Buffer
	safeLogger := redactedLogger{logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})}.LogMode(logger.Info)
	conn, err := initSQLite(filepath.Join(t.TempDir(), "log.db"), &gorm.Config{Logger: safeLogger})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := conn.Exec("CREATE TABLE log_credentials (value TEXT UNIQUE)").Error; err != nil {
		t.Fatal(err)
	}
	const secret = "fixture-database-key"
	if err := conn.Exec("INSERT INTO log_credentials (value) VALUES (?)", secret).Error; err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := conn.Raw("SELECT value FROM log_credentials").Scan(&stored).Error; err != nil || stored != secret {
		t.Fatal("debug log protection changed the stored credential", err)
	}
	driverError := errors.New("driver rejected value " + secret)
	safeLogger.Trace(context.Background(), time.Now(), func() (string, int64) { return "INSERT INTO log_credentials (value) VALUES (?)", 0 }, driverError)
	safeLogger.Error(context.Background(), "initialization failed: %v", driverError)
	if strings.Contains(output.String(), secret) {
		t.Fatal("database debug log leaked a credential")
	}
	if !strings.Contains(output.String(), "INSERT INTO log_credentials") || !strings.Contains(output.String(), "[redacted:") {
		t.Fatal("database diagnostics lost statement context or error fingerprint")
	}
}
