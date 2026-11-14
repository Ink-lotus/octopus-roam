package main

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/db/sequence"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"gorm.io/gorm"
)

type sequenceInfo struct {
	LastValue int64 `json:"last_value"`
	IsCalled  bool  `json:"is_called"`
	Next      int64 `json:"next"`
	Cache     int64 `json:"cache"`
}

func inspectSequences(conn *gorm.DB) (map[string]sequenceInfo, error) {
	result := make(map[string]sequenceInfo)
	for _, table := range entityTables {
		var name struct {
			Schema, Name string
			Cache        int64
		}
		if err := conn.Raw(`SELECT n.nspname AS schema, c.relname AS name, s.seqcache AS cache
			FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			JOIN pg_catalog.pg_sequence s ON s.seqrelid=c.oid
			WHERE c.oid=pg_catalog.pg_get_serial_sequence(?, 'id')::regclass`, table).Scan(&name).Error; err != nil {
			return nil, err
		}
		if name.Name == "" {
			return nil, errors.New("entity sequence is missing")
		}
		var state sequenceInfo
		if err := conn.Raw("SELECT last_value, is_called FROM " + quote(name.Schema) + "." + quote(name.Name)).Scan(&state).Error; err != nil {
			return nil, err
		}
		state.Cache, state.Next = name.Cache, state.LastValue
		if state.IsCalled {
			state.Next++
		}
		result[table] = state
	}
	return result, nil
}

func (s *suite) schema(name string) (string, error) {
	admin, err := rawDB("postgres", s.dsn)
	if err != nil {
		return "", err
	}
	pool, err := admin.DB()
	if err != nil {
		return "", err
	}
	defer pool.Close()
	if err := admin.WithContext(s.ctx).Exec("CREATE SCHEMA " + quote(name)).Error; err != nil {
		return "", err
	}
	return postgresDSN(s.dsn, name, "")
}

func (s *suite) verifyPostgres(dump *model.DBDump) error {
	for _, sample := range []struct {
		name  string
		input *model.DBDump
	}{
		{"b01_dense", dump}, {"b01_sparse", sparseDump(dump, 100000)},
	} {
		dsn, err := s.schema(sample.name)
		if err != nil {
			return err
		}
		s.scope = sample.name
		if err := s.verifyCommon(dsn, sample.input); err != nil {
			return err
		}
	}
	for _, sample := range []struct {
		name   string
		verify func(string, *model.DBDump) error
	}{
		{"b01_empty", s.verifyEmpty}, {"b01_ahead", s.verifyAhead},
		{"b01_cached", s.verifyCachedRanges}, {`b01 quoted "schema`, s.verifyRenamedSequence},
		{"b01_permissions", s.verifyPermissions}, {"b01_concurrency", s.verifyConcurrentImport},
		{"b01_failure", s.verifySequenceFailure}, {"b01_identity", s.verifyIdentitySequence},
		{"b01_parameters", s.verifySequenceParameters},
	} {
		dsn, err := s.schema(sample.name)
		if err != nil {
			return err
		}
		s.scope = sample.name
		if err := sample.verify(dsn, dump); err != nil {
			return err
		}
	}
	s.scope = ""
	return nil
}

func (s *suite) verifyEmpty(dsn string, _ *model.DBDump) error {
	defer db.Close()
	if err := s.step("empty-unused-sequences", func() (any, error) {
		if err := s.boot(dsn); err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, &model.DBDump{Product: "octopus-roam", Version: 1003, GroupCategories: []model.BackupGroupCategory{}}); err != nil {
			return nil, err
		}
		states, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		for _, state := range states {
			if state.Next != 1 || state.IsCalled {
				return nil, errors.New("empty unused sequence advanced or lost is_called=false")
			}
		}
		return states, nil
	}); err != nil {
		return err
	}
	return s.step("empty-database-committed-new-entities", s.createEntities)
}

func (s *suite) verifyAhead(dsn string, dump *model.DBDump) error {
	defer db.Close()
	if err := s.step("ahead-unused-sequences", func() (any, error) {
		if err := s.boot(dsn); err != nil {
			return nil, err
		}
		for _, table := range entityTables {
			var name string
			if err := db.GetDB().Raw("SELECT pg_get_serial_sequence(?, 'id')", table).Scan(&name).Error; err != nil {
				return nil, err
			}
			if err := db.GetDB().Exec("ALTER SEQUENCE " + name + " RESTART WITH 512").Error; err != nil {
				return nil, err
			}
		}
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
			return nil, err
		}
		if err := op.InitCache(); err != nil {
			return nil, err
		}
		states, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		for _, state := range states {
			if state.Next != 512 || state.IsCalled {
				return nil, errors.New("ahead unused sequence was changed")
			}
		}
		return states, s.verifyGraph(dump)
	}); err != nil {
		return err
	}
	if err := s.step("ahead-committed-new-entities", s.createEntities); err != nil {
		return err
	}
	return s.step("deleted-high-id-never-reused", func() (any, error) {
		var key model.APIKey
		if err := db.GetDB().Where("name = ?", "b01-created-api-key").First(&key).Error; err != nil {
			return nil, err
		}
		if err := db.GetDB().Delete(&key).Error; err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
			return nil, err
		}
		states, err := inspectSequences(db.GetDB())
		if err != nil || states["api_keys"].Next <= int64(key.ID) {
			return nil, errors.New("deleted high ID was reused")
		}
		return states["api_keys"], nil
	})
}

