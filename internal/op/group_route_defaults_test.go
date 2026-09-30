package op

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

var defaultsTestID atomic.Int64

func keepRouteDefaults(t *testing.T) model.GroupRelayConfig {
	t.Helper()
	config, err := GroupRouteDefaultsGet()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, _, err := GroupRouteDefaultsSet(config, false, context.Background()); err != nil {
			t.Error(err)
		}
	})
	return config
}

func newDefaultsGroup(t *testing.T, mode model.GroupMode, config model.GroupRelayConfig) model.Group {
	t.Helper()
	name := fmt.Sprintf("defaults-%d", defaultsTestID.Add(1))
	group, err := GroupCreate(&model.GroupCreateRequest{Name: name, Mode: mode, RelayConfig: config}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return *group
}

func rawGroupConfig(t *testing.T, id int) string {
	t.Helper()
	var raw string
	if err := db.GetDB().Model(&model.Group{}).Select("relay_config").Where("id = ?", id).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRouteDefaultsOnlyAffectNewGroupsWithoutApply(t *testing.T) {
	config := keepRouteDefaults(t)
	existing := newDefaultsGroup(t, model.GroupModeManual, config)
	legacyJSON := `{ "member_retry_interval_seconds": 7, "member_affinity_seconds": 300 }`
	if err := db.GetDB().Model(&model.Group{}).Where("id = ?", existing.ID).Update("relay_config", legacyJSON).Error; err != nil {
		t.Fatal(err)
	}
	if err := groupRefreshCache(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := rawGroupConfig(t, existing.ID)
	config.MemberRetryIntervalSeconds = 17
	config.ManualMaxAttempts = 5
	config.MemberAffinitySeconds = 900
	saved, changes, err := GroupRouteDefaultsSet(config, false, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 || rawGroupConfig(t, existing.ID) != before || saved.MemberAffinitySeconds != 0 {
		t.Fatal("仅保存默认值改写了已有分组 JSON 或未清零 affinity")
	}
	inherited := newDefaultsGroup(t, model.GroupModeManual, model.GroupRelayConfig{})
	if inherited.RelayConfig != saved {
		t.Fatal("未指定配置的新分组没有继承全局默认")
	}
	explicit := model.DefaultGroupRelayConfig()
	explicit.MemberRetryIntervalSeconds = 9
	created := newDefaultsGroup(t, model.GroupModeFailover, explicit)
	if created.RelayConfig != explicit {
		t.Fatal("全局默认覆盖了显式创建配置")
	}
}

func TestApplyRouteDefaultsPreservesModeMembersAndInactiveFields(t *testing.T) {
	config := keepRouteDefaults(t)
	config.MemberMaxAttempts, config.MemberCooldownSeconds, config.ManualMaxAttempts = 9, 77, 13
	manual := newDefaultsGroup(t, model.GroupModeManual, config)
	failover := newDefaultsGroup(t, model.GroupModeFailover, config)
	channel, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: fmt.Sprintf("defaults-channel-%d", defaultsTestID.Add(1)), BaseURL: "http://127.0.0.1", Enabled: true},
		Keys:          []model.ChannelKeyConfig{{Name: "test", Key: "fixture", Enabled: true}}, Models: []string{"first", "second"},
		Grants: []model.ChannelGrantConfig{{ModelName: "first", KeyName: "test", Protocols: model.ProtocolOpenAIChatCompletion}, {ModelName: "second", KeyName: "test", Protocols: model.ProtocolOpenAIChatCompletion}},
	}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var items []model.GroupItemInput
	for _, grant := range ChannelGrantCandidates() {
		if grant.ChannelID == channel.ID {
			items = append(items, model.GroupItemInput{ChannelGrantID: grant.ID})
		}
	}
	for _, id := range []int{manual.ID, failover.ID} {
		group, err := GroupUpdate(id, &model.GroupUpdateRequest{Items: &items}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		selected := group.Items[1].ID
		group, err = GroupUpdate(id, &model.GroupUpdateRequest{ActiveItemID: &selected}, t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if id == manual.ID {
			manual = *group
		} else {
			failover = *group
		}
	}
	defaults := model.DefaultGroupRelayConfig()
	defaults.MemberRetryIntervalSeconds, defaults.ManualMaxAttempts = 19, 3
	defaults.MemberMaxAttempts, defaults.MemberCooldownSeconds = 4, 29
	if _, _, err := GroupRouteDefaultsSet(defaults, true, t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, before := range []model.Group{manual, failover} {
		after, err := GroupGet(before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.Name != before.Name || after.Mode != before.Mode || after.ActiveItemID != before.ActiveItemID || !reflect.DeepEqual(after.Items, before.Items) {
			t.Fatal("应用策略改变了名称、模式、成员顺序或当前选择")
		}
		if after.RelayConfig.MemberRetryIntervalSeconds != 19 {
			t.Fatal("公共策略没有更新")
		}
		if before.Mode == model.GroupModeManual {
			if after.RelayConfig.ManualMaxAttempts != 3 || after.RelayConfig.MemberMaxAttempts != 9 || after.RelayConfig.MemberCooldownSeconds != 77 {
				t.Fatal("手动模式没有选择性更新参数")
			}
		} else if after.RelayConfig.ManualMaxAttempts != 13 || after.RelayConfig.MemberMaxAttempts != 4 || after.RelayConfig.MemberCooldownSeconds != 29 {
			t.Fatal("故障转移模式没有选择性更新参数")
		}
	}
}

func TestApplyRouteDefaultsRollsBackSettingsGroupsAndCaches(t *testing.T) {
	baseline := keepRouteDefaults(t)
	first := newDefaultsGroup(t, model.GroupModeManual, baseline)
	second := newDefaultsGroup(t, model.GroupModeFailover, baseline)
	firstRaw, secondRaw := rawGroupConfig(t, first.ID), rawGroupConfig(t, second.ID)
	callback := "test:fail_route_defaults"
	if err := db.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if group, ok := tx.Statement.Dest.(*model.Group); ok && group.ID == second.ID {
			tx.AddError(errors.New("scripted group update failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Update().Remove(callback) })
	changed := baseline
	changed.MemberRetryIntervalSeconds += 11
	_, changes, err := GroupRouteDefaultsSet(changed, true, t.Context())
	_ = db.GetDB().Callback().Update().Remove(callback)
	if err == nil || len(changes) != 0 {
		t.Fatal("中途失败没有返回错误或泄漏了成功变更")
	}
	if rawGroupConfig(t, first.ID) != firstRaw || rawGroupConfig(t, second.ID) != secondRaw {
		t.Fatal("失败事务没有回滚分组 JSON")
	}
	saved, err := GroupRouteDefaultsGet()
	if err != nil || saved != baseline {
		t.Fatal("失败事务更新了默认配置缓存")
	}
	var stored model.Setting
	if err := db.GetDB().First(&stored, "key = ?", model.SettingKeyGroupRouteDefaults).Error; err != nil {
		t.Fatal(err)
	}
	persisted, err := parseGroupRouteDefaults(stored.Value)
	if err != nil || persisted != baseline {
		t.Fatal("失败事务没有回滚默认 Setting")
	}
	cached, _ := GroupGet(first.ID)
	if cached.RelayConfig != first.RelayConfig {
		t.Fatal("失败事务更新了分组缓存")
	}
}

func TestRouteDefaultsAndSingleUpdateKeepDatabaseAndCacheOrdered(t *testing.T) {
	baseline := keepRouteDefaults(t)
	group := newDefaultsGroup(t, model.GroupModeManual, baseline)
	entered, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	callback := "test:block_route_defaults"
	if err := db.GetDB().Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*model.Group); ok && row.ID == group.ID && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.GetDB().Callback().Update().Remove(callback) })
	bulkConfig := baseline
	bulkConfig.MemberRetryIntervalSeconds += 8
	bulkDone := make(chan error, 1)
	go func() { _, _, err := GroupRouteDefaultsSet(bulkConfig, true, t.Context()); bulkDone <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("批量事务未到达握手点")
	}
	singleConfig := baseline
	singleConfig.MemberRetryIntervalSeconds += 18
	singleDone := make(chan error, 1)
	go func() {
		_, err := GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &singleConfig}, t.Context())
		singleDone <- err
	}()
	close(release)
	for _, done := range []chan error{bulkDone, singleDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("并发配置更新没有完成")
		}
	}
	cached, _ := GroupGet(group.ID)
	var stored model.Group
	if err := db.GetDB().First(&stored, group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if cached.RelayConfig != singleConfig || stored.RelayConfig != singleConfig {
		t.Fatal("后提交的单组更新被旧缓存覆盖")
	}
}

func TestRouteDefaultsBackupAndInvalidWrites(t *testing.T) {
	baseline := keepRouteDefaults(t)
	custom := baseline
	custom.MemberRetryIntervalSeconds += 7
	if _, _, err := GroupRouteDefaultsSet(custom, false, t.Context()); err != nil {
		t.Fatal(err)
	}
	exported, err := DBExportAll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var setting model.Setting
	for _, row := range exported.Settings {
		if row.Key == model.SettingKeyGroupRouteDefaults {
			setting = row
		}
	}
	if setting.Value == "" {
		t.Fatal("导出缺少默认策略 Setting")
	}
	if _, _, err := GroupRouteDefaultsSet(baseline, false, t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: exported.Version, Settings: []model.Setting{setting}}); err != nil {
		t.Fatal(err)
	}
	if config, err := GroupRouteDefaultsGet(); err != nil || config != custom {
		t.Fatal("新备份导入没有恢复默认策略")
	}
	if _, err := DBImportIncremental(t.Context(), &model.DBDump{Version: legacyDBDumpVersion}); err != nil {
		t.Fatal("不含默认策略的旧备份不能导入", err)
	}
	if config, _ := GroupRouteDefaultsGet(); config != custom {
		t.Fatal("旧备份缺键时覆盖了已有默认策略")
	}
	for _, invalid := range []string{"null", "[]", "{broken", `{"manual_max_attempts":-1}`, `{"member_cooldown_seconds":-1}`, `{"retry_max_interval_seconds":601}`, `{"unknown":1}`} {
		if _, err := DBImportIncremental(t.Context(), &model.DBDump{Settings: []model.Setting{{Key: model.SettingKeyGroupRouteDefaults, Value: invalid}}}); err == nil {
			t.Fatalf("备份导入接受非法默认策略 %q", invalid)
		}
	}
	if err := SettingSetString(model.SettingKeyGroupRouteDefaults, setting.Value); err == nil {
		t.Fatal("通用字符串 setter 绕过了专用默认策略接口")
	}
	if err := SettingSetInt(model.SettingKeyGroupRouteDefaults, 1); err == nil {
		t.Fatal("通用整数 setter 绕过了专用默认策略接口")
	}
	invalidConfig := custom
	invalidConfig.ManualMaxAttempts = -1
	if _, _, err := GroupRouteDefaultsSet(invalidConfig, true, t.Context()); err == nil {
		t.Fatal("默认策略没有同时校验手动参数")
	}
	invalidConfig = custom
	invalidConfig.MemberCooldownSeconds = -1
	if _, _, err := GroupRouteDefaultsSet(invalidConfig, true, t.Context()); err == nil {
		t.Fatal("默认策略没有同时校验故障转移参数")
	}
	var roundTrip model.GroupRelayConfig
	if err := json.Unmarshal([]byte(setting.Value), &roundTrip); err != nil || roundTrip != custom {
		t.Fatal("备份默认策略不再是兼容的扁平 JSON")
	}
}
