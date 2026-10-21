package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/dbprofile"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/privacy"
	"github.com/glebarez/sqlite"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var errAssertion = errors.New("acceptance assertion failed")
var errNotEmpty = errors.New("acceptance target is not empty")

const fixtureID = 910001
const rollbackID = 910099
const invalidID = 2140000000
const backupPassword = "synthetic acceptance backup password"

type suite struct {
	ctx    context.Context
	o      options
	r      result
	kind   string
	dsn    string
	caFile string
}

func runStage(o options, profile dbprofile.Profile) (r result) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s := suite{ctx: ctx, o: o, caFile: profile.CAFile, r: result{Status: "failed", Alias: o.alias, Stage: o.stage, Error: "test_failed"}}
	defer func() {
		if recover() != nil {
			s.r.Status, s.r.Error = "failed", "worker_exception"
		}
		r = s.r
	}()
	kind, dsn, err := connection(o, profile, strings.Contains(o.stage, "restore"))
	if err != nil {
		s.r.Error = classify(err)
		return s.r
	}
	s.kind, s.dsn, s.r.Type = kind, dsn, kind
	s.r.RestoreAvailable = o.selfTest || profile.RestoreDatabase != ""
	if o.stage == "product_restart_primary" {
		err = s.test("frozen_executable_api_normal_restart", s.productRuntime)
	} else if o.stage == "preflight" {
		err = s.test("primary_empty_before_migration", func() error { return assertEmpty(ctx, kind, dsn) })
		if err == nil && s.r.RestoreAvailable {
			err = s.test("restore_empty_before_migration", func() error {
				k, d, e := connection(o, profile, true)
				if e != nil {
					return e
				}
				return assertEmpty(ctx, k, d)
			})
		}
	} else {
		closed := false
		// Recheck empty targets in the process that will migrate them. A
		// previous preflight alone is insufficient if another actor wrote.
		if o.stage == "seed" || o.stage == "restore" {
			err = s.test("target_still_empty", func() error { return assertEmpty(ctx, kind, dsn) })
		}
		if err == nil {
			err = s.test("schema_migration_and_cache", func() error {
				if e := db.InitDB(kind, dsn, false); e != nil {
					return e
				}
				return op.InitCache()
			})
		}
		if db.GetDB() != nil {
			defer func() {
				if !closed {
					_ = db.Close()
				}
			}()
		}
		if err == nil {
			err = s.test("engine_version", func() error {
				query := "SELECT VERSION()"
				if kind == "sqlite" {
					query = "SELECT sqlite_version()"
				}
				var version string
				if e := db.GetDB().WithContext(ctx).Raw(query).Scan(&version).Error; e != nil {
					return e
				}
				// Discard vendor/build strings, which may contain private text.
				s.r.EngineVersion = regexp.MustCompile(`[0-9]+(?:\.[0-9]+){1,3}`).FindString(version)
				return require(s.r.EngineVersion != "")
			})
		}
		if err == nil {
			switch o.stage {
			case "seed":
				err = s.seed()
			case "restart_primary":
				err = s.test("new_process_persistence", func() error { return s.verify(false) })
			case "restore":
				err = s.restore()
			case "restart_restore":
				err = s.test("restored_new_process_persistence", func() error { return s.verify(true) })
			default:
				err = errAssertion
			}
		}
		if err == nil {
			err = s.test("normal_cache_save_and_connection_close", func() error {
				if e := op.SaveCache(); e != nil {
					return e
				}
				if e := db.Close(); e != nil {
					return e
				}
				closed = true
				return nil
			})
		}
	}
	if err != nil {
		s.r.Error = classify(err)
		return s.r
	}
	s.r.Status, s.r.Error = "passed", ""
	return s.r
}

func connection(o options, p dbprofile.Profile, restore bool) (string, string, error) {
	if o.selfTest {
		name := "primary.db"
		if restore {
			name = "restore.db"
		}
		return "sqlite", filepath.Join(o.work, name), nil
	}
	if restore {
		if p.RestoreDatabase == "" {
			return "", "", errAssertion
		}
		p.Database = p.RestoreDatabase
		p.RestoreDatabase = ""
	}
	spec, err := p.BuildConnectionSpec()
	if err != nil {
		return "", "", err
	}
	if spec.Type == "mysql" {
		cfg, e := mysqlDriver.ParseDSN(spec.DSN)
		if e != nil {
			return "", "", e
		}
		cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 15*time.Second, 30*time.Second, 30*time.Second
		spec.DSN = cfg.FormatDSN()
	} else {
		u, e := url.Parse(spec.DSN)
		if e != nil {
			return "", "", e
		}
		q := u.Query()
		q.Set("connect_timeout", "15")
		q.Set("statement_timeout", "30000")
		u.RawQuery = q.Encode()
		spec.DSN = u.String()
	}
	return spec.Type, spec.DSN, nil
}