func (s *suite) verifyCachedRanges(dsn string, dump *model.DBDump) error {
	defer db.Close()
	if err := s.boot(dsn); err != nil {
		return err
	}
	pool, err := db.GetDB().DB()
	if err != nil {
		return err
	}
	first, err := pool.Conn(s.ctx)
	if err != nil {
		return err
	}
	defer first.Close()
	second, err := pool.Conn(s.ctx)
	if err != nil {
		return err
	}
	defer second.Close()
	if err := s.step("two-pinned-connection-cache-ranges", func() (any, error) {
		for _, table := range entityTables {
			var name string
			if err := db.GetDB().Raw("SELECT pg_get_serial_sequence(?, 'id')", table).Scan(&name).Error; err != nil {
				return nil, err
			}
			if err := db.GetDB().Exec("ALTER SEQUENCE " + name + " CACHE 20").Error; err != nil {
				return nil, err
			}
			for i, conn := range []*sql.Conn{first, second} {
				var number int64
				if err := conn.QueryRowContext(s.ctx, "SELECT nextval(pg_get_serial_sequence($1, 'id'))", table).Scan(&number); err != nil || number != int64(1+i*20) {
					return nil, errors.New("could not reserve independent cached ranges")
				}
			}
		}
		before, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
			return nil, err
		}
		for _, table := range entityTables {
			seen := make(map[int64]bool)
			for _, conn := range []*sql.Conn{first, second} {
				var number int64
				if err := conn.QueryRowContext(s.ctx, "SELECT nextval(pg_get_serial_sequence($1, 'id'))", table).Scan(&number); err != nil || number <= before[table].LastValue || seen[number] {
					return nil, fmt.Errorf("%s retained a stale cached range", table)
				}
				seen[number] = true
			}
		}
		return map[string]any{"reserved_before_import": before, "pinned_connections_discarded_old_ranges": true}, nil
	}); err != nil {
		return err
	}
	if err := op.InitCache(); err != nil {
		return err
	}
	if err := s.step("cached-first-save", func() (any, error) { return nil, s.saveAutoAdd(dump) }); err != nil {
		return err
	}
	return s.step("cached-committed-new-entities", s.createEntities)
}

func (s *suite) verifyRenamedSequence(dsn string, dump *model.DBDump) error {
	defer db.Close()
	if err := s.boot(dsn); err != nil {
		return err
	}
	var name string
	if err := db.GetDB().Raw("SELECT pg_get_serial_sequence('channels', 'id')").Scan(&name).Error; err != nil {
		return err
	}
	if err := db.GetDB().Exec("ALTER SEQUENCE " + name + " RENAME TO " + quote(`custom "channel ids`)).Error; err != nil {
		return err
	}
	if err := s.step("quoted-schema-renamed-associated-sequence", func() (any, error) {
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
			return nil, err
		}
		if err := op.InitCache(); err != nil {
			return nil, err
		}
		states, err := inspectSequences(db.GetDB())
		return states, err
	}); err != nil {
		return err
	}
	if err := s.step("renamed-first-save", func() (any, error) { return nil, s.saveAutoAdd(dump) }); err != nil {
		return err
	}
	return s.step("renamed-committed-new-entities", s.createEntities)
}

