package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"gorm.io/gorm"
)

func b03Dump(id int) *model.DBDump {
	return &model.DBDump{Product: dumpProduct, Version: dbDumpVersion,
		Channels:         []model.Channel{{ID: id, ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("b03-channel-%d", id), BaseURL: "http://127.0.0.1:1", Enabled: true, AllowedProtocols: model.ProtocolOpenAIChatCompletion}}},
		ChannelKeys:      []model.ChannelKey{{ID: id, ChannelID: id, ChannelKeyConfig: model.ChannelKeyConfig{Name: fmt.Sprintf("key-%d", id), Key: "synthetic-b03-credential", Enabled: true}}},
		ChannelModels:    []model.ChannelModel{{ID: id, ChannelID: id, Name: "b03-model"}},
		ChannelGrants:    []model.ChannelGrant{{ID: id, ChannelModelID: id, ChannelKeyID: id, Protocols: model.ProtocolOpenAIChatCompletion}},
		GroupCategories:  []model.BackupGroupCategory{{ID: id, Name: fmt.Sprintf("B03 category %d", id)}},
		Groups:           []model.Group{{ID: id, Name: fmt.Sprintf("b03-group-%d", id), CategoryID: &id, ActiveItemID: id}},
		GroupItems:       []model.GroupItem{{ID: id, GroupID: id, ChannelGrantID: id, Priority: 1}},
		APIKeys:          []model.APIKey{{ID: id, Name: fmt.Sprintf("b03-api-%d", id), APIKey: fmt.Sprintf("synthetic-b03-client-%d", id), Enabled: true}},
		WebhookEndpoints: []model.WebhookEndpoint{{ID: id, Name: fmt.Sprintf("b03-hook-%d", id), URL: "http://127.0.0.1:1", BodyTemplate: "{}", Enabled: true}},
	}
}

func b03Import(t *testing.T, d *model.DBDump) {
	t.Helper()
	if _, err := DBImportIncremental(t.Context(), d); err != nil {
		t.Fatal(err)
	}
}

