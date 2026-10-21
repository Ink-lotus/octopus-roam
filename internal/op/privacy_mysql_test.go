package op

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Exercise the real import path with both SQL dialects without connecting to
// an external server. KEY is reserved in MySQL; SQLite accepts the broken SQL.
func TestPrivacyImportQuotesSettingColumns(t *testing.T) {
	pool, err := db.GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	for _, dialect := range []gorm.Dialector{
		mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}),
		postgres.New(postgres.Config{Conn: pool}),
	} {
		t.Run(dialect.Name(), func(t *testing.T) {
			conn, err := gorm.Open(dialect, &gorm.Config{
				DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true,
				Logger: logger.Discard,
			})
			if err != nil {
				t.Fatal(err)
			}
			var querySQL, updateSQL string
			var updateVars []any
			var rowsAffected int64 = 1
			if err := conn.Callback().Query().After("gorm:query").Register("test:privacy_query", func(tx *gorm.DB) {
				querySQL = tx.Statement.SQL.String()
			}); err != nil {
				t.Fatal(err)
			}
			if err := conn.Callback().Update().After("gorm:update").Register("test:privacy_update", func(tx *gorm.DB) {
				updateSQL = tx.Statement.SQL.String()
				updateVars = append([]any(nil), tx.Statement.Vars...)
				tx.RowsAffected = rowsAffected
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := preparePrivacyImport(conn, nil); err != nil {
				t.Fatal(err)
			}
			quotedKey := conn.Statement.Quote("settings.key")
			quotedValue := conn.Statement.Quote("settings.value")
			if !strings.Contains(querySQL, "WHERE "+quotedKey+" IN ") {
				t.Fatalf("setting lookup must quote its identifier: %s", querySQL)
			}
			for _, previous := range []string{"before", ""} {
				update := &privacyImport{
					previous: map[model.SettingKey]string{model.SettingKeyPrivacyProtectionEnabled: previous},
					settings: []model.Setting{{Key: model.SettingKeyPrivacyProtectionEnabled, Value: "true"}},
				}
				rowsAffected = 1
				if n, err := update.write(conn); err != nil || n != 1 {
					t.Fatalf("write: count=%d, error=%v", n, err)
				}
				if !strings.Contains(updateSQL, quotedKey+" = ") || !strings.Contains(updateSQL, quotedValue+" = ") {
					t.Fatalf("compare-and-swap must quote both identifiers: %s", updateSQL)
				}
				if !reflect.DeepEqual(updateVars, []any{"true", model.SettingKeyPrivacyProtectionEnabled, previous}) {
					t.Fatalf("compare-and-swap lost a bound value: %v", updateVars)
				}
				rowsAffected = 0
				if _, err := update.write(conn); !errors.Is(err, ErrPrivacyConflict) {
					t.Fatalf("concurrent change must reject import: %v", err)
				}
			}
		})
	}
}

func TestChannelRefreshSnapshotAvoidsReservedKeyAlias(t *testing.T) {
	pool, err := db.GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{
		DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	var querySQL string
	var queryVars []any
	if err := conn.Callback().Row().After("gorm:row").Register("test:refresh_snapshot", func(tx *gorm.DB) {
		querySQL = tx.Statement.SQL.String()
		queryVars = append([]any(nil), tx.Statement.Vars...)
	}); err != nil {
		t.Fatal(err)
	}
	err = verifyChannelRefreshGrantSnapshot(conn, 910001, nil)
	if !errors.Is(err, gorm.ErrDryRunModeUnsupported) {
		t.Fatalf("expected SQL generation without executing it: %v", err)
	}
	if querySQL == "" || strings.Contains(querySQL, " AS keys ") {
		t.Fatalf("refresh query uses an unquoted MySQL reserved alias: %s", querySQL)
	}
	if !reflect.DeepEqual(queryVars, []any{910001}) {
		t.Fatalf("refresh query lost its channel filter: %v", queryVars)
	}
}