func (s *suite) verifySequenceFailure(dsn string, dump *model.DBDump) error {
	defer db.Close()
	if err := s.boot(dsn); err != nil {
		return err
	}
	if _, err := op.DBImportIncremental(s.ctx, cloneDump(dump)); err != nil {
		return err
	}
	if err := op.InitCache(); err != nil {
		return err
	}
	if err := s.step("exhaustion-full-business-rollback", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		cached, err := op.ChannelDetailGet(dump.Channels[0].ID)
		if err != nil {
			return nil, err
		}
		bad := sparseDump(dump, 100000)
		bad.Channels[0].ID = math.MaxInt64
		bad.Channels[0].Name = "b01-exhausted-channel"
		bad.Groups[0].Name = "b01-exhausted-group"
		for i := range bad.ChannelKeys {
			bad.ChannelKeys[i].ChannelID = math.MaxInt64
		}
		for i := range bad.ChannelModels {
			bad.ChannelModels[i].ChannelID = math.MaxInt64
		}
		published := false
		_, importErr := op.DBImportIncrementalAndPublish(s.ctx, bad, func() { published = true })
		// B03 deliberately hides the database cause from the public message.
		// Inspect the retained cause so an unrelated rejection cannot pass this
		// real sequence-exhaustion/transaction-rollback regression.
		cause := errors.Unwrap(importErr)
		details := map[string]any{
			"write_error":         errors.Is(importErr, op.ErrBackupWrite),
			"sequence_exhaustion": cause != nil && strings.Contains(cause.Error(), "exhaust"),
			"success_publication": published,
		}
		if !errors.Is(importErr, op.ErrBackupWrite) || cause == nil || !strings.Contains(cause.Error(), "exhaust") || published {
			return details, &assertionFailure{"exhausted entity ID was not rejected at sequence repair"}
		}
		after, err := snapshot()
		if err != nil || before != after {
			return details, &assertionFailure{"sequence exhaustion left partial business rows"}
		}
		afterCached, err := op.ChannelDetailGet(dump.Channels[0].ID)
		if err != nil || !reflect.DeepEqual(cached, afterCached) {
			return details, &assertionFailure{"sequence exhaustion changed the channel cache"}
		}
		details["before"], details["after"], details["cache_unchanged"] = before, after, true
		return details, nil
	}); err != nil {
		return err
	}
	if err := s.step("restart-transaction-rollback", func() (any, error) {
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		statesBefore, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		marker := errors.New("synthetic late rollback")
		err = db.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := sequence.LockForImport(tx); err != nil {
				return err
			}
			if err := tx.Create(&model.APIKey{Name: "b01-rolled-back-key", APIKey: "synthetic-value", Enabled: true}).Error; err != nil {
				return err
			}
			if err := sequence.Sync(tx); err != nil {
				return err
			}
			return marker
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if !errors.Is(err, marker) {
			return nil, err
		}
		after, err := snapshot()
		if err != nil || before != after {
			return nil, errors.New("transaction rollback changed business rows")
		}
		statesAfter, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		return map[string]any{"before": statesBefore, "after": statesAfter, "business_content_unchanged": true}, nil
	}); err != nil {
		return err
	}
	if err := s.step("reject-unsafe-isolation-and-business-key", func() (any, error) {
		err := db.GetDB().Transaction(func(tx *gorm.DB) error { return sequence.Sync(tx) }, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err == nil || !strings.Contains(err.Error(), "READ COMMITTED") {
			return nil, errors.New("unsafe sequence snapshot isolation accepted")
		}
		err = db.GetDB().Transaction(func(tx *gorm.DB) error { return sequence.Sync(tx, &model.StatsHourly{}) }, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err == nil || !strings.Contains(err.Error(), "not a backup entity") {
			return nil, errors.New("business integer key accepted as an entity sequence")
		}
		return nil, nil
	}); err != nil {
		return err
	}
	return s.step("post-failure-committed-new-entities", s.createEntities)
}

func (s *suite) verifyIdentitySequence(dsn string, _ *model.DBDump) error {
	conn, err := rawDB("postgres", dsn)
	if err != nil {
		return err
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	return s.step("identity-associated-sequence", func() (any, error) {
		if err := conn.Exec(`CREATE TABLE api_keys (id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY, name text NOT NULL, api_key text NOT NULL, enabled boolean DEFAULT true, expire_at bigint, max_cost double precision, supported_models text)`).Error; err != nil {
			return nil, err
		}
		if err := conn.Create(&model.APIKey{ID: 9, Name: "b01-identity-restored", APIKey: "synthetic-value", Enabled: true}).Error; err != nil {
			return nil, err
		}
		if err := conn.Transaction(func(tx *gorm.DB) error { return sequence.Sync(tx, &model.APIKey{}) }, &sql.TxOptions{Isolation: sql.LevelReadCommitted}); err != nil {
			return nil, err
		}
		row := model.APIKey{Name: "b01-identity-new", APIKey: "synthetic-value", Enabled: true}
		if err := conn.Create(&row).Error; err != nil || row.ID != 10 {
			return nil, errors.New("identity sequence did not repair")
		}
		return map[string]int{"restored_id": 9, "allocated_id": row.ID}, nil
	})
}

func (s *suite) verifySequenceParameters(dsn string, _ *model.DBDump) error {
	defer db.Close()
	if err := s.boot(dsn); err != nil {
		return err
	}
	return s.step("custom-sequence-parameters-retained", func() (any, error) {
		conn := db.GetDB()
		var name string
		if err := conn.Raw("SELECT pg_get_serial_sequence('api_keys', 'id')").Scan(&name).Error; err != nil {
			return nil, err
		}
		if err := conn.Exec("ALTER SEQUENCE " + name + " START WITH 17 INCREMENT BY 3 MINVALUE 1 MAXVALUE 1000 CACHE 20 RESTART WITH 7").Error; err != nil {
			return nil, err
		}
		type config struct {
			Start, Increment, MinValue, MaxValue, Cache int64
			Cycle                                       bool
			DataType                                    string
		}
		read := func(value *config) error {
			return conn.Raw(`SELECT seqstart AS start, seqincrement AS increment, seqmin AS min_value,
				seqmax AS max_value, seqcache AS cache, seqcycle AS cycle, seqtypid::regtype::text AS data_type
				FROM pg_sequence WHERE seqrelid=pg_get_serial_sequence('api_keys', 'id')::regclass`).Scan(value).Error
		}
		var before, after config
		if err := read(&before); err != nil {
			return nil, err
		}
		if err := conn.Create(&model.APIKey{ID: 9, Name: "b01-custom-restored", APIKey: "synthetic-value", Enabled: true}).Error; err != nil {
			return nil, err
		}
		if err := conn.Transaction(func(tx *gorm.DB) error { return sequence.Sync(tx) }, &sql.TxOptions{Isolation: sql.LevelReadCommitted}); err != nil {
			return nil, err
		}
		if err := read(&after); err != nil || before != after {
			return nil, errors.New("sequence repair changed sequence parameters")
		}
		ids := []int{}
		// A single transaction pins the connection, so CACHE 20 cannot turn
		// ordinary pool scheduling into an unrelated ID-gap assertion failure.
		if err := conn.Transaction(func(tx *gorm.DB) error {
			for i, want := range []int{10, 13} {
				row := model.APIKey{Name: fmt.Sprintf("b01-custom-new-%d", i), APIKey: "synthetic-value", Enabled: true}
				if err := tx.Create(&row).Error; err != nil || row.ID != want {
					return errors.New("custom sequence increment was not preserved")
				}
				ids = append(ids, row.ID)
			}
			return nil
		}); err != nil {
			return nil, err
		}
		return map[string]any{"before": before, "after": after, "allocated_ids": ids}, nil
	})
}

func (s *suite) verifyLegacyUpgrade(dump *model.DBDump) error {
	s.scope = "legacy-v0.1.2"
	conn, err := rawDB("postgres", s.dsn)
	if err != nil {
		return err
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	var before map[string]sequenceInfo
	var beforeContent string
	if err := s.step("old-behavior-left-lagging-sequences", func() (any, error) {
		var err error
		before, err = inspectSequences(conn)
		if err != nil {
			return nil, err
		}
		beforeContent, err = entitySnapshot(conn)
		if err != nil {
			return nil, err
		}
		highest, err := maxima(conn)
		if err != nil {
			return nil, err
		}
		bad := 0
		for table, state := range before {
			if state.Next <= highest[table] {
				bad++
			}
		}
		if bad == 0 {
			return nil, errors.New("old fixture has no lagging sequence")
		}
		var count int64
		if err := conn.Model(&migrate.MigrationRecord{}).Where("version = ?", 16).Count(&count).Error; err != nil || count != 0 {
			return nil, errors.New("legacy fixture already has migration 16")
		}
		return map[string]any{"lagging_tables": bad, "before": before}, nil
	}); err != nil {
		return err
	}
	defer db.Close()
	if err := s.step("upgrade-without-reimport", func() (any, error) {
		if err := s.boot(s.dsn); err != nil {
			return nil, err
		}
		afterContent, err := entitySnapshot(db.GetDB())
		if err != nil || beforeContent != afterContent {
			return nil, errors.New("upgrade changed entity IDs, content or foreign keys")
		}
		states, err := inspectSequences(db.GetDB())
		if err != nil {
			return nil, err
		}
		highest, err := maxima(db.GetDB())
		if err != nil {
			return nil, err
		}
		for table, state := range states {
			if state.Next <= highest[table] || state.Next < before[table].Next {
				return nil, errors.New("upgrade did not advance every sequence")
			}
		}
		for _, key := range dump.ChannelKeys {
			var got model.ChannelKey
			if err := db.GetDB().First(&got, key.ID).Error; err != nil || got.ChannelID != key.ChannelID || got.Key != key.Key {
				return nil, errors.New("upgrade changed restored key or association")
			}
		}
		return map[string]any{"sequences": states, "before_content": beforeContent, "after_content": afterContent}, nil
	}); err != nil {
		return err
	}
	if err := s.step("upgraded-first-save", func() (any, error) { return nil, s.saveAutoAdd(dump) }); err != nil {
		return err
	}
	return s.step("upgraded-committed-new-entities", s.createEntities)
}
