package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/migrate"
	"github.com/Ink-lotus/octopus-roam/internal/db/sequence"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

func (s *suite) verifyPermissions(dsn string, dump *model.DBDump) error {
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
	var schemaName string
	if err := db.GetDB().Raw("SELECT current_schema()").Scan(&schemaName).Error; err != nil {
		return err
	}
	role := "b01_sequence_limited"
	passwordBytes := make([]byte, 32)
	if _, err := rand.Read(passwordBytes); err != nil {
		return err
	}
	password := hex.EncodeToString(passwordBytes)
	for _, command := range []string{
		"CREATE ROLE " + quote(role) + " LOGIN PASSWORD '" + password + "'",
		"GRANT USAGE ON SCHEMA " + quote(schemaName) + " TO " + quote(role),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA " + quote(schemaName) + " TO " + quote(role),
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + quote(schemaName) + " TO " + quote(role),
	} {
		if err := db.GetDB().Exec(command).Error; err != nil {
			return err
		}
	}
	limitedDSN, err := postgresDSN(s.dsn, schemaName, "")
	if err != nil {
		return err
	}
	limitedURL, err := url.Parse(limitedDSN)
	if err != nil {
		return err
	}
	limitedURL.User = url.UserPassword(role, password)
	limitedDSN = limitedURL.String()
	if err := db.Close(); err != nil {
		return err
	}
	if err := s.step("nonowner-initialization-rejected", func() (any, error) {
		err := db.InitDB("postgres", limitedDSN, false)
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "42501" {
			return nil, errors.New("nonowner initialization did not enforce DDL ownership")
		}
		return map[string]string{"sqlstate": "42501", "boundary": "initialization"}, nil
	}); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	admin, err := rawDB("postgres", dsn)
	if err != nil {
		return err
	}
	adminPool, err := admin.DB()
	if err != nil {
		return err
	}
	defer adminPool.Close()
	var adminRole string
	if err := admin.Raw("SELECT current_user").Scan(&adminRole).Error; err != nil {
		return err
	}
	if err := admin.Exec("ALTER SCHEMA " + quote(schemaName) + " OWNER TO " + quote(role)).Error; err != nil {
		return err
	}
	var tables []string
	if err := admin.Raw("SELECT tablename FROM pg_catalog.pg_tables WHERE schemaname = ? ORDER BY tablename", schemaName).Scan(&tables).Error; err != nil {
		return err
	}
	for _, table := range tables {
		if err := admin.Exec("ALTER TABLE " + quote(schemaName) + "." + quote(table) + " OWNER TO " + quote(role)).Error; err != nil {
			return err
		}
	}
	if err := s.step("initialize-import-role-as-owner", func() (any, error) { return nil, s.boot(limitedDSN) }); err != nil {
		return err
	}
	// Startup must retain its DDL checks. Revoke ownership only after it has
	// finished, keeping the application's real LOGIN role and DML privileges.
	for _, command := range []string{
		"ALTER TABLE " + quote(schemaName) + ".webhook_endpoints OWNER TO " + quote(adminRole),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON " + quote(schemaName) + ".webhook_endpoints TO " + quote(role),
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA " + quote(schemaName) + " TO " + quote(role),
	} {
		if err := admin.Exec(command).Error; err != nil {
			return err
		}
	}
	if err := s.step("sequence-ownership-import-full-rollback", func() (any, error) {
		var currentRole string
		if err := db.GetDB().Raw("SELECT current_user").Scan(&currentRole).Error; err != nil || currentRole != role {
			return nil, errors.New("import did not use the independent limited LOGIN role")
		}
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		incoming := sparseDump(dump, 50000)
		incoming.Channels[0].Name = "b01-permission-channel"
		incoming.Groups[0].Name = "b01-permission-group"
		incoming.GroupCategories = []model.BackupGroupCategory{}
		incoming.Groups[0].CategoryID = nil
		if _, err := op.DBImportIncremental(s.ctx, incoming); !errors.Is(err, op.ErrBackupWrite) || !strings.Contains(err.Error(), "ownership") {
			return nil, errors.New("sequence ownership failure was not rejected")
		}
		after, err := snapshot()
		if err != nil || before != after {
			return nil, errors.New("sequence permission failure left partial rows")
		}
		if err := s.verifyGraph(dump); err != nil {
			return nil, err
		}
		return map[string]string{"before": before, "after": after}, nil
	}); err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if err := s.boot(dsn); err != nil {
		return err
	}
	for _, table := range entityTables {
		var name string
		if err := db.GetDB().Raw("SELECT pg_get_serial_sequence(?, 'id')", table).Scan(&name).Error; err != nil {
			return err
		}
		if err := db.GetDB().Exec("ALTER SEQUENCE " + name + " RESTART WITH 1").Error; err != nil {
			return err
		}
	}
	// Force the AFTER-AutoMigrate sequence migration itself to fail. A missing
	// DDL owner would fail earlier in AutoMigrate and would not test migration 16.
	if err := db.GetDB().Exec(`SELECT setval(pg_get_serial_sequence('webhook_endpoints', 'id'),
		(SELECT seqmax FROM pg_catalog.pg_sequence WHERE seqrelid=pg_get_serial_sequence('webhook_endpoints', 'id')::regclass), true)`).Error; err != nil {
		return err
	}
	if err := db.GetDB().Model(&migrate.MigrationRecord{}).Where("version = ?", 16).Update("status", migrate.MigrationRecordStatusFailed).Error; err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if err := s.step("failed-upgrade-remains-retryable", func() (any, error) {
		err := db.InitDB("postgres", dsn, false)
		if err == nil || !strings.Contains(err.Error(), "migration 16") || !strings.Contains(err.Error(), "exhaust") {
			return nil, errors.New("exhausted sequence did not fail migration 16")
		}
		var record migrate.MigrationRecord
		if err := db.GetDB().First(&record, "version = ?", 16).Error; err != nil || record.Status != migrate.MigrationRecordStatusFailed {
			return nil, errors.New("failed migration was marked successful")
		}
		return map[string]any{"migration": 16, "status": "failed", "cause": "sequence-exhaustion", "retryable": true}, nil
	}); err != nil {
		return err
	}
	var exhaustedSequence string
	if err := db.GetDB().Raw("SELECT pg_get_serial_sequence('webhook_endpoints', 'id')").Scan(&exhaustedSequence).Error; err != nil {
		return err
	}
	if err := db.GetDB().Exec("ALTER SEQUENCE " + exhaustedSequence + " RESTART WITH 1").Error; err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	if err := s.step("owner-upgrade-retry-without-reimport", func() (any, error) {
		if err := s.boot(dsn); err != nil {
			return nil, err
		}
		var record migrate.MigrationRecord
		if err := db.GetDB().First(&record, "version = ?", 16).Error; err != nil || record.Status != migrate.MigrationRecordStatusSuccess {
			return nil, errors.New("migration retry did not succeed")
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
			if state.Next <= highest[table] {
				return nil, errors.New("migration retry left a lagging sequence")
			}
		}
		return states, s.verifyGraph(dump)
	}); err != nil {
		return err
	}
	if err := s.step("retry-first-save", func() (any, error) { return nil, s.saveAutoAdd(dump) }); err != nil {
		return err
	}
	return s.step("retry-committed-new-entities", s.createEntities)
}