func TestBackupIntegrityInputAndConflictMatrix(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	base := b03Dump(1)
	b03Import(t, base)
	for _, tc := range []struct {
		name string
		kind error
		edit func(*model.DBDump)
	}{
		{"C02-parent-child", ErrBackupConflict, func(d *model.DBDump) {
			d.Channels[0].BaseURL = "https://different.invalid"
			d.ChannelKeys[0].Key = "forbidden-new-secret"
		}},
		{"C03-key-owner", ErrBackupConflict, func(d *model.DBDump) {
			other := b03Dump(100)
			*d = *other
			d.ChannelKeys[0].ID = 1
			d.ChannelGrants[0].ChannelKeyID = 1
		}},
		{"C04-member-owner", ErrBackupConflict, func(d *model.DBDump) { *d = *b03Dump(100); d.GroupItems[0].ID = 1; d.Groups[0].ActiveItemID = 1 }},
		{"C05-key-name", ErrBackupConflict, func(d *model.DBDump) { d.ChannelKeys[0].ID = 2; d.ChannelGrants[0].ChannelKeyID = 2 }},
		{"credential-change", ErrBackupConflict, func(d *model.DBDump) { d.ChannelKeys[0].Key = "forbidden-new-secret" }},
		{"model-change", ErrBackupConflict, func(d *model.DBDump) { d.ChannelModels[0].Name = "different-model" }},
		{"protocol-change", ErrBackupConflict, func(d *model.DBDump) { d.ChannelGrants[0].Protocols = model.ProtocolAll }},
		{"api-credential", ErrBackupConflict, func(d *model.DBDump) { d.APIKeys[0].APIKey = "forbidden-new-secret" }},
		{"webhook-body", ErrBackupConflict, func(d *model.DBDump) { d.WebhookEndpoints[0].BodyTemplate = `{"secret":"forbidden-new-secret"}` }},
		{"duplicate-primary", ErrBackupInput, func(d *model.DBDump) { d.ChannelKeys = append(d.ChannelKeys, d.ChannelKeys[0]) }},
		{"duplicate-unique", ErrBackupInput, func(d *model.DBDump) {
			other := d.ChannelKeys[0]
			other.ID = 2
			d.ChannelKeys = append(d.ChannelKeys, other)
		}},
		{"C08-missing-grant", ErrBackupInput, func(d *model.DBDump) { d.GroupItems[0].ChannelGrantID = 999 }},
		{"local-ID-cannot-fill-input", ErrBackupInput, func(d *model.DBDump) { d.ChannelKeys = nil }},
		{"cross-channel-input", ErrBackupInput, func(d *model.DBDump) {
			d.Channels = append(d.Channels, b03Dump(100).Channels[0])
			d.ChannelKeys[0].ChannelID = 100
		}},
		{"cross-group-active", ErrBackupInput, func(d *model.DBDump) {
			g := d.Groups[0]
			g.ID = 100
			g.Name = "other-group"
			d.Groups = append(d.Groups, g)
		}},
		{"negative-ID", ErrBackupInput, func(d *model.DBDump) { d.ChannelKeys[0].ID = -1 }},
		{"nested-grant", ErrBackupInput, func(d *model.DBDump) { d.ChannelGrants[0].ChannelKey = &d.ChannelKeys[0] }},
		{"nested-group", ErrBackupInput, func(d *model.DBDump) { d.Groups[0].Items = d.GroupItems }},
		{"nested-hidden-channel", ErrBackupInput, func(d *model.DBDump) { d.Channels[0].Keys = d.ChannelKeys }},
		{"duplicate-prices", ErrBackupInput, func(d *model.DBDump) { d.LLMInfos = []model.LLMInfo{{Name: " Model "}, {Name: "model"}} }},
		{"duplicate-dates", ErrBackupInput, func(d *model.DBDump) { d.StatsDaily = []model.StatsDaily{{Date: "20261113"}, {Date: "20261113"}} }},
		{"invalid-date", ErrBackupInput, func(d *model.DBDump) { d.StatsHourly = []model.StatsHourly{{Hour: 0, Date: "20260230"}} }},
		{"stats-missing-input-parent", ErrBackupInput, func(d *model.DBDump) { d.APIKeys = nil; d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: 1}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := cloneBackupInput(base)
			tc.edit(d)
			before, groups := categoryBackupFingerprint(t), GroupList()
			detail, _ := ChannelDetailGet(1)
			signal := ChannelConfigChanged()
			published := false
			_, err := DBImportIncrementalAndPublish(t.Context(), d, func() { published = true })
			if !errors.Is(err, tc.kind) {
				t.Fatalf("wrong rejection kind: %v", err)
			}
			if strings.Contains(err.Error(), "forbidden-new-secret") || strings.Contains(err.Error(), "synthetic-b03-credential") {
				t.Fatal("error exposed credentials")
			}
			afterDetail, _ := ChannelDetailGet(1)
			if before != categoryBackupFingerprint(t) || !reflect.DeepEqual(groups, GroupList()) || !reflect.DeepEqual(detail, afterDetail) || published {
				t.Fatal("rejection changed data, caches or publication")
			}
			select {
			case <-signal:
				t.Fatal("rejection notified config change")
			default:
			}
		})
	}
	for _, raw := range []string{`{"version":5,"channels":[{"id":1,"name":"n","base_url":"http://127.0.0.1:1","keys":[{"id":2}]}]}`, `{"version":5,"groups":[{"id":1,"name":"g","items":[{"id":2}]}]}`} {
		var d model.DBDump
		if err := json.Unmarshal([]byte(raw), &d); err != nil {
			t.Fatal(err)
		}
		if _, err := DBImportIncremental(t.Context(), &d); !errors.Is(err, ErrBackupInput) {
			t.Fatalf("nested wire associations accepted: %v", err)
		}
	}
}

