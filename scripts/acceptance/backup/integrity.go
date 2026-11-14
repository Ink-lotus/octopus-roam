package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"gorm.io/gorm"
)

func integrityInput(base *model.DBDump, offset int) *model.DBDump {
	d := sparseDump(base, offset)
	d.Channels[0].Name = fmt.Sprintf("b03-channel-%d", offset)
	d.Groups[0].Name = fmt.Sprintf("b03-group-%d", offset)
	d.Groups[0].ActiveItemID = d.GroupItems[0].ID
	d.GroupCategories[0].Name = fmt.Sprintf("B03 category %d", offset)
	for i := range d.APIKeys {
		d.APIKeys[i].APIKey = fmt.Sprintf("synthetic-b03-client-%d-%d", offset, i)
	}
	return d
}

func (s *suite) verifyIntegrity(base *model.DBDump) error {
	defer db.Close()
	if err := s.step("B03-initialize", func() (any, error) { return nil, s.boot(s.dsn) }); err != nil {
		return err
	}
	seed := integrityInput(base, 0)
	// A running empty application already has zero-valued statistics caches,
	// while these rows do not yet exist in the database.
	seed.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 1000, InputCost: 0.1}}}
	seed.StatsDaily = []model.StatsDaily{{Date: time.Now().Format("20060102"), StatsMetrics: model.StatsMetrics{InputToken: 400}}}
	seed.StatsHourly = nil
	seed.StatsAPIKey = nil
	if err := s.step("C01-complete-restore-and-replay", func() (any, error) {
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(seed)); err != nil {
			return nil, err
		}
		before, err := snapshot()
		if err != nil {
			return nil, err
		}
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(seed)); err != nil {
			return nil, err
		}
		after, err := snapshot()
		if err != nil || before != after {
			return nil, &assertionFailure{"safe replay changed business content"}
		}
		var total model.StatsTotal
		if err := db.GetDB().First(&total, 1).Error; err != nil {
			return nil, err
		}
		wantCost := 0.1
		if s.opts.dialect == "postgres" {
			wantCost = float64(float32(wantCost))
		}
		if total.InputToken != 1000 || total.InputCost != wantCost || op.StatsTotalGet() != total {
			return nil, &assertionFailure{"empty restore statistics differ from database precision or cache"}
		}
		raw, _ := json.Marshal(seed)
		return map[string]string{"snapshot": after, "input_sha256": digest(raw)}, s.verifyGraph(seed)
	}); err != nil {
		return err
	}
	if err := s.step("first-pending-statistics-and-imported-overwrite", func() (any, error) {
		id := seed.APIKeys[0].ID
		var count int64
		if err := db.GetDB().Model(&model.StatsAPIKey{}).Where("api_key_id = ?", id).Count(&count).Error; err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, &assertionFailure{"first-request statistics fixture was already persisted"}
		}
		if err := op.StatsAPIKeyUpdate(id, model.StatsMetrics{InputToken: 7}); err != nil {
			return nil, err
		}
		d := cloneDump(seed)
		d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: id, StatsMetrics: model.StatsMetrics{InputToken: 700}}}
		if _, err := op.DBImportIncremental(s.ctx, d); err != nil {
			return nil, err
		}
		if err := op.SaveCache(); err != nil {
			return nil, err
		}
		var stored model.StatsAPIKey
		if err := db.GetDB().First(&stored, id).Error; err != nil {
			return nil, err
		}
		if stored.InputToken != 700 || op.StatsAPIKeyGet(id).InputToken != 700 {
			return nil, &assertionFailure{"pending first-request metrics replaced imported statistics"}
		}
		return map[string]int64{"pending": 7, "persisted_and_cached": 700}, nil
	}); err != nil {
		return err
	}
	for _, scenario := range []string{"C02-parent-child", "C03-key-owner", "C04-member-owner", "C05-key-name", "C08-missing-reference", "duplicate-primary", "duplicate-unique", "cross-channel-input", "invalid-active-item", "nested-association", "local-ID-cannot-complete-backup"} {
		if err := s.step(scenario, func() (any, error) {
			d := cloneDump(seed)
			kind := op.ErrBackupConflict
			switch scenario {
			case "C02-parent-child":
				d.Channels[0].BaseURL = "http://127.0.0.1:2"
				d.ChannelKeys[0].Key = "synthetic-must-not-replace"
			case "C03-key-owner":
				d = integrityInput(base, 1000)
				d.ChannelKeys[0].ID = seed.ChannelKeys[0].ID
				d.ChannelGrants[0].ChannelKeyID = seed.ChannelKeys[0].ID
			case "C04-member-owner":
				d = integrityInput(base, 2000)
				d.GroupItems[0].ID = seed.GroupItems[0].ID
				d.Groups[0].ActiveItemID = d.GroupItems[0].ID
			case "C05-key-name":
				d.ChannelKeys[0].ID = 9000
				d.ChannelGrants[0].ChannelKeyID = 9000
			case "C08-missing-reference":
				d = integrityInput(base, 3000)
				d.GroupItems[0].ChannelGrantID = 999999
				kind = op.ErrBackupInput
			case "duplicate-primary":
				d.ChannelKeys = append(d.ChannelKeys, d.ChannelKeys[0])
				kind = op.ErrBackupInput
			case "duplicate-unique":
				row := d.ChannelKeys[0]
				row.ID = 9999
				d.ChannelKeys = append(d.ChannelKeys, row)
				kind = op.ErrBackupInput
			case "cross-channel-input":
				d.Channels = append(d.Channels, integrityInput(base, 3000).Channels[0])
				d.ChannelKeys[0].ChannelID = 3001
				kind = op.ErrBackupInput
			case "invalid-active-item":
				d.Groups[0].ActiveItemID = 999999
				kind = op.ErrBackupInput
			case "nested-association":
				d.ChannelGrants[0].ChannelKey = &d.ChannelKeys[0]
				kind = op.ErrBackupInput
			case "local-ID-cannot-complete-backup":
				d.ChannelKeys = d.ChannelKeys[1:]
				kind = op.ErrBackupInput
			}
			return s.rejectIntegrityInput(d, kind)
		}); err != nil {
			return err
		}
	}
	if err := s.step("database-collation-cannot-update-another-key", func() (any, error) {
		old := seed.ChannelKeys[0]
		key := old
		key.ID = 99001
		key.Name = strings.ToUpper(key.Name)
		key.Key = "synthetic-other-collation-key"
		d := &model.DBDump{Product: "octopus-roam", Version: 1003, Channels: seed.Channels, ChannelKeys: []model.ChannelKey{key}}
		_, err := op.DBImportIncremental(s.ctx, d)
		if err != nil && !errors.Is(err, op.ErrBackupConflict) {
			return nil, err
		}
		if s.opts.dialect == "mysql" && err == nil {
			return nil, &assertionFailure{"MySQL collation collision was not rejected"}
		}
		var saved model.ChannelKey
		if e := db.GetDB().First(&saved, old.ID).Error; e != nil {
			return nil, e
		}
		if saved.Key != old.Key || saved.Name != old.Name || saved.ChannelID != old.ChannelID {
			return nil, &assertionFailure{"collation collision updated an old credential"}
		}
		return map[string]bool{"rejected": err != nil, "old_key_unchanged": true}, nil
	}); err != nil {
		return err
	}
	if err := s.step("compatible-state-statistics-and-unconflicted-increment", func() (any, error) {
		d := cloneDump(seed)
		follow := !seed.Channels[0].FollowsGlobalAutoUpdate()
		d.Channels[0].Enabled = false
		d.Channels[0].FollowGlobalAutoUpdate = &follow
		d.Channels[0].MaxConcurrency = 9
		d.Channels[0].InputToken = 123
		d.ChannelKeys[0].Enabled = false
		d.ChannelKeys[0].InputToken = 456
		d.ChannelModels[0].InputToken = 789
		d.Groups[0].ActiveItemID = 0
		d.GroupItems[0].Priority = 99
		d.APIKeys[0].Enabled = false
		d.WebhookEndpoints[0].Enabled = true
		d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: d.APIKeys[0].ID, StatsMetrics: model.StatsMetrics{InputToken: 321}}}
		if _, err := op.DBImportIncremental(s.ctx, d); err != nil {
			return nil, err
		}
		var channel model.Channel
		var key model.ChannelKey
		var group model.Group
		var api model.APIKey
		for _, row := range []any{&channel, &key, &group, &api} {
			if err := db.GetDB().First(row, 1).Error; err != nil {
				return nil, err
			}
		}
		if channel.Enabled != seed.Channels[0].Enabled || channel.FollowsGlobalAutoUpdate() != seed.Channels[0].FollowsGlobalAutoUpdate() || channel.MaxConcurrency != seed.Channels[0].MaxConcurrency || channel.InputToken != 123 || key.Enabled || key.InputToken != 456 || api.Enabled != seed.APIKeys[0].Enabled || group.ActiveItemID != seed.Groups[0].ActiveItemID {
			return nil, &assertionFailure{"compatible replay state/metrics differed"}
		}
		newGraph := integrityInput(base, 10000)
		if _, err := op.DBImportIncremental(s.ctx, newGraph); err != nil {
			return nil, err
		}
		if _, err := op.ChannelGrantGet(10001); err != nil {
			return nil, err
		}
		// Restore the control's allowed enabled/metrics differences for later checks.
		if _, err := op.DBImportIncremental(s.ctx, cloneDump(seed)); err != nil {
			return nil, err
		}
		return nil, nil
	}); err != nil {
		return err
	}
	if err := s.step("late-SQL-write-full-rollback", func() (any, error) { return s.verifyRollback(seed) }); err != nil {
		return err
	}
	if err := s.step("category-merge-and-reserved-ids", func() (any, error) { return nil, s.verifyCategories(seed) }); err != nil {
		return err
	}
	if err := s.step("C06-C07-hour-zero-one-and-24-slots", func() (any, error) {
		d := &model.DBDump{Product: "octopus-roam", Version: 1003}
		for h := 0; h < 24; h++ {
			d.StatsHourly = append(d.StatsHourly, model.StatsHourly{Hour: h, Date: "20261113", StatsMetrics: model.StatsMetrics{InputToken: int64((h + 1) * 100)}})
		}
		for range 2 {
			if _, err := op.DBImportIncremental(s.ctx, d); err != nil {
				return nil, err
			}
		}
		var hours []model.StatsHourly
		if err := db.GetDB().Order("hour").Find(&hours).Error; err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(hours, d.StatsHourly) {
			return nil, &assertionFailure{"hour slots shifted or overwrote another hour"}
		}
		return map[string]any{"hours": 24, "hour0": 100, "hour1": 200}, nil
	}); err != nil {
		return err
	}
	if err := s.step("concurrent-import-save-statistics", func() (any, error) { return s.verifyIntegrityConcurrency(seed) }); err != nil {
		return err
	}
	if err := s.step("database-lock-blocks-competing-unique-insert", func() (any, error) { return s.verifyIntegrityDatabaseLock(base) }); err != nil {
		return err
	}
	if err := s.step("committed-publication-failure-and-recovery", func() (any, error) { return s.verifyIntegrityPublication(seed) }); err != nil {
		return err
	}
	return s.step("C03-old-mismatch-readonly-audit-save-refresh-restart", func() (any, error) { return s.verifyOldIntegrityMismatch(base) })
}

