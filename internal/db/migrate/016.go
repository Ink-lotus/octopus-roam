package migrate

import (
	"database/sql"
	"fmt"

	"github.com/Ink-lotus/octopus-roam/internal/db/sequence"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{Version: 16, Up: migrateBackupEntitySequences})
}

// Old migrations (including 011's explicit group item IDs) and AutoMigrate have
// finished before this runs. Existing restored instances need no new import.
// A failure leaves migration 16 retryable and does not rewrite previous records.
func migrateBackupEntitySequences(db *gorm.DB) error {
	if db == nil || db.Dialector == nil {
		return fmt.Errorf("db is nil")
	}
	if db.Dialector.Name() != "postgres" {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		return sequence.Sync(tx)
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}