func TestBackupIntegrityCompatibleReplayAndIncremental(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	d := b03Dump(1)
	b03Import(t, d)
	no := false
	d.Channels[0].Enabled = false
	d.Channels[0].FollowGlobalAutoUpdate = &no
	d.Channels[0].MaxConcurrency = 9
	d.Channels[0].InputToken = 123
	d.ChannelKeys[0].Enabled = false
	d.ChannelKeys[0].InputToken = 456
	d.ChannelModels[0].InputToken = 789
	d.Groups[0].ActiveItemID = 0
	d.GroupItems[0].Priority = 42
	d.APIKeys[0].Enabled = false
	d.WebhookEndpoints[0].Enabled = false
	d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: 1, StatsMetrics: model.StatsMetrics{InputToken: 321}}}
	d.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 999}}}
	d.StatsHourly = []model.StatsHourly{{Hour: 0, Date: "20261113", StatsMetrics: model.StatsMetrics{InputToken: 100}}, {Hour: 1, Date: "20261113", StatsMetrics: model.StatsMetrics{InputToken: 200}}}
	for range 2 {
		b03Import(t, d)
	}
	var c model.Channel
	var k model.ChannelKey
	var g model.Group
	var a model.APIKey
	var w model.WebhookEndpoint
	for _, row := range []any{&c, &k, &g, &a, &w} {
		if err := db.GetDB().First(row, 1).Error; err != nil {
			t.Fatal(err)
		}
	}
	if !c.Enabled || !c.FollowsGlobalAutoUpdate() || c.MaxConcurrency != 0 || c.InputToken != 123 || k.Enabled || k.InputToken != 456 || !a.Enabled || w.Enabled || g.ActiveItemID != 1 {
		t.Fatal("allowed replay differences changed identity or state contract")
	}
	group, _ := GroupGet(1)
	if len(group.Items) != 1 || group.Items[0].Priority != 1 || group.Items[0].Available {
		t.Fatal("replay changed local member priority or enabled state")
	}
	b03Import(t, b03Dump(100))
	if _, err := ChannelGrantGet(100); err != nil {
		t.Fatal(err)
	}
	if StatsTotalGet().InputToken != 999 || StatsAPIKeyGet(1).InputToken != 321 {
		t.Fatal("statistics caches not published")
	}
	if _, err := DBImportIncrementalAndPublish(t.Context(), d, func() {
		if StatsAPIKeyGet(1).InputToken != 321 || StatsAPIKeyGet(999).APIKeyID != 999 {
			t.Fatal("publication callback did not read committed statistics")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := StatsSaveDB(t.Context()); err != nil {
		t.Fatal(err)
	}
	var unused int64
	if err := db.GetDB().Model(&model.StatsAPIKey{}).Where("api_key_id = ?", 999).Count(&unused).Error; err != nil || unused != 0 {
		t.Fatal("statistics read created an orphan row")
	}
}

type b03FaultContext struct{}

func TestBackupIntegrityLiveEmptyRestoreWithStatistics(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	for _, row := range []any{&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{}} {
		var count int64
		if err := db.GetDB().Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatal("fixture must start with initialized caches and empty statistics tables")
		}
	}
	today := time.Now().Format("20060102")
	d := b03Dump(1)
	d.StatsTotal = []model.StatsTotal{{ID: 1, StatsMetrics: model.StatsMetrics{InputToken: 1000, InputCost: 0.1}}}
	d.StatsDaily = []model.StatsDaily{{Date: today, StatsMetrics: model.StatsMetrics{InputToken: 400}}}
	d.StatsHourly = []model.StatsHourly{{Hour: 0, Date: today, StatsMetrics: model.StatsMetrics{InputToken: 100}}}
	d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: 1, StatsMetrics: model.StatsMetrics{InputToken: 300}}}
	b03Import(t, d)
	if err := SaveCache(); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	if StatsTotalGet().InputToken != 1000 || StatsTotalGet().InputCost != 0.1 || StatsAPIKeyGet(1).InputToken != 300 {
		t.Fatal("live empty restore lost imported statistics")
	}
	var daily model.StatsDaily
	var hourly model.StatsHourly
	if err := db.GetDB().Where("date = ?", today).First(&daily).Error; err != nil || daily.InputToken != 400 {
		t.Fatal("daily statistics were overwritten by the empty cache")
	}
	if err := db.GetDB().Where("hour = ?", 0).First(&hourly).Error; err != nil || hourly.InputToken != 100 {
		t.Fatal("midnight statistics were overwritten by the empty cache")
	}
}

