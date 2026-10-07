package op

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

func TestChannelAutomationSettingsAndBackupDefaults(t *testing.T) {
	for _, key := range []model.SettingKey{model.SettingKeyChannelAutoUpdateEnabled, model.SettingKeyChannelAutoAddGroups, model.SettingKeyChannelAutoUpdateInterval} {
		old, err := SettingGetString(key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = SettingSetString(key, old) })
		for _, value := range []string{"-1", "0", "10081", "bad"} {
			if err := SettingSetString(key, value); err == nil {
				t.Fatalf("accepted %s=%s", key, value)
			}
		}
	}
	if v, _ := SettingGetBool(model.SettingKeyChannelAutoUpdateEnabled); v {
		t.Fatal("automation enabled by default")
	}
	changed := AutomationSettingsChanged()
	if err := SettingSetString(model.SettingKeyChannelAutoUpdateInterval, "1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Fatal("setting update lost")
	}
	var detail model.ChannelDetail
	if err := json.Unmarshal([]byte(`{"name":"automation-legacy","base_url":"http://127.0.0.1","enabled":true,"keys":[],"models":[],"grants":[]}`), &detail); err != nil {
		t.Fatal(err)
	}
	created, err := ChannelCreate(&detail, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !created.FollowsGlobalAutoUpdate() || created.MaxConcurrency != 0 {
		t.Fatal("legacy defaults changed")
	}
	no := false
	created.FollowGlobalAutoUpdate, created.MaxConcurrency = &no, 2
	if _, err := ChannelUpdate(created, t.Context()); err != nil {
		t.Fatal(err)
	}
	var row model.Channel
	if err := db.GetDB().First(&row, created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if row.FollowsGlobalAutoUpdate() || row.MaxConcurrency != 2 {
		t.Fatal("configuration did not persist")
	}
	dump, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range dump.Channels {
		if c.ID == created.ID {
			found = true
			if c.FollowsGlobalAutoUpdate() || c.MaxConcurrency != 2 {
				t.Fatal("backup lost settings")
			}
		}
	}
	if !found {
		t.Fatal("channel missing in backup")
	}
	legacy := model.Channel{ID: created.ID + 100000, ChannelConfig: model.ChannelConfig{Name: "automation-legacy-import", BaseURL: "http://127.0.0.1", Enabled: true}}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: 5, Channels: []model.Channel{legacy}}); err != nil {
		t.Fatal(err)
	}
	var imported model.Channel
	if err := db.GetDB().First(&imported, legacy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !imported.FollowsGlobalAutoUpdate() || imported.MaxConcurrency != 0 {
		t.Fatal("old backup defaults changed")
	}
	created.MaxConcurrency = -1
	if _, err := ChannelUpdate(created, t.Context()); err == nil {
		t.Fatal("negative concurrency accepted")
	}
}

func TestSettingReloadOnlySignalsAutomationChanges(t *testing.T) {
	for _, scenario := range []string{"unchanged", "unrelated", "changed", "initial"} {
		t.Run(scenario, func(t *testing.T) {
			key := model.SettingKeyChannelAutoUpdateInterval
			old, _ := SettingGetString(key)
			switch scenario {
			case "changed":
				settingCache.Set(key, "10080")
			case "initial":
				settingCache.Del(key)
			case "unrelated":
				settingCache.Set(model.SettingKeyProxyURL, "test-cache-value")
			}
			t.Cleanup(func() { settingCache.Set(key, old) })
			changed := AutomationSettingsChanged()
			if err := settingRefreshCache(t.Context()); err != nil {
				t.Fatal(err)
			}
			signaled := false
			select {
			case <-changed:
				signaled = true
			default:
			}
			if signaled != (scenario == "changed" || scenario == "initial") {
				t.Fatalf("reload signal=%v for %s", signaled, scenario)
			}
		})
	}
}

func TestManualRefreshMaintenanceCanExceedTenSeconds(t *testing.T) {
	channelRefreshObservers.Lock()
	previous := channelRefreshObservers.maintenance
	channelRefreshObservers.Unlock()
	t.Cleanup(func() { SetChannelRefreshMaintenance(previous) })
	completed := false
	SetChannelRefreshMaintenance(func(ctx context.Context, _ []string) []string {
		select {
		case <-time.After(10100 * time.Millisecond):
			completed = true
			return nil
		case <-ctx.Done():
			return []string{ctx.Err().Error()}
		}
	})
	server := newModelListServer(t, func(string, bool) (int, string) { return http.StatusOK, `{"data":[]}` })
	id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
	result, err := RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if err != nil || !result.Success || !completed || len(result.Warnings) != 0 {
		t.Fatalf("manual maintenance prematurely canceled: completed=%v result=%+v err=%v", completed, result, err)
	}
}

func TestRefreshMaintenanceCancellationPolicy(t *testing.T) {
	channelRefreshObservers.Lock()
	previous := channelRefreshObservers.maintenance
	channelRefreshObservers.Unlock()
	t.Cleanup(func() { SetChannelRefreshMaintenance(previous) })
	old, _ := SettingGetString(model.SettingKeyChannelAutoUpdateEnabled)
	t.Cleanup(func() { _ = SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, old) })
	if err := SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	server := newModelListServer(t, func(string, bool) (int, string) { return http.StatusOK, `{"data":[]}` })
	for _, automatic := range []bool{false, true} {
		id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		called := false
		SetChannelRefreshMaintenance(func(maintenanceCtx context.Context, _ []string) []string {
			called = true
			cancel()
			if automatic {
				deadline, ok := maintenanceCtx.Deadline()
				if maintenanceCtx.Err() != nil || !ok || time.Until(deadline) < 110*time.Second || time.Until(deadline) > 2*time.Minute {
					t.Error("automatic maintenance did not retain its independent two-minute budget")
				}
			} else if !errors.Is(maintenanceCtx.Err(), context.Canceled) {
				t.Error("manual maintenance ignored request cancellation")
			}
			return nil
		})
		result, err := RefreshChannelModels(ctx, id, model.ChannelModelRefreshOptions{Automatic: automatic})
		cancel()
		if err != nil || !result.Success || !called {
			t.Fatalf("maintenance not called: %+v %v", result, err)
		}
	}
}

