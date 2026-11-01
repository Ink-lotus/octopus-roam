package op

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