func TestBackupIntegrityNewPendingStatisticsOverwriteAndRollback(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	b03Import(t, b03Dump(1))
	now := time.Date(2026, 11, 13, 2, 0, 0, 0, time.Local)
	if err := StatsAPIKeyUpdate(1, model.StatsMetrics{InputToken: 7}); err != nil {
		t.Fatal(err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{InputToken: 8}, now); err != nil {
		t.Fatal(err)
	}
	if err := statsHourlyUpdateAt(model.StatsMetrics{InputToken: 9}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	d := b03Dump(1)
	d.StatsAPIKey = []model.StatsAPIKey{{APIKeyID: 1, StatsMetrics: model.StatsMetrics{InputToken: 700}}}
	d.StatsHourly = []model.StatsHourly{{Hour: 2, Date: "20261113", StatsMetrics: model.StatsMetrics{InputToken: 800}}}
	d.WebhookEndpoints = append(d.WebhookEndpoints, b03Dump(100).WebhookEndpoints[0])
	before := categoryBackupFingerprint(t)
	callback := "b03:new_stats_late_failure"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(b03FaultContext{}) == true && tx.Statement.Table == "webhook_endpoints" {
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO b03_missing_write_target (id) VALUES (1)").Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := DBImportIncremental(context.WithValue(t.Context(), b03FaultContext{}, true), d)
	_ = db.GetDB().Callback().Create().Remove(callback)
	if !errors.Is(err, ErrBackupWrite) || categoryBackupFingerprint(t) != before || StatsAPIKeyGet(1).InputToken != 7 {
		t.Fatalf("late failure changed pending statistics or stored rows: %v", err)
	}
	b03Import(t, d)
	if err := SaveCache(); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	if StatsAPIKeyGet(1).InputToken != 700 {
		t.Fatal("first pending API key statistics did not yield to imported values")
	}
	for hour, want := range map[int]int64{2: 800, 3: 9} {
		var row model.StatsHourly
		if err := db.GetDB().Where("hour = ?", hour).First(&row).Error; err != nil || row.InputToken != want {
			t.Fatal("new statistics were duplicated, lost, or overwritten after publication")
		}
	}
}

func TestBackupIntegrityLateWriteRollbackAndPendingStats(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	b03Import(t, b03Dump(1))
	b03Import(t, b03Dump(2))
	if err := ChannelStatsUpdate(1, model.StatsMetrics{InputToken: 7}); err != nil {
		t.Fatal(err)
	}
	if err := ChannelStatsUpdate(2, model.StatsMetrics{InputToken: 9}); err != nil {
		t.Fatal(err)
	}
	before := categoryBackupFingerprint(t)
	published, writes := false, 0
	callback := "b03:late_sql_failure"
	if err := db.GetDB().Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(b03FaultContext{}) != true {
			return
		}
		if tx.Statement.Table == "webhook_endpoints" {
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO b03_missing_write_target (id) VALUES (1)").Error)
		} else {
			writes++
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := DBImportIncrementalAndPublish(context.WithValue(t.Context(), b03FaultContext{}, true), b03Dump(100), func() { published = true })
	_ = db.GetDB().Callback().Create().Remove(callback)
	if !errors.Is(err, ErrBackupWrite) || writes < 4 || published || categoryBackupFingerprint(t) != before {
		t.Fatalf("late SQL error did not roll back attempted rows: writes=%d, err=%v", writes, err)
	}
	if c, _ := ChannelGet(2); c.InputToken != 9 {
		t.Fatal("failed import lost unsaved statistics")
	}
	d := b03Dump(1)
	d.Channels[0].InputToken = 100
	b03Import(t, d)
	if err := StatsSaveDB(t.Context()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int]int64{1: 100, 2: 9} {
		var c model.Channel
		if err := db.GetDB().First(&c, id).Error; err != nil || c.InputToken != want {
			t.Fatal("old cache overwrote imported statistics or lost unrelated increments")
		}
	}
	before = categoryBackupFingerprint(t)
	callback = "b03:final_graph_fault"
	if err := db.GetDB().Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(b03FaultContext{}) == true && tx.Statement.Table == "group_items" {
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Model(&model.ChannelKey{}).Where("id = ?", 100).Update("channel_id", 2).Error)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err = DBImportIncremental(context.WithValue(t.Context(), b03FaultContext{}, true), b03Dump(100))
	_ = db.GetDB().Callback().Create().Remove(callback)
	if !errors.Is(err, ErrBackupWrite) || categoryBackupFingerprint(t) != before {
		t.Fatalf("final graph corruption was not rolled back: %v", err)
	}
	for _, fault := range []string{"missing-row", "wrong-statistics", "wrong-preserved-state"} {
		t.Run(fault, func(t *testing.T) {
			callback := "b03:final_value_fault"
			if err := db.GetDB().Callback().Create().After("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Context.Value(b03FaultContext{}) != true || tx.Statement.Table != "webhook_endpoints" {
					return
				}
				clean := tx.Session(&gorm.Session{NewDB: true})
				switch fault {
				case "missing-row":
					tx.AddError(clean.Where("id = ?", 100).Delete(&model.WebhookEndpoint{}).Error)
				case "wrong-statistics":
					tx.AddError(clean.Model(&model.Channel{}).Where("id = ?", 100).Update("input_token", 999).Error)
				case "wrong-preserved-state":
					tx.AddError(clean.Model(&model.Channel{}).Where("id = ?", 1).Update("enabled", false).Error)
				}
			}); err != nil {
				t.Fatal(err)
			}
			dump := b03Dump(100)
			dump.Channels = append(dump.Channels, b03Dump(1).Channels[0])
			_, err := DBImportIncremental(context.WithValue(t.Context(), b03FaultContext{}, true), dump)
			_ = db.GetDB().Callback().Create().Remove(callback)
			if !errors.Is(err, ErrBackupWrite) || categoryBackupFingerprint(t) != before {
				t.Fatalf("final persisted-value fault escaped rollback: %s: %v", fault, err)
			}
		})
	}
}

func TestBackupIntegrityOldMismatchReadSaveRefreshAndRestart(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	path := openRetiredSettingsDatabase(t)
	base := b03Dump(1)
	b03Import(t, base)
	b03Import(t, b03Dump(100))
	detail, _ := ChannelDetailGet(1)
	if err := db.GetDB().Model(&model.ChannelKey{}).Where("id = ?", 1).Update("channel_id", 100).Error; err != nil {
		t.Fatal(err)
	}
	for restart := 0; restart < 2; restart++ {
		if restart > 0 {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.InitDB("sqlite", path, false); err != nil {
				t.Fatal(err)
			}
		}
		if err := InitCache(); err != nil {
			t.Fatal(err)
		}
		before := categoryBackupFingerprint(t)
		r, err := integrity.Audit(t.Context(), "sqlite", path)
		if err != nil || !r.Complete || len(r.Issues) != 1 || r.Issues[0].Code != "cross_channel_grant" {
			t.Fatalf("audit missed old mismatch: %v, %+v", err, r)
		}
		if _, err := ChannelGrantGet(1); !errors.Is(err, ErrChannelIntegrity) {
			t.Fatalf("cross-channel key remained available: %v", err)
		}
		if _, err := ChannelRelaySnapshot(1); err == nil {
			t.Fatal("cross-channel key remained routable")
		}
		g, _ := GroupGet(1)
		if g.Items[0].Available || !strings.Contains(g.Items[0].UnavailableReason, "cross-channel") {
			t.Fatal("mismatched member is not visibly unavailable")
		}
		if _, err := ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{AutoAddGroups: true}, t.Context()); !errors.Is(err, ErrChannelIntegrity) {
			t.Fatalf("ordinary save accepted mismatch: %v", err)
		}
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = io.WriteString(w, `{"data":[]}`) }))
		_, err = RefreshChannelModelsWithClient(t.Context(), 1, model.ChannelModelRefreshOptions{Automatic: true, AutoAddGroups: true}, server.Client())
		server.Close()
		if !errors.Is(err, ErrChannelIntegrity) || requests.Load() != 0 || categoryBackupFingerprint(t) != before {
			t.Fatal("read/save/refresh/audit changed damaged rows or sent a request")
		}
	}
	b03ImportErr := func() error { _, err := DBImportIncremental(t.Context(), b03Dump(200)); return err }()
	if !errors.Is(b03ImportErr, ErrBackupConflict) {
		t.Fatal("import ignored existing corruption")
	}
	// Healthy channels still work through ordinary CRUD, even while import is blocked.
	healthy := model.ChannelDetail{ChannelConfig: model.ChannelConfig{Name: "healthy-unrelated", BaseURL: "http://127.0.0.1:1", Enabled: true}}
	if _, err := ChannelCreate(&healthy, t.Context()); err != nil {
		t.Fatal(err)
	}
}