func TestRefreshDiagnosticCopiesDoNotMutateCachedRecord(t *testing.T) {
	const id = -1000
	record := model.ChannelRefreshRecord{ChannelID: id, Status: "success", Result: model.ChannelModelRefreshResult{
		AddedModels: []string{"added"}, RemovedModels: []string{"removed"}, Warnings: []string{"warning"},
		KeyResults: []model.ChannelModelRefreshKeyResult{{KeyName: "original"}},
	}}
	channelRefreshRecords.Set(id, record)
	t.Cleanup(func() { channelRefreshRecords.Del(id) })
	detail := ChannelRefreshStatus(id)
	detail.Last.Status = "changed"
	detail.Last.Result.AddedModels[0] = "changed"
	detail.Last.Result.RemovedModels[0] = "changed"
	detail.Last.Result.Warnings[0] = "changed"
	detail.Last.Result.KeyResults[0].KeyName = "changed"
	summary := ChannelRefreshSummary(id)
	summary.Last.Status = "changed"
	got := ChannelRefreshStatus(id)
	if got.Last.Status != "success" || got.Last.Result.AddedModels[0] != "added" ||
		got.Last.Result.RemovedModels[0] != "removed" || got.Last.Result.Warnings[0] != "warning" ||
		got.Last.Result.KeyResults[0].KeyName != "original" || ChannelRefreshSummary(id).Last.Status != "success" {
		t.Fatal("diagnostic mutation leaked into shared cache")
	}
}

func TestChannelRefreshRecordFailureSuccessAndReload(t *testing.T) {
	previous, _ := SettingGetString(model.SettingKeyPrivacyProtectionEnabled)
	t.Cleanup(func() { _ = SettingSetString(model.SettingKeyPrivacyProtectionEnabled, previous) })
	if err := SettingSetString(model.SettingKeyPrivacyProtectionEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	secret := "automation-private-credential"
	var fail atomic.Bool
	fail.Store(true)
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		if fail.Load() {
			return http.StatusServiceUnavailable, `{"error":"` + secret + `"}`
		}
		return http.StatusOK, `{"data":[]}`
	})
	id, keys := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: secret, Enabled: true}})
	group, grant := addRefreshGroup(t, id, keys["key"])
	before, _ := ChannelDetailGet(id)
	result, err := RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if err != nil || result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertFailedRefreshPreservesState(t, before, group, grant)
	status := ChannelRefreshStatus(id)
	if status.Running || status.Last == nil || status.Last.Status != "failed" {
		t.Fatal(status)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), secret) {
		t.Fatal("refresh status leaks credential")
	}
	var persisted model.ChannelRefreshRecord
	if err := db.GetDB().First(&persisted, "channel_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(persisted)
	if strings.Contains(string(raw), secret) {
		t.Fatal("stored status leaks credential")
	}
	fail.Store(false)
	result, err = RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if err != nil || !result.Success || !result.ZeroModels {
		t.Fatalf("empty result=%+v err=%v", result, err)
	}
	if err := loadChannelRefreshRecords(t.Context()); err != nil {
		t.Fatal(err)
	}
	status = ChannelRefreshStatus(id)
	if status.Last.Status != "success" || status.Last.BeforeCount != 1 || status.Last.AfterCount != 0 {
		t.Fatal(status.Last)
	}
	if _, err := GroupGet(group.ID); err != nil {
		t.Fatal("refresh deleted group")
	}
}

func TestAutomaticRefreshHonorsEligibilityAndBusyStatus(t *testing.T) {
	old, _ := SettingGetString(model.SettingKeyChannelAutoUpdateEnabled)
	t.Cleanup(func() { _ = SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, old) })
	started, proceed := make(chan struct{}, 8), make(chan struct{})
	server := newModelListServer(t, func(_ string, _ bool) (int, string) {
		started <- struct{}{}
		<-proceed
		return http.StatusOK, `{"data":[]}`
	})
	id, _ := newRefreshChannel(t, server.URL, []model.ChannelKeyConfig{{Name: "key", Key: "placeholder", Enabled: true}})
	if _, err := RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{Automatic: true}); !errors.Is(err, ErrChannelRefreshStale) {
		t.Fatal("disabled automatic refresh ran", err)
	}
	if err := SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, "true"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = RefreshChannelModels(ctx, id, model.ChannelModelRefreshOptions{Automatic: true})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("automatic refresh not started")
	}
	busy, err := RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if err != nil || busy.Error != ErrChannelRefreshInProgress.Error() {
		t.Fatal("duplicate refresh accepted")
	}
	if !ChannelRefreshStatus(id).Running || ChannelRefreshStatus(id).Last != nil {
		t.Fatal("busy trigger replaced status")
	}
	cancel()
	close(proceed)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled refresh did not finish")
	}
	if ChannelRefreshStatus(id).Running {
		t.Fatal("running flag leaked")
	}
	_ = SettingSetString(model.SettingKeyChannelAutoUpdateEnabled, "false")
	result, err := RefreshChannelModels(t.Context(), id, model.ChannelModelRefreshOptions{})
	if err != nil || !result.Success {
		t.Fatalf("manual refresh disabled: %+v %v", result, err)
	}
}