func (s *suite) rejectIntegrityInput(d *model.DBDump, kind error) (any, error) {
	before, err := snapshot()
	if err != nil {
		return nil, err
	}
	groups, channels := op.GroupList(), stableChannelStats()
	published := false
	raw, _ := json.Marshal(d)
	_, err = op.DBImportIncrementalAndPublish(s.ctx, d, func() { published = true })
	if !errors.Is(err, kind) {
		return nil, &assertionFailure{"input did not receive the required rejection class"}
	}
	if strings.Contains(err.Error(), "synthetic-must-not-replace") {
		return nil, &assertionFailure{"error exposed a credential"}
	}
	after, e := snapshot()
	if e != nil {
		return nil, e
	}
	if before != after || published || !reflect.DeepEqual(groups, op.GroupList()) || !reflect.DeepEqual(channels, stableChannelStats()) {
		return nil, &assertionFailure{"rejected import changed rows, associations, caches or publication"}
	}
	return map[string]string{"input_sha256": digest(raw), "before": before, "after": after, "rejection": kind.Error()}, nil
}

func stableChannelStats() []model.ChannelStats {
	rows := op.ChannelStatsList()
	sort.Slice(rows, func(i, j int) bool { return rows[i].ChannelID < rows[j].ChannelID })
	for i := range rows {
		sort.Slice(rows[i].Models, func(a, b int) bool { return rows[i].Models[a].ModelID < rows[i].Models[b].ModelID })
	}
	return rows
}