func (s *suite) productDatabaseConfig() (map[string]string, error) {
	config := map[string]string{"type": s.kind, "path": s.dsn}
	if s.kind == "mysql" && s.caFile != "" {
		parsed, err := mysqlDriver.ParseDSN(s.dsn)
		if err != nil {
			return nil, errAssertion
		}
		// Only a built-in TLS mode can cross the process boundary. The
		// original program registers its own CA from the ordinary config.
		parsed.TLS, parsed.TLSConfig, parsed.AllowFallbackToPlaintext = nil, "true", false
		config["path"], config["mysql_ca_file"] = parsed.FormatDSN(), s.caFile
	}
	return config, nil
}

func assertEmpty(ctx context.Context, kind, dsn string) error {
	var dialect gorm.Dialector
	switch kind {
	case "mysql":
		dialect = mysql.Open(dsn)
	case "postgres":
		dialect = postgres.Open(dsn)
	case "sqlite":
		dialect = sqlite.Open(dsn)
	default:
		return errAssertion
	}
	conn, err := gorm.Open(dialect, &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	pool, err := conn.DB()
	if err != nil {
		return err
	}
	defer pool.Close()
	query := "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE()"
	if kind == "postgres" {
		query = "SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = current_schema() AND c.relkind IN ('r','p','v','m','f','S')"
	} else if kind == "sqlite" {
		query = "SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND type IN ('table','view')"
	}
	var count int64
	if err := conn.WithContext(ctx).Raw(query).Scan(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return errNotEmpty
	}
	return nil
}

func (s *suite) test(name string, fn func() error) error {
	started := time.Now()
	err := fn()
	c := check{Name: name, Status: "passed", DurationMS: time.Since(started).Milliseconds()}
	if err != nil {
		c.Status, c.Error = "failed", classify(err)
	}
	s.r.Checks = append(s.r.Checks, c)
	return err
}

func require(ok bool) error {
	if !ok {
		return errAssertion
	}
	return nil
}

func (s *suite) seed() error {
	if err := s.test("defaults_and_reserved_setting_write", func() error {
		if value, e := op.SettingGetString(model.SettingKeyStatsSaveInterval); e != nil || value != "10" {
			return errAssertion
		}
		if e := op.UserInit(); e != nil {
			return e
		}
		if e := op.UserVerify("admin", "admin"); e != nil {
			return e
		}
		return op.SettingSetString(model.SettingKeyStatsSaveInterval, "11")
	}); err != nil {
		return err
	}
	if err := s.test("explicit_disabled_json_configuration", func() error {
		no := false
		policy := model.DefaultGroupRelayConfig()
		policy.MemberMaxAttempts, policy.RetryMode, policy.RetryInitialIntervalSeconds = 4, model.RetryModeExponential, 2
		privacyConfig := privacy.DefaultConfig()
		privacyConfig.CustomRules = []privacy.Rule{{ID: privacy.NewRuleID(), Name: "synthetic-rule", Kind: "literal", Pattern: "synthetic-private-value", Enabled: false}}
		privacyJSON, _ := json.Marshal(privacyConfig)
		policyJSON, _ := json.Marshal(policy)
		dump := &model.DBDump{Product: "octopus-roam", Version: 1000,
			Channels:         []model.Channel{{ID: fixtureID, ChannelConfig: model.ChannelConfig{Name: "env-disabled-channel", BaseURL: "http://127.0.0.1:1", Enabled: false, FollowGlobalAutoUpdate: &no, MaxConcurrency: 3, AllowedProtocols: model.ProtocolOpenAIResponse, CustomHeader: []model.CustomHeader{{HeaderKey: "X-Fixture", HeaderValue: "synthetic-value"}}, ParamOverride: `{"temperature":0.1}`}}},
			ChannelKeys:      []model.ChannelKey{{ID: fixtureID, ChannelID: fixtureID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "disabled-key", Key: "synthetic-disabled-key", Enabled: false}}},
			ChannelModels:    []model.ChannelModel{{ID: fixtureID, ChannelID: fixtureID, Name: "env-model"}},
			ChannelGrants:    []model.ChannelGrant{{ID: fixtureID, ChannelModelID: fixtureID, ChannelKeyID: fixtureID, Protocols: model.ProtocolOpenAIResponse}},
			Groups:           []model.Group{{ID: fixtureID, Name: "env-disabled-group", Mode: model.GroupModeFailover, ModelMatchMode: model.GroupModelMatchModeExact, RelayConfig: policy}},
			GroupItems:       []model.GroupItem{{ID: fixtureID, GroupID: fixtureID, ChannelGrantID: fixtureID, Priority: 2}},
			APIKeys:          []model.APIKey{{ID: fixtureID, Name: "env-disabled-api-key", APIKey: "synthetic-api-key", Enabled: false, SupportedModels: []string{"env-disabled-group"}}},
			WebhookEndpoints: []model.WebhookEndpoint{{ID: fixtureID, Name: "env-disabled-hook", Enabled: false, URL: "http://127.0.0.1:1/hook", Method: "POST", ContentType: "application/json", Headers: map[string]string{"X-Fixture": "synthetic-header"}, BodyTemplate: "{}", TimeoutSeconds: 15}},
			Settings:         []model.Setting{{Key: model.SettingKeyPrivacyProtectionRules, Value: string(privacyJSON)}, {Key: model.SettingKeyPrivacyProtectionEnabled, Value: "true"}, {Key: model.SettingKeyGroupRouteDefaults, Value: string(policyJSON)}, {Key: model.SettingKeyChannelAutoUpdateEnabled, Value: "true"}, {Key: model.SettingKeyChannelAutoUpdateInterval, Value: "60"}, {Key: model.SettingKeyChannelAutoAddGroups, Value: "true"}},
		}
		if _, e := op.DBImportIncremental(s.ctx, dump); e != nil {
			return e
		}
		if e := op.InitCache(); e != nil {
			return e
		}
		detail, e := op.ChannelDetailGet(fixtureID)
		if e != nil {
			return e
		}
		detail.MaxConcurrency = 5
		if _, e := op.ChannelUpdate(&detail, s.ctx); e != nil {
			return e
		}
		return s.checkConfiguration()
	}); err != nil {
		return err
	}
	if s.kind != "sqlite" {
		if err := s.test("database_row_lock_serialization", s.rowLock); err != nil {
			return err
		}
	} else {
		s.r.Checks = append(s.r.Checks, check{Name: "database_row_lock_serialization", Status: "pending"})
	}
	if err := s.refresh(); err != nil {
		return err
	}
	if err := s.test("backup_consistent_snapshot_during_delete", s.snapshot); err != nil {
		return err
	}
	if err := s.test("invalid_import_database_write_rollback", s.failedImport); err != nil {
		return err
	}
	return s.test("encrypted_backup_and_rejected_password_damage", func() error {
		if e := op.SaveCache(); e != nil {
			return e
		}
		dump, e := op.DBExportAll(s.ctx)
		if e != nil {
			return e
		}
		if e = require(dump.Product == "octopus-roam" && dump.Version == 1000); e != nil {
			return e
		}
		encrypted, e := op.EncryptBackup(dump, backupPassword)
		if e != nil {
			return e
		}
		if bytes.Contains(encrypted, []byte(backupPassword)) || bytes.Contains(encrypted, []byte("synthetic-disabled-key")) {
			return errAssertion
		}
		before, e := s.fingerprint()
		if e != nil {
			return e
		}
		if _, e := op.DecryptBackup(encrypted, "wrong synthetic password"); e == nil {
			return errAssertion
		}
		var wire map[string]json.RawMessage
		if json.Unmarshal(encrypted, &wire) != nil {
			return errAssertion
		}
		var ciphertext string
		if json.Unmarshal(wire["ciphertext"], &ciphertext) != nil || len(ciphertext) < 8 {
			return errAssertion
		}
		// Mutate a byte in the encoded ciphertext, preserving envelope shape.
		replacement := "A"
		if ciphertext[0] == 'A' {
			replacement = "B"
		}
		wire["ciphertext"], _ = json.Marshal(replacement + ciphertext[1:])
		damaged, _ := json.Marshal(wire)
		if _, e := op.DecryptBackup(damaged, backupPassword); e == nil {
			return errAssertion
		}
		after, e := s.fingerprint()
		if e != nil {
			return e
		}
		if !bytes.Equal(before, after) {
			return errAssertion
		}
		file, e := os.OpenFile(filepath.Join(s.o.work, "backup.encrypted.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if e != nil {
			return e
		}
		_, e = file.Write(encrypted)
		return errors.Join(e, file.Close())
	})
}

func (s *suite) checkConfiguration() error {
	var channel model.Channel
	var key model.ChannelKey
	var group model.Group
	var item model.GroupItem
	var apiKey model.APIKey
	var hook model.WebhookEndpoint
	for _, row := range []any{&channel, &key, &group, &item, &apiKey, &hook} {
		if e := db.GetDB().WithContext(s.ctx).First(row, fixtureID).Error; e != nil {
			return e
		}
	}
	if channel.Enabled || channel.FollowsGlobalAutoUpdate() || channel.MaxConcurrency != 5 || channel.AllowedProtocols != model.ProtocolOpenAIResponse ||
		!reflect.DeepEqual(channel.CustomHeader, []model.CustomHeader{{HeaderKey: "X-Fixture", HeaderValue: "synthetic-value"}}) || channel.ParamOverride != `{"temperature":0.1}` ||
		key.Enabled || key.Key != "synthetic-disabled-key" || group.Mode != model.GroupModeFailover || group.ModelMatchMode != model.GroupModelMatchModeExact ||
		group.RelayConfig.MemberMaxAttempts != 4 || group.RelayConfig.RetryMode != model.RetryModeExponential || item.Priority != 2 || item.ChannelGrantID != fixtureID ||
		apiKey.Enabled || !slices.Equal(apiKey.SupportedModels, []string{"env-disabled-group"}) || hook.Enabled || hook.Headers["X-Fixture"] != "synthetic-header" || hook.TimeoutSeconds != 15 {
		return errAssertion
	}
	policy, e := op.GroupRouteDefaultsGet()
	if e != nil {
		return e
	}
	rules, e := op.PrivacyRulesGet()
	if e != nil {
		return e
	}
	auto, e := op.SettingGetBool(model.SettingKeyChannelAutoUpdateEnabled)
	if e != nil {
		return e
	}
	return require(policy.MemberMaxAttempts == 4 && rules.Enabled && len(rules.CustomRules) == 1 && !rules.CustomRules[0].Enabled && rules.CustomRules[0].Pattern == "synthetic-private-value" && auto)
}

func (s *suite) rowLock() error {
	first := db.GetDB().WithContext(s.ctx).Begin()
	if first.Error != nil {
		return first.Error
	}
	defer first.Rollback()
	var channel model.Channel
	if e := first.Clauses(clause.Locking{Strength: "UPDATE"}).First(&channel, fixtureID).Error; e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		second := db.GetDB().WithContext(ctx).Begin()
		if second.Error != nil {
			close(started)
			done <- second.Error
			return
		}
		defer second.Rollback()
		close(started)
		var row model.Channel
		done <- second.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, fixtureID).Error
	}()
	<-started
	select {
	case <-done:
		return errAssertion
	case <-time.After(500 * time.Millisecond):
	}
	if e := first.Commit().Error; e != nil {
		return e
	}
	select {
	case e := <-done:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *suite) refresh() error {
	var mode atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 1:
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":"synthetic fixture failure"}`))
		case 2:
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			_, _ = w.Write([]byte(`{"data":[{"id":"env-refresh-model"}]}`))
		}
	}))
	defer fixture.Close()
	// An exact loopback fixture client with no proxy and no redirect follow.
	client := fixture.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errAssertion }
	no := false
	detail, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "env-refresh-channel", Enabled: true, FollowGlobalAutoUpdate: &no, BaseURL: fixture.URL, AllowedProtocols: model.ProtocolOpenAIResponse}, Keys: []model.ChannelKeyConfig{{Name: "fixture-key", Key: "synthetic-refresh-key", Enabled: true}}, Models: []string{"env-old-model"}, Grants: []model.ChannelGrantConfig{{ModelName: "env-old-model", KeyName: "fixture-key", Protocols: model.ProtocolOpenAIResponse}}}, s.ctx)
	if err != nil {
		return err
	}
	var oldGrant int
	for _, grant := range op.ChannelGrantCandidates() {
		if grant.ChannelID == detail.ID {
			oldGrant = grant.ID
		}
	}
	group, err := op.GroupCreate(&model.GroupCreateRequest{Name: "env-refresh-model", Mode: model.GroupModeManual, ModelMatchMode: model.GroupModelMatchModeExact, Items: []model.GroupItemInput{{ChannelGrantID: oldGrant}}}, s.ctx)
	if err != nil || len(group.Items) != 1 {
		return errAssertion
	}
	active := group.Items[0].ID
	if _, err = op.GroupUpdate(group.ID, &model.GroupUpdateRequest{ActiveItemID: &active}, s.ctx); err != nil {
		return err
	}
	refresh := func(ctx context.Context) (model.ChannelModelRefreshResult, error) {
		return op.RefreshChannelModelsWithClient(ctx, detail.ID, model.ChannelModelRefreshOptions{AutoAddGroups: true}, client)
	}
	if err = s.test("refresh_transaction_database_failure_rollback", func() error {
		before, e := s.fingerprint()
		if e != nil {
			return e
		}
		type marker struct{}
		attempted := false
		name := "acceptance:refresh_failure"
		if e := db.GetDB().Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
			if tx.Statement.Context.Value(marker{}) != true || tx.Statement.Table != "group_items" {
				return
			}
			attempted = true
			// A real FK violation inside the production refresh transaction.
			e := tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO group_items (id, group_id, channel_grant_id, priority) VALUES (?, ?, ?, ?)", invalidID, invalidID, invalidID, 1).Error
			if e == nil {
				e = errAssertion
			}
			tx.AddError(e)
		}); e != nil {
			return e
		}
		defer db.GetDB().Callback().Create().Remove(name)
		res, e := refresh(context.WithValue(s.ctx, marker{}, true))
		if !attempted || e == nil || res.Success {
			return errAssertion
		}
		after, e := s.fingerprint()
		if e != nil {
			return e
		}
		return require(bytes.Equal(before, after))
	}); err != nil {
		return err
	}
	if err = s.test("refresh_success_grant_group_record_sync", func() error {
		res, e := refresh(s.ctx)
		if e != nil {
			return e
		}
		if !res.Success || !slices.Equal(res.AddedModels, []string{"env-refresh-model"}) || !slices.Equal(res.RemovedModels, []string{"env-old-model"}) {
			return errAssertion
		}
		updated, e := op.GroupGet(group.ID)
		if e != nil {
			return e
		}
		record := op.ChannelRefreshStatus(detail.ID)
		return require(len(updated.Items) == 1 && updated.Items[0].ModelName == "env-refresh-model" && updated.ActiveItemID == 0 && record != nil && record.Last != nil && record.Last.Status == "success")
	}); err != nil {
		return err
	}
	if err = s.test("refresh_upstream_failure_preserves_data", func() error {
		mode.Store(1)
		before, e := s.fingerprint()
		if e != nil {
			return e
		}
		res, e := refresh(s.ctx)
		if e != nil || res.Success {
			return errAssertion
		}
		after, e := s.fingerprint()
		if e != nil {
			return e
		}
		record := op.ChannelRefreshStatus(detail.ID)
		return require(bytes.Equal(before, after) && record != nil && record.Last != nil && record.Last.Status == "failed")
	}); err != nil {
		return err
	}
	if err = s.test("refresh_success_empty_clears_grants_members", func() error {
		mode.Store(2)
		res, e := refresh(s.ctx)
		if e != nil {
			return e
		}
		updated, e := op.GroupGet(group.ID)
		if e != nil {
			return e
		}
		d, e := op.ChannelDetailGet(detail.ID)
		if e != nil {
			return e
		}
		return require(res.Success && res.ZeroModels && len(d.Models) == 0 && len(d.Grants) == 0 && len(updated.Items) == 0 && updated.ActiveItemID == 0)
	}); err != nil {
		return err
	}
	mode.Store(0)
	res, err := refresh(s.ctx)
	if err != nil {
		return err
	}
	return require(res.Success)
}

