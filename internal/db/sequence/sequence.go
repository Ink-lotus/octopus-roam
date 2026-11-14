// Package sequence repairs PostgreSQL entity sequences without changing row IDs.
package sequence

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// Parent tables precede their children, matching normal channel/group writes.
// Business-assigned integer keys (hours, statistics and migration versions) are
// deliberately excluded, even when an old physical schema has a serial column.
func entityModels() []any {
	return []any{
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.ChannelGrant{}, &model.GroupCategory{}, &model.Group{},
		&model.GroupItem{}, &model.APIKey{}, &model.WebhookEndpoint{},
	}
}

type entity struct {
	TableOID       int64 `gorm:"column:table_oid"`
	TableSchema    string
	TableName      string
	ColumnName     string
	SequenceOID    sql.NullInt64 `gorm:"column:sequence_oid"`
	SequenceSchema sql.NullString
	SequenceName   sql.NullString
	SequenceOwner  string
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

func (e entity) table() string { return quote(e.TableSchema) + "." + quote(e.TableName) }
func (e entity) sequence() string {
	return quote(e.SequenceSchema.String) + "." + quote(e.SequenceName.String)
}

// LockForImport must run before any imported rows are written. Ordinary INSERT,
// UPDATE and DELETE statements then wait until the import commits or rolls back.
// The caller must retain the same READ COMMITTED transaction through Sync.
func LockForImport(tx *gorm.DB) error {
	if tx == nil || tx.Dialector == nil {
		return fmt.Errorf("sequence repair requires a database")
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	_, err := lockEntities(tx, nil)
	return err
}

// Sync advances the sequences of all backup entities, or the supplied entity
// models. It requires a READ COMMITTED transaction and holds locks until its end.
// RESTART also invalidates cached ranges in other PostgreSQL connections; setval
// alone would leave those connections able to allocate colliding imported IDs.
func Sync(tx *gorm.DB, models ...any) error {
	if tx == nil || tx.Dialector == nil {
		return fmt.Errorf("sequence repair requires a database")
	}
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	entities, err := lockEntities(tx, models)
	if err != nil {
		return err
	}
	for _, e := range entities {
		// Assigning the existing owner takes an AccessExclusiveLock without
		// changing sequence parameters or its counter. The owning table lock
		// keeps that owner stable. Read last_value only after direct nextval
		// and concurrent sequence configuration changes have finished.
		if err := tx.Exec("ALTER SEQUENCE " + e.sequence() + " OWNER TO " + quote(e.SequenceOwner)).Error; err != nil {
			return fmt.Errorf("lock %s.%s sequence (sequence ownership is required): %w", e.TableName, e.ColumnName, err)
		}
		var state sequenceState
		if err := tx.Raw("SELECT last_value, is_called FROM " + e.sequence()).Scan(&state).Error; err != nil {
			return fmt.Errorf("read %s sequence: %w", e.TableName, err)
		}
		var parameters sequenceParameters
		if err := tx.Raw("SELECT seqincrement AS increment, seqmin AS minimum, seqmax AS maximum, seqcycle AS cycle FROM pg_catalog.pg_sequence WHERE seqrelid = ?", e.SequenceOID.Int64).Scan(&parameters).Error; err != nil {
			return fmt.Errorf("read %s sequence bounds: %w", e.TableName, err)
		}
		var highest sql.NullInt64
		if err := tx.Raw("SELECT MAX(" + quote(e.ColumnName) + ") FROM " + e.table()).Scan(&highest).Error; err != nil {
			return fmt.Errorf("read %s highest ID: %w", e.TableName, err)
		}
		next, err := nextValue(state, parameters, highest)
		if err != nil {
			return fmt.Errorf("repair %s.%s sequence: %w", e.TableName, e.ColumnName, err)
		}
		// next is a checked int64, not SQL supplied by a backup or a caller.
		if err := tx.Exec(fmt.Sprintf("ALTER SEQUENCE %s RESTART WITH %d", e.sequence(), next)).Error; err != nil {
			return fmt.Errorf("restart %s sequence: %w", e.TableName, err)
		}
	}
	return nil
}

func lockEntities(tx *gorm.DB, requested []any) ([]entity, error) {
	if _, ok := tx.Statement.ConnPool.(gorm.TxCommitter); !ok {
		return nil, fmt.Errorf("sequence repair requires a transaction")
	}
	var isolation string
	if err := tx.Raw("SHOW transaction_isolation").Scan(&isolation).Error; err != nil {
		return nil, err
	}
	if isolation != "read committed" {
		return nil, fmt.Errorf("sequence repair requires READ COMMITTED isolation")
	}
	models := entityModels()
	if len(requested) != 0 {
		allowed := make(map[string]bool, len(models))
		for _, m := range models {
			statement := &gorm.Statement{DB: tx}
			if err := statement.Parse(m); err != nil {
				return nil, err
			}
			allowed[statement.Schema.Table] = true
		}
		models = requested
		for _, m := range models {
			statement := &gorm.Statement{DB: tx}
			if err := statement.Parse(m); err != nil {
				return nil, err
			}
			if !allowed[statement.Schema.Table] {
				return nil, fmt.Errorf("%s is not a backup entity sequence", statement.Schema.Table)
			}
		}
	}
	entities := make([]entity, 0, len(models))
	names := make([]string, 0, len(models))
	for _, m := range models {
		statement := &gorm.Statement{DB: tx}
		if err := statement.Parse(m); err != nil {
			return nil, err
		}
		key := statement.Schema.PrioritizedPrimaryField
		if key == nil || !key.PrimaryKey || !key.AutoIncrement || (key.DataType != schema.Int && key.DataType != schema.Uint) || len(statement.Schema.PrimaryFields) != 1 {
			return nil, fmt.Errorf("%s does not have one auto-increment integer primary key", statement.Schema.Table)
		}
		var e entity
		result := tx.Raw(`SELECT c.oid AS table_oid, n.nspname AS table_schema, c.relname AS table_name,
			q.oid AS sequence_oid, qn.nspname AS sequence_schema, q.relname AS sequence_name
			FROM pg_catalog.pg_class c
			JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
			LEFT JOIN pg_catalog.pg_class q ON q.oid = pg_catalog.pg_get_serial_sequence(
				pg_catalog.quote_ident(n.nspname) || '.' || pg_catalog.quote_ident(c.relname), ?)::regclass
			LEFT JOIN pg_catalog.pg_namespace qn ON qn.oid = q.relnamespace
			WHERE c.oid = pg_catalog.to_regclass(?) AND c.relkind IN ('r', 'p')`, key.DBName, statement.Schema.Table).Scan(&e)
		if result.Error != nil {
			return nil, fmt.Errorf("inspect %s entity sequence: %w", statement.Schema.Table, result.Error)
		}
		if result.RowsAffected != 1 || !e.SequenceOID.Valid || !e.SequenceSchema.Valid || !e.SequenceName.Valid {
			return nil, fmt.Errorf("%s.%s is missing its associated sequence", statement.Schema.Table, key.DBName)
		}
		e.ColumnName = key.DBName
		entities = append(entities, e)
		names = append(names, e.table())
	}
	if err := tx.Exec("LOCK TABLE " + strings.Join(names, ", ") + " IN SHARE ROW EXCLUSIVE MODE").Error; err != nil {
		return nil, fmt.Errorf("lock backup entity tables: %w", err)
	}
	// A DROP/recreate while waiting must not make us repair a different table.
	for i := range entities {
		e := &entities[i]
		var current struct {
			TableOID      int64 `gorm:"column:table_oid"`
			SequenceOID   int64 `gorm:"column:sequence_oid"`
			SequenceOwner string
		}
		if err := tx.Raw(`SELECT ?::regclass::oid AS table_oid, s.oid AS sequence_oid,
			pg_catalog.pg_get_userbyid(s.relowner) AS sequence_owner
			FROM pg_catalog.pg_class s
			WHERE s.oid = pg_catalog.pg_get_serial_sequence(?, ?)::regclass`, e.table(), e.table(), e.ColumnName).Scan(&current).Error; err != nil {
			return nil, err
		}
		if current.TableOID != e.TableOID || current.SequenceOID != e.SequenceOID.Int64 || current.SequenceOwner == "" {
			return nil, fmt.Errorf("%s sequence association changed; retry the operation", e.TableName)
		}
		e.SequenceOwner = current.SequenceOwner
	}
	return entities, nil
}

type sequenceState struct {
	LastValue int64
	IsCalled  bool
}

type sequenceParameters struct {
	Increment int64
	Minimum   int64
	Maximum   int64
	Cycle     bool
}

func nextValue(state sequenceState, p sequenceParameters, highest sql.NullInt64) (int64, error) {
	if p.Increment <= 0 || p.Cycle {
		return 0, fmt.Errorf("an ascending, non-cycling sequence is required")
	}
	// Entity IDs are positive; rejecting nonpositive sequence bounds also keeps
	// the overflow checks below independent of signed subtraction edge cases.
	if p.Minimum <= 0 || p.Maximum < p.Minimum || state.LastValue < p.Minimum || state.LastValue > p.Maximum {
		return 0, fmt.Errorf("invalid positive entity sequence bounds")
	}
	next := state.LastValue
	if state.IsCalled {
		if p.Increment > p.Maximum-next {
			return 0, fmt.Errorf("sequence exhausted; no unused ID remains")
		}
		next += p.Increment
	}
	if highest.Valid && highest.Int64 >= next {
		if highest.Int64 >= p.Maximum {
			return 0, fmt.Errorf("highest entity ID exhausts the sequence")
		}
		steps := (highest.Int64-next)/p.Increment + 1
		if steps > (p.Maximum-next)/p.Increment {
			return 0, fmt.Errorf("sequence exhausted; no unused ID remains")
		}
		next += steps * p.Increment
	}
	return next, nil
}