type b03FailPublisher struct{ armed atomic.Bool }

func (p *b03FailPublisher) Configure(int, uint64, []model.CooldownRule) (func(), error) {
	if p.armed.Load() {
		return nil, errors.New("synthetic-private-publication-error")
	}
	return nil, nil
}
func (p *b03FailPublisher) Remove(int, uint64) (func(), error) { return nil, nil }

func TestBackupIntegrityCommittedPublicationFailure(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	d := b03Dump(1)
	b03Import(t, d)
	p := &b03FailPublisher{}
	if err := RegisterChannelCooldownPublisher(p); err != nil {
		t.Fatal(err)
	}
	p.armed.Store(true)
	d.ChannelKeys[0].Enabled = false
	d.Channels[0].InputToken = 999
	published := false
	_, err := DBImportIncrementalAndPublish(t.Context(), d, func() { published = true })
	if !errors.Is(err, ErrBackupCommitted) || published || strings.Contains(err.Error(), "synthetic-private") {
		t.Fatalf("wrong committed failure: %v", err)
	}
	var key model.ChannelKey
	if err := db.GetDB().First(&key, 1).Error; err != nil || key.Enabled {
		t.Fatal("committed rows were incorrectly described as rolled back")
	}
	if _, err := ChannelRelaySnapshot(1); !errors.Is(err, ErrBackupCommitted) {
		t.Fatal("stale relay remained available")
	}
	if err := StatsSaveDB(t.Context()); !errors.Is(err, ErrBackupCommitted) {
		t.Fatal("stale statistics could overwrite committed rows")
	}
	committed := categoryBackupFingerprint(t)
	if _, err := GroupUpdate(1, &model.GroupUpdateRequest{}, t.Context()); !errors.Is(err, ErrBackupCommitted) {
		t.Fatal("stale group cache remained writable")
	}
	if _, err := GroupCategoryDelete(1, t.Context()); !errors.Is(err, ErrBackupCommitted) {
		t.Fatal("stale category cache remained writable")
	}
	if err := ChannelEnabled(1, false, t.Context()); !errors.Is(err, ErrBackupCommitted) {
		t.Fatal("stale channel cache remained writable")
	}
	if committed != categoryBackupFingerprint(t) {
		t.Fatal("blocked mutation changed the committed import")
	}
	p.armed.Store(false)
	if err := RegisterChannelCooldownPublisher(nil); err != nil {
		t.Fatal(err)
	}
	if err := InitCache(); err != nil {
		t.Fatal(err)
	}
	if err := StatsSaveDB(t.Context()); err != nil {
		t.Fatal(err)
	}
	var channel model.Channel
	if err := db.GetDB().First(&channel, 1).Error; err != nil || channel.InputToken != 999 {
		t.Fatal("cache recovery lost the committed import")
	}
}

