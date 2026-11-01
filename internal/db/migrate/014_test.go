package migrate

import (
	"testing"
)

func TestMigrateCooldownRulesBackfillsOnlyNullRows(t *testing.T) {
	db := migrationTestDB(t)
	migrationExec(t, db, `CREATE TABLE channels (id integer PRIMARY KEY, cooldown_rules text)`)
	migrationExec(t, db, `INSERT INTO channels (id, cooldown_rules) VALUES (1, NULL), (2, 'not-json'), (3, '[]')`)
	if err := migrateCooldownRules(db); err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID            int
		CooldownRules *string
	}
	if err := db.Table("channels").Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].CooldownRules == nil || *rows[0].CooldownRules != "[]" || rows[1].CooldownRules == nil || *rows[1].CooldownRules != "not-json" || rows[2].CooldownRules == nil || *rows[2].CooldownRules != "[]" {
		t.Fatalf("migration changed unexpected rows: %+v", rows)
	}
	if err := migrateCooldownRules(db); err != nil {
		t.Fatal(err)
	}
}
