package migrate

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestMigrationRecordsControlSkipAndRetry(t *testing.T) {
	db := migrationTestDB(t)
	if err := ensureMigrationRecordTable(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&MigrationRecord{Version: 102, Status: MigrationRecordStatusSuccess}).Error; err != nil {
		t.Fatal(err)
	}
	attempts := 0
	sentinel := errors.New("synthetic migration failure")
	migrations := []Migration{
		{Version: 102, Up: func(*gorm.DB) error { t.Fatal("successful migration was rerun"); return nil }},
		{Version: 101, Up: func(*gorm.DB) error {
			attempts++
			if attempts == 1 {
				return sentinel
			}
			return nil
		}},
	}
	if err := runMigrationsWithRecord(db, migrations); !errors.Is(err, sentinel) {
		t.Fatalf("migration failure lost: %v", err)
	}
	var failed MigrationRecord
	if err := db.First(&failed, "version = ?", 101).Error; err != nil || failed.Status != MigrationRecordStatusFailed {
		t.Fatalf("failed attempt not recorded: %+v, err=%v", failed, err)
	}
	for range 2 {
		if err := runMigrationsWithRecord(db, migrations); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 2 {
		t.Fatalf("got %d attempts, want one failure and one retry", attempts)
	}
	var records []MigrationRecord
	if err := db.Order("version").Find(&records).Error; err != nil || len(records) != 2 {
		t.Fatalf("records changed unexpectedly: %+v, err=%v", records, err)
	}
	for _, record := range records {
		if record.Status != MigrationRecordStatusSuccess {
			t.Fatalf("successful retry not recorded: %+v", record)
		}
	}
	if migrations[0].Version != 102 || migrations[1].Version != 101 {
		t.Fatal("runner mutated the shared registration order")
	}
}

func TestSchemaCleanupPropagatesMetadataFailure(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(*gorm.DB) error
	}{
		{"drop_column", func(db *gorm.DB) error { return dropColumnIfExists(db, &channelsTable{}, "channels", "type") }},
		{"replace_group_items", func(db *gorm.DB) error { return migrateGroupItemsToGrants(db, nil) }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			db := migrationTestDB(t)
			migrationExec(t, db, `CREATE TABLE channels (id integer PRIMARY KEY, type text)`)
			migrationExec(t, db, `CREATE TABLE group_items (id integer PRIMARY KEY, group_id integer, channel_model_id integer, priority integer)`)
			migrationExec(t, db, `INSERT INTO group_items VALUES (47, 41, 23, 6)`)
			sentinel := errors.New("synthetic metadata failure")
			pool := metadataFailurePool{ConnPool: db.ConnPool, failure: sentinel}
			db.ConnPool = pool
			db.Statement.ConnPool = pool
			if err := operation.run(db); !errors.Is(err, sentinel) {
				t.Fatalf("cleanup treated metadata failure as absent schema: %v", err)
			}
			var count int64
			if err := db.Table("group_items").Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("group items changed on metadata failure: count=%d, err=%v", count, err)
			}
		})
	}
}
