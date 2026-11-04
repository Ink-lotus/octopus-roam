package migrate

import (
	"strings"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

func TestGroupCategoryMigrationPreservesRowsAndEnforcesReferences(t *testing.T) {
	db := migrationTestDB(t)
	migrationExec(t, db, `CREATE TABLE groups (id integer PRIMARY KEY, name text)`)
	migrationExec(t, db, `CREATE TABLE group_items (id integer PRIMARY KEY, group_id integer REFERENCES groups(id) ON DELETE CASCADE, priority integer)`)
	migrationExec(t, db, `INSERT INTO groups VALUES (501, 'fixture')`)
	migrationExec(t, db, `INSERT INTO group_items VALUES (601, 501, 7)`)
	if err := migrateGroupCategories(db); err != nil {
		t.Fatal(err)
	}
	var unassigned, items int64
	if err := db.Table("groups").Where("category_id IS NULL").Count(&unassigned).Error; err != nil || unassigned != 1 {
		t.Fatalf("old group was not uncategorized: %d, %v", unassigned, err)
	}
	category := model.GroupCategory{ID: 71, Name: "Fixture", NameKey: "fixture"}
	if err := db.Create(&category).Error; err != nil {
		t.Fatal(err)
	}
	migrationExec(t, db, `UPDATE groups SET category_id = 71 WHERE id = 501`)
	if err := migrateGroupCategories(db); err != nil {
		t.Fatal(err)
	}
	var row struct{ CategoryID *int }
	if err := db.Table("groups").First(&row).Error; err != nil || row.CategoryID == nil || *row.CategoryID != 71 {
		t.Fatalf("retry lost assigned category: %+v, %v", row, err)
	}
	if err := db.Exec(`UPDATE groups SET category_id = 999 WHERE id = 501`).Error; err == nil {
		t.Fatal("foreign key accepted a dangling category")
	}
	if err := db.Delete(&category).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("groups").Where("category_id IS NULL").Count(&unassigned).Error; err != nil || unassigned != 1 {
		t.Fatalf("foreign key did not clear deleted category: %d, %v", unassigned, err)
	}
	if err := db.Table("group_items").Where("id = 601 AND group_id = 501 AND priority = 7").Count(&items).Error; err != nil || items != 1 {
		t.Fatalf("migration or category deletion removed member: %d, %v", items, err)
	}
	var enabled int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&enabled).Error; err != nil || enabled != 1 {
		t.Fatalf("foreign key checks were disabled: %d, %v", enabled, err)
	}
}

func TestGroupCategoryMigrationRollbackAndRetry(t *testing.T) {
	db := migrationTestDB(t)
	migrationExec(t, db, `CREATE TABLE groups (id integer PRIMARY KEY, name text)`)
	migrationExec(t, db, `INSERT INTO groups VALUES (501, 'fixture')`)
	// 与待建索引同名的合成表使最后一步失败，验证前面的建表和加列一并回滚。
	migrationExec(t, db, `CREATE TABLE idx_groups_category_id (id integer PRIMARY KEY)`)
	migrations := []Migration{{Version: 15, Up: migrateGroupCategories}}
	if err := runMigrationsWithRecord(db, migrations); err == nil {
		t.Fatal("injected schema conflict did not fail")
	}
	if db.Migrator().HasColumn("groups", "category_id") || db.Migrator().HasTable("group_categories") {
		t.Fatal("failed migration left partial category schema")
	}
	var record MigrationRecord
	if err := db.First(&record, 15).Error; err != nil || record.Status != MigrationRecordStatusFailed {
		t.Fatalf("failed migration status lost: %+v, %v", record, err)
	}
	migrationExec(t, db, `DROP TABLE idx_groups_category_id`)
	for range 2 {
		if err := runMigrationsWithRecord(db, migrations); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.First(&record, 15).Error; err != nil || record.Status != MigrationRecordStatusSuccess {
		t.Fatalf("successful retry status lost: %+v, %v", record, err)
	}
	var count int64
	if err := db.Table("groups").Where("id = 501 AND category_id IS NULL").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retry changed old group: %d, %v", count, err)
	}
}

func TestGroupCategoryMigrationRejectsInvalidPartialSchema(t *testing.T) {
	db := migrationTestDB(t)
	migrationExec(t, db, `CREATE TABLE groups (id integer PRIMARY KEY, category_id integer)`)
	migrationExec(t, db, `INSERT INTO groups VALUES (501, 999)`)
	if err := migrateGroupCategories(db); err == nil || !strings.Contains(err.Error(), "invalid category references") {
		t.Fatalf("invalid existing reference silently changed: %v", err)
	}
	var id int
	if err := db.Table("groups").Select("category_id").Scan(&id).Error; err != nil || id != 999 {
		t.Fatalf("failed migration changed data: %d, %v", id, err)
	}
	if err := migrateGroupCategories(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