func TestBackupIntegrityConcurrentSaveStatsAndRefresh(t *testing.T) {
	if runRetiredSettingsIsolated(t) {
		return
	}
	openRetiredSettingsDatabase(t)
	d := b03Dump(1)
	b03Import(t, d)
	detail, _ := ChannelDetailGet(1)
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	callback := "b03:pause_import"
	if err := db.GetDB().Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Context.Value(b03FaultContext{}) == true && tx.Statement.Table == "channels" {
			once.Do(func() { close(entered); <-resume })
		}
	}); err != nil {
		t.Fatal(err)
	}
	imported := make(chan error, 1)
	d.Channels[0].InputToken = 100
	go func() {
		_, err := DBImportIncremental(context.WithValue(t.Context(), b03FaultContext{}, true), d)
		imported <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("import did not reach transaction")
	}
	if channelRefreshCommitMu.TryLock() {
		channelRefreshCommitMu.Unlock()
		t.Fatal("import omitted channel commit lock")
	}
	if groupWriteMu.TryLock() {
		groupWriteMu.Unlock()
		t.Fatal("import omitted group write lock")
	}
	if backupStatsMu.TryRLock() {
		backupStatsMu.RUnlock()
		t.Fatal("import omitted statistics barrier")
	}
	detail.Name = "b03-edited-after-import"
	saved, stats := make(chan error, 1), make(chan error, 1)
	go func() { _, err := ChannelUpdate(&detail, t.Context()); saved <- err }()
	go func() {
		err := ChannelStatsUpdate(1, model.StatsMetrics{InputToken: 7})
		if err == nil {
			err = StatsSaveDB(t.Context())
		}
		stats <- err
	}()
	close(resume)
	for _, done := range []chan error{imported, saved, stats} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("save/import/stats deadlock")
		}
	}
	_ = db.GetDB().Callback().Query().Remove(callback)
	var stored model.Channel
	if err := db.GetDB().First(&stored, 1).Error; err != nil || stored.Name != detail.Name || stored.InputToken != 107 {
		t.Fatal("concurrent save or old statistics lost the committed state")
	}

	probeStarted, finishProbe := make(chan struct{}), make(chan struct{})
	var probeOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeOnce.Do(func() { close(probeStarted) })
		<-finishProbe
		_, _ = io.WriteString(w, `{"data":[{"id":"b03-model"}]}`)
	}))
	defer server.Close()
	detail.BaseURL = server.URL
	if _, err := ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
	refreshDone := make(chan error, 1)
	go func() {
		_, err := RefreshChannelModelsWithClient(t.Context(), 1, model.ChannelModelRefreshOptions{}, server.Client())
		refreshDone <- err
	}()
	select {
	case <-probeStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not probe")
	}
	d.Channels[0].ChannelConfig = detail.ChannelConfig
	d.ChannelKeys[0].Enabled = false
	b03Import(t, d)
	close(finishProbe)
	select {
	case err := <-refreshDone:
		if !errors.Is(err, ErrChannelRefreshStale) {
			t.Fatalf("old network result committed after import: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("refresh/import deadlock")
	}
	group, _ := GroupGet(1)
	if len(group.Items) != 1 {
		t.Fatal("stale refresh silently removed a member")
	}
}