type integrityContextKey struct{}

func (s *suite) verifyIntegrityConcurrency(seed *model.DBDump) (any, error) {
	input := cloneDump(seed)
	input.Channels[0].InputToken = 100
	detail, err := op.ChannelDetailGet(1)
	if err != nil {
		return nil, err
	}
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	callback := "b03:pause_import"
	if err := db.GetDB().Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(integrityContextKey{}) == true && tx.Statement.Table == "channels" {
			once.Do(func() { close(entered); <-resume })
		}
	}); err != nil {
		return nil, err
	}
	defer db.GetDB().Callback().Query().Remove(callback)
	imported := make(chan error, 1)
	go func() {
		_, err := op.DBImportIncremental(context.WithValue(s.ctx, integrityContextKey{}, true), input)
		imported <- err
	}()
	select {
	case <-entered:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	saved, stats := make(chan error, 1), make(chan error, 1)
	detail.MaxConcurrency = 4
	go func() { _, err := op.ChannelUpdate(&detail, s.ctx); saved <- err }()
	go func() {
		err := op.ChannelStatsUpdate(1, model.StatsMetrics{InputToken: 7})
		if err == nil {
			err = op.StatsSaveDB(s.ctx)
		}
		stats <- err
	}()
	close(resume)
	for _, done := range []chan error{imported, saved, stats} {
		select {
		case err := <-done:
			if err != nil {
				return nil, err
			}
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		}
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, 1).Error; err != nil {
		return nil, err
	}
	if channel.MaxConcurrency != 4 || channel.InputToken != 107 {
		return nil, &assertionFailure{"concurrent save/statistics overwrote the import"}
	}
	return map[string]any{"input_token": channel.InputToken, "max_concurrency": channel.MaxConcurrency}, nil
}