func (s *suite) waitForLock(conn *gorm.DB, pattern string) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int64
		if err := conn.WithContext(ctx).Raw("SELECT COUNT(*) FROM pg_catalog.pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE ?", pattern).Scan(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("expected database lock wait was not observed")
		case <-ticker.C:
		}
	}
}

func await(ctx context.Context, done <-chan error) error {
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *suite) verifyConcurrentImport(dsn string, dump *model.DBDump) error {
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
	other, err := rawDB("postgres", dsn)
	if err != nil {
		return err
	}
	pool, err := other.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := s.step("ordinary-insert-before-import", func() (any, error) {
		writer := other.WithContext(s.ctx).Begin()
		if writer.Error != nil {
			return nil, writer.Error
		}
		defer writer.Rollback()
		row := model.APIKey{Name: "b01-concurrent-before", APIKey: "synthetic-value", Enabled: true}
		if err := writer.Create(&row).Error; err != nil {
			return nil, err
		}
		incoming := sparseDump(dump, 200000)
		incoming.Channels[0].Name = "b01-concurrent-channel"
		incoming.Groups[0].Name = "b01-concurrent-group"
		done := make(chan error, 1)
		go func() { _, err := op.DBImportIncremental(s.ctx, incoming); done <- err }()
		if err := s.waitForLock(other, "LOCK TABLE %"); err != nil {
			_ = writer.Rollback()
			_ = await(s.ctx, done)
			return nil, err
		}
		if err := writer.Commit().Error; err != nil {
			return nil, err
		}
		if err := await(s.ctx, done); err != nil {
			return nil, err
		}
		var count int64
		if err := other.Model(&model.APIKey{}).Where("id = ? AND name = ?", row.ID, row.Name).Count(&count).Error; err != nil || count != 1 {
			return nil, errors.New("concurrent inserted row lost")
		}
		return map[string]any{"inserted_id": row.ID, "import_waited_for_writer": true}, nil
	}); err != nil {
		return err
	}
	if err := s.step("ordinary-insert-waits-for-import-repair", func() (any, error) {
		tx := db.GetDB().WithContext(s.ctx).Begin(&sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if tx.Error != nil {
			return nil, tx.Error
		}
		defer tx.Rollback()
		if err := sequence.LockForImport(tx); err != nil {
			return nil, err
		}
		row := model.APIKey{Name: "b01-concurrent-after", APIKey: "synthetic-value", Enabled: true}
		done := make(chan error, 1)
		go func() { done <- other.WithContext(s.ctx).Create(&row).Error }()
		if err := s.waitForLock(other, `INSERT INTO "api_keys"%`); err != nil {
			_ = tx.Rollback()
			_ = await(s.ctx, done)
			return nil, err
		}
		if err := tx.Create(&model.APIKey{ID: 1000000, Name: "b01-concurrent-explicit", APIKey: "synthetic-value", Enabled: true}).Error; err != nil {
			_ = tx.Rollback()
			_ = await(s.ctx, done)
			return nil, err
		}
		if err := sequence.Sync(tx); err != nil {
			_ = tx.Rollback()
			_ = await(s.ctx, done)
			return nil, err
		}
		if err := tx.Commit().Error; err != nil {
			return nil, err
		}
		if err := await(s.ctx, done); err != nil || row.ID <= 1000000 {
			return nil, errors.New("ordinary insert used an ID before the import repair")
		}
		return map[string]any{"explicit_id": 1000000, "allocated_id": row.ID, "writer_waited_for_repair": true}, nil
	}); err != nil {
		return err
	}
	return s.step("concurrent-direct-nextval-is-included", func() (any, error) {
		writer := other.WithContext(s.ctx).Begin()
		if writer.Error != nil {
			return nil, writer.Error
		}
		defer writer.Rollback()
		var reserved int64
		if err := writer.Raw("SELECT nextval(pg_get_serial_sequence('channels', 'id'))").Scan(&reserved).Error; err != nil {
			return nil, err
		}
		done := make(chan error, 1)
		go func() {
			done <- db.GetDB().WithContext(s.ctx).Transaction(func(tx *gorm.DB) error { return sequence.Sync(tx) }, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		}()
		if err := s.waitForLock(other, "ALTER SEQUENCE %"); err != nil {
			_ = writer.Rollback()
			_ = await(s.ctx, done)
			return nil, err
		}
		if err := writer.Commit().Error; err != nil {
			return nil, err
		}
		if err := await(s.ctx, done); err != nil {
			return nil, err
		}
		var next int64
		if err := other.Raw("SELECT nextval(pg_get_serial_sequence('channels', 'id'))").Scan(&next).Error; err != nil || next <= reserved {
			return nil, errors.New("concurrent reserved sequence value was not included")
		}
		return map[string]int64{"reserved_before_lock": reserved, "allocated_after_repair": next}, nil
	})
}