func (s *suite) snapshot() error {
	channel, err := op.ChannelCreate(&model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "env-snapshot-channel", Enabled: true, BaseURL: "http://127.0.0.1:1"}, Keys: []model.ChannelKeyConfig{{Name: "snapshot-key", Key: "synthetic-snapshot-key", Enabled: true}}}, s.ctx)
	if err != nil {
		return err
	}
	type marker struct{}
	changed := false
	name := "acceptance:export_snapshot"
	if err := db.GetDB().Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if changed || tx.Statement.Table != "channels" || tx.Statement.Context.Value(marker{}) != true {
			return
		}
		changed = true
		if e := op.ChannelDel(channel.ID, s.ctx); e != nil {
			tx.AddError(e)
		}
	}); err != nil {
		return err
	}
	defer db.GetDB().Callback().Query().Remove(name)
	dump, err := op.DBExportAll(context.WithValue(s.ctx, marker{}, true))
	if err != nil {
		return err
	}
	foundChannel, foundKey := false, false
	for _, c := range dump.Channels {
		foundChannel = foundChannel || c.ID == channel.ID
	}
	for _, k := range dump.ChannelKeys {
		foundKey = foundKey || k.ChannelID == channel.ID
	}
	return require(changed && foundChannel && foundKey)
}

func (s *suite) failedImport() error {
	before, err := s.fingerprint()
	if err != nil {
		return err
	}
	dump := &model.DBDump{Product: "octopus-roam", Version: 1000, Groups: []model.Group{{ID: rollbackID, Name: "env-rollback-only-group", Mode: model.GroupModeManual}}, GroupItems: []model.GroupItem{{ID: rollbackID, GroupID: rollbackID, ChannelGrantID: invalidID, Priority: 1}}}
	wrote, failed := false, false
	name := "acceptance:import_write_observer"
	if err := db.GetDB().Callback().Create().After("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "groups" && tx.RowsAffected > 0 && tx.Error == nil {
			wrote = true
		}
		if tx.Statement.Table == "group_items" && tx.Error != nil {
			failed = true
		}
	}); err != nil {
		return err
	}
	defer db.GetDB().Callback().Create().Remove(name)
	_, err = op.DBImportIncremental(s.ctx, dump)
	if err == nil || !wrote || !failed {
		return errAssertion
	}
	after, err := s.fingerprint()
	if err != nil {
		return err
	}
	var count int64
	if e := db.GetDB().Model(&model.Group{}).Where("id = ?", rollbackID).Count(&count).Error; e != nil {
		return e
	}
	return require(count == 0 && bytes.Equal(before, after))
}