func (s *suite) verifyIntegrityDatabaseLock(base *model.DBDump) (any, error) {
	input := integrityInput(base, 20000)
	other, err := rawDB(s.opts.dialect, s.dsn)
	if err != nil {
		return nil, err
	}
	pool, _ := other.DB()
	defer pool.Close()
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	callback := "b03:locked_range"
	if err := db.GetDB().Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(integrityContextKey{}) == true && tx.Statement.Table == "channels" {
			once.Do(func() { close(entered); <-resume })
		}
	}); err != nil {
		return nil, err
	}
	defer db.GetDB().Callback().Query().Remove(callback)
	imported := make(chan error, 1)
	go func() {
		_, err := op.DBImportIncremental(context.WithValue(s.ctx, integrityContextKey{}, true), input)
		imported <- err
	}()
	select {
	case <-entered:
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	competing := make(chan error, 1)
	row := input.Channels[0]
	row.ID += 500000
	go func() { competing <- other.WithContext(s.ctx).Create(&row).Error }()
	blocked := false
	select {
	case err := <-competing:
		close(resume)
		<-imported
		return nil, &assertionFailure{fmt.Sprintf("competing DML escaped the import lock (success=%t)", err == nil)}
	case <-time.After(150 * time.Millisecond):
		blocked = true
	}
	close(resume)
	select {
	case err := <-imported:
		if err != nil {
			return nil, err
		}
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	select {
	case err := <-competing:
		if err == nil {
			return nil, &assertionFailure{"competing unique insert unexpectedly succeeded"}
		}
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
	var count int64
	if err := db.GetDB().Model(&model.Channel{}).Where("name = ?", row.Name).Count(&count).Error; err != nil || count != 1 {
		return nil, &assertionFailure{"competing insert changed the accepted entity"}
	}
	return map[string]bool{"competing_writer_blocked": blocked, "unique_insert_rejected": true}, nil
}

type integrityPublisher struct{ fail atomic.Bool }

func (p *integrityPublisher) Configure(int, uint64, []model.CooldownRule) (func(), error) {
	if p.fail.Load() {
		return nil, errors.New("synthetic-private-publisher-failure")
	}
	return nil, nil
}
func (p *integrityPublisher) Remove(int, uint64) (func(), error) { return nil, nil }

func (s *suite) verifyIntegrityPublication(seed *model.DBDump) (any, error) {
	p := &integrityPublisher{}
	if err := op.RegisterChannelCooldownPublisher(p); err != nil {
		return nil, err
	}
	p.fail.Store(true)
	d := cloneDump(seed)
	d.ChannelKeys[0].Enabled = false
	d.Channels[0].InputToken = 909
	published := false
	_, err := op.DBImportIncrementalAndPublish(s.ctx, d, func() { published = true })
	if !errors.Is(err, op.ErrBackupCommitted) || published {
		return nil, &assertionFailure{"post-commit publication error had the wrong outcome"}
	}
	var key model.ChannelKey
	if err := db.GetDB().First(&key, 1).Error; err != nil || key.Enabled {
		return nil, &assertionFailure{"publication failure incorrectly rolled back committed rows"}
	}
	if _, err := op.ChannelRelaySnapshot(1); !errors.Is(err, op.ErrBackupCommitted) {
		return nil, &assertionFailure{"old route remained usable after cache failure"}
	}
	if err := op.StatsSaveDB(s.ctx); !errors.Is(err, op.ErrBackupCommitted) {
		return nil, &assertionFailure{"old metrics remained writeable after cache failure"}
	}
	p.fail.Store(false)
	if err := op.RegisterChannelCooldownPublisher(nil); err != nil {
		return nil, err
	}
	if err := op.InitCache(); err != nil {
		return nil, err
	}
	if err := op.StatsSaveDB(s.ctx); err != nil {
		return nil, err
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, 1).Error; err != nil || channel.InputToken != 909 {
		return nil, &assertionFailure{"cache recovery lost committed metrics"}
	}
	return map[string]bool{"committed": true, "forwarding_paused": true, "statistics_paused": true, "recovery_passed": true}, nil
}

func (s *suite) auditFingerprint() (string, error) {
	state := map[string]any{}
	content, err := snapshot()
	if err != nil {
		return "", err
	}
	state["content"] = content
	var records []map[string]any
	if err := db.GetDB().Table("migration_records").Order("version").Find(&records).Error; err != nil {
		return "", err
	}
	state["migrations"] = records
	var schemaRows []map[string]any
	switch s.opts.dialect {
	case "sqlite":
		if err := db.GetDB().Raw("SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY type,name").Scan(&schemaRows).Error; err != nil {
			return "", err
		}
		var sequences []map[string]any
		if err := db.GetDB().Raw("SELECT name,seq FROM sqlite_sequence ORDER BY name").Scan(&sequences).Error; err != nil {
			return "", err
		}
		state["sequences"] = sequences
	case "mysql":
		if err := db.GetDB().Raw("SELECT table_name,column_name,column_type,column_default,is_nullable,extra FROM information_schema.columns WHERE table_schema=DATABASE() ORDER BY table_name,ordinal_position").Scan(&schemaRows).Error; err != nil {
			return "", err
		}
		var sequences []map[string]any
		if err := db.GetDB().Raw("SELECT table_name,auto_increment FROM information_schema.tables WHERE table_schema=DATABASE() ORDER BY table_name").Scan(&sequences).Error; err != nil {
			return "", err
		}
		state["sequences"] = sequences
	case "postgres":
		if err := db.GetDB().Raw("SELECT table_name,column_name,data_type,column_default,is_nullable FROM information_schema.columns WHERE table_schema=current_schema() ORDER BY table_name,ordinal_position").Scan(&schemaRows).Error; err != nil {
			return "", err
		}
		sequences, err := inspectSequences(db.GetDB())
		if err != nil {
			return "", err
		}
		state["sequences"] = sequences
	}
	state["schema"] = schemaRows
	raw, err := json.Marshal(state)
	return digest(raw), err
}

func (s *suite) verifyOldIntegrityMismatch(base *model.DBDump) (any, error) {
	input := integrityInput(base, 30000)
	if _, err := op.DBImportIncremental(s.ctx, input); err != nil {
		return nil, err
	}
	detail, err := op.ChannelDetailGet(30001)
	if err != nil {
		return nil, err
	}
	// A legacy cross-parent state is deliberately created in this isolated fixture.
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 30001).Updates(map[string]any{"channel_id": 1, "name": "b03-legacy-moved-key"}).Error; err != nil {
		return nil, err
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte(`{"data":[]}`)) }))
	defer server.Close()
	var audit *integrity.AuditReport
	for restart := 0; restart < 2; restart++ {
		if restart > 0 {
			if err := db.Close(); err != nil {
				return nil, err
			}
			if err := s.boot(s.dsn); err != nil {
				return nil, err
			}
		} else if err := op.InitCache(); err != nil {
			return nil, err
		}
		before, err := s.auditFingerprint()
		if err != nil {
			return nil, err
		}
		audit, err = integrity.Audit(s.ctx, s.opts.dialect, s.dsn)
		if err != nil || !audit.Complete {
			return nil, err
		}
		found := false
		for _, issue := range audit.Issues {
			if issue.Table == "channel_grants" && issue.ID == 30001 && issue.Code == "cross_channel_grant" {
				found = true
			}
		}
		if !found {
			return nil, &assertionFailure{"read-only audit missed the legacy key move"}
		}
		after, err := s.auditFingerprint()
		if err != nil || before != after {
			return nil, &assertionFailure{"audit changed schema, data, migration records or sequence state"}
		}
		if _, err := op.ChannelGrantGet(30001); !errors.Is(err, op.ErrChannelIntegrity) {
			return nil, &assertionFailure{"legacy grant remained available"}
		}
		if _, err := op.ChannelRelaySnapshot(30001); err == nil {
			return nil, &assertionFailure{"legacy grant remained routable"}
		}
		group, err := op.GroupGet(30001)
		if err != nil || len(group.Items) != 2 || group.Items[0].Available || group.Items[0].UnavailableReason == "" {
			return nil, &assertionFailure{"legacy member not visibly unavailable"}
		}
		if _, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{AutoAddGroups: true}, s.ctx); !errors.Is(err, op.ErrChannelIntegrity) {
			return nil, &assertionFailure{"ordinary save accepted legacy mismatch"}
		}
		if _, err := op.RefreshChannelModelsWithClient(s.ctx, 30001, model.ChannelModelRefreshOptions{Automatic: true, AutoAddGroups: true}, server.Client()); !errors.Is(err, op.ErrChannelIntegrity) {
			return nil, &assertionFailure{"automatic refresh accepted legacy mismatch"}
		}
		after, err = s.auditFingerprint()
		if err != nil || before != after || requests.Load() != 0 {
			return nil, &assertionFailure{"legacy protection altered members or contacted upstream"}
		}
	}
	raw, _ := json.Marshal(audit)
	if strings.Contains(string(raw), "synthetic-value") {
		return nil, &assertionFailure{"audit exposed credentials"}
	}
	return audit, nil
}