func (s *suite) fingerprint() ([]byte, error) {
	dump, err := op.DBExportAll(s.ctx)
	if err != nil {
		return nil, err
	}
	return canonical(dump)
}

func canonical(dump *model.DBDump) ([]byte, error) {
	// Export order is database-dependent. Normalize only ordering, export
	// timestamps and the intentionally renewed privacy editor revision.
	raw, err := json.Marshal(dump)
	if err != nil {
		return nil, err
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	delete(value, "exported_at")
	for _, field := range value {
		rows, ok := field.([]any)
		if !ok {
			continue
		}
		for _, row := range rows {
			object, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if object["key"] == string(model.SettingKeyPrivacyProtectionRules) {
				var rules map[string]any
				if json.Unmarshal([]byte(fmt.Sprint(object["value"])), &rules) != nil {
					return nil, errAssertion
				}
				delete(rules, "revision")
				encoded, _ := json.Marshal(rules)
				object["value"] = string(encoded)
			}
		}
		sort.Slice(rows, func(i, j int) bool {
			a, _ := json.Marshal(rows[i])
			b, _ := json.Marshal(rows[j])
			return bytes.Compare(a, b) < 0
		})
	}
	return json.Marshal(value)
}

func (s *suite) readBackup() (*model.DBDump, error) {
	raw, err := os.ReadFile(filepath.Join(s.o.work, "backup.encrypted.json"))
	if err != nil {
		return nil, err
	}
	return op.DecryptBackup(raw, backupPassword)
}

func (s *suite) verify(restored bool) error {
	if err := s.checkConfiguration(); err != nil {
		return err
	}
	backup, err := s.readBackup()
	if err != nil {
		return err
	}
	expected, err := canonical(backup)
	if err != nil {
		return err
	}
	actual, err := s.fingerprint()
	if err != nil {
		return err
	}
	if !bytes.Equal(expected, actual) {
		return errAssertion
	}
	var diagnostics int64
	if err := db.GetDB().Model(&model.ChannelRefreshRecord{}).Count(&diagnostics).Error; err != nil {
		return err
	}
	if restored {
		return require(diagnostics == 0)
	}
	return require(diagnostics == 1)
}

func (s *suite) restore() error {
	if err := s.test("empty_restore_defaults", func() error {
		value, err := op.SettingGetString(model.SettingKeyStatsSaveInterval)
		if err != nil {
			return err
		}
		return require(value == "10")
	}); err != nil {
		return err
	}
	return s.test("encrypted_empty_database_restore", func() error {
		dump, err := s.readBackup()
		if err != nil {
			return err
		}
		if _, err := op.DBImportIncremental(s.ctx, dump); err != nil {
			return err
		}
		if err := op.InitCache(); err != nil {
			return err
		}
		return s.verify(true)
	})
}
