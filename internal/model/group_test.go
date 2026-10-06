package model

// 本文件锁定 GroupRelayConfig 的默认值, 旧配置补默认与新增字段的边界:
// 新增的重试模式, 退避参数与手动上限都是持久化 JSON 的一部分, 旧分组必须无需人工修改即可继续运行。

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// platformInt 把 int64 取值收进当前平台的 int 范围, 供测试书写"远超配置上限"的取值。
// 32 位平台的 int 装不下 9223372037 这类字面量(编译期即报溢出错, 测试根本无法构建),
// 故先用 int64 承载再按位宽在运行时收口; 收口后的取值仍远大于配置上限, 覆盖同一分支。
func platformInt(v int64) int {
	if strconv.IntSize == 32 && v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(v)
}

// TestDefaultGroupRelayConfigPinsRetryDefaults 锁定新分组使用的重试默认值:
// 默认固定模式并沿用既有 3 秒间隔, 指数参数取 1 秒起, 600 秒封顶, 手动上限 0 表示不限额。
func TestDefaultGroupRelayConfigPinsRetryDefaults(t *testing.T) {
	defaults := DefaultGroupRelayConfig()

	if defaults.RetryMode != RetryModeFixed {
		t.Fatalf("默认重试模式 = %q, 期望 %q", defaults.RetryMode, RetryModeFixed)
	}
	if defaults.MemberRetryIntervalSeconds != 3 {
		t.Fatalf("默认固定重试间隔 = %d, 期望 3", defaults.MemberRetryIntervalSeconds)
	}
	if defaults.RetryInitialIntervalSeconds != 1 {
		t.Fatalf("默认指数初始间隔 = %d, 期望 1", defaults.RetryInitialIntervalSeconds)
	}
	if defaults.RetryMaxIntervalSeconds != 600 {
		t.Fatalf("默认指数最大间隔 = %d, 期望 600", defaults.RetryMaxIntervalSeconds)
	}
	if defaults.ManualMaxAttempts != 0 {
		t.Fatalf("默认手动上限 = %d, 期望 0 (不限额)", defaults.ManualMaxAttempts)
	}
}

// TestNormalizeZeroRelayConfigUsesDefaults 锁定空配置整体取默认值。
func TestNormalizeZeroRelayConfigUsesDefaults(t *testing.T) {
	var config GroupRelayConfig
	NormalizeGroupRelayConfig(&config)

	if config != DefaultGroupRelayConfig() {
		t.Fatalf("空配置补齐结果 = %+v, 期望取默认值 %+v", config, DefaultGroupRelayConfig())
	}
}

// TestNormalizeLegacyRelayConfigBackfillsNewFields 锁定旧分组 JSON 的读取兼容:
// 只有既有字段的配置补齐重试模式, 退避参数与手动上限, 既有字段原样保留。
func TestNormalizeLegacyRelayConfigBackfillsNewFields(t *testing.T) {
	legacy := `{"member_max_attempts":5,"member_retry_interval_seconds":7,` +
		`"member_non_stream_response_timeout_seconds":90,"member_stream_first_event_timeout_seconds":45,` +
		`"member_cooldown_seconds":30,"member_affinity_seconds":0}`

	var config GroupRelayConfig
	if err := json.Unmarshal([]byte(legacy), &config); err != nil {
		t.Fatalf("旧分组 JSON 反序列化失败: %v", err)
	}
	NormalizeGroupRelayConfig(&config)

	want := GroupRelayConfig{
		MemberMaxAttempts:                     5,
		MemberRetryIntervalSeconds:            7,
		MemberNonStreamResponseTimeoutSeconds: 90,
		MemberStreamFirstEventTimeoutSeconds:  45,
		MemberCooldownSeconds:                 30,
		MemberAffinitySeconds:                 0,
		RetryMode:                             RetryModeFixed,
		RetryInitialIntervalSeconds:           1,
		RetryMaxIntervalSeconds:               600,
		ManualMaxAttempts:                     0,
	}
	if config != want {
		t.Fatalf("旧配置补齐结果 = %+v, 期望 %+v", config, want)
	}
}

// TestNormalizeGroupRelayConfigBounds 锁定新增字段的边界:
// 非法重试模式回落固定模式, 指数参数与手动上限被夹到各自合法区间。
func TestNormalizeGroupRelayConfigBounds(t *testing.T) {
	cases := []struct {
		name       string
		in         GroupRelayConfig
		wantMode   RetryMode
		wantInit   int
		wantMax    int
		wantManual int
	}{
		{"非法重试模式回落固定", GroupRelayConfig{RetryMode: "linear"}, RetryModeFixed, 1, 600, 0},
		{"指数模式被保留", GroupRelayConfig{RetryMode: RetryModeExponential}, RetryModeExponential, 1, 600, 0},
		{"固定模式被保留", GroupRelayConfig{RetryMode: RetryModeFixed}, RetryModeFixed, 1, 600, 0},
		{"初始间隔为零补 1", GroupRelayConfig{RetryMode: RetryModeExponential}, RetryModeExponential, 1, 600, 0},
		{"初始间隔为负补 1", GroupRelayConfig{RetryMode: RetryModeExponential, RetryInitialIntervalSeconds: -5}, RetryModeExponential, 1, 600, 0},
		{"初始间隔在范围内保留", GroupRelayConfig{RetryMode: RetryModeExponential, RetryInitialIntervalSeconds: 7}, RetryModeExponential, 7, 600, 0},
		{"初始间隔超上限夹到 600", GroupRelayConfig{RetryMode: RetryModeExponential, RetryInitialIntervalSeconds: 5000}, RetryModeExponential, 600, 600, 0},
		{"初始间隔为超大值夹到 600", GroupRelayConfig{RetryMode: RetryModeExponential, RetryInitialIntervalSeconds: platformInt(9223372037)}, RetryModeExponential, 600, 600, 0},
		{"最大间隔为零补 600", GroupRelayConfig{RetryMode: RetryModeExponential, RetryMaxIntervalSeconds: 0}, RetryModeExponential, 1, 600, 0},
		{"最大间隔超上限夹到 600", GroupRelayConfig{RetryMode: RetryModeExponential, RetryMaxIntervalSeconds: 5000}, RetryModeExponential, 1, 600, 0},
		{"最大间隔在范围内保留", GroupRelayConfig{RetryMode: RetryModeExponential, RetryMaxIntervalSeconds: 30}, RetryModeExponential, 1, 30, 0},
		{"手动上限为负归零", GroupRelayConfig{ManualMaxAttempts: -3}, RetryModeFixed, 1, 600, 0},
		{"手动上限为正保留", GroupRelayConfig{ManualMaxAttempts: 5}, RetryModeFixed, 1, 600, 5},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := testCase.in
			NormalizeGroupRelayConfig(&config)

			if config.RetryMode != testCase.wantMode {
				t.Fatalf("重试模式 = %q, 期望 %q", config.RetryMode, testCase.wantMode)
			}
			if config.RetryInitialIntervalSeconds != testCase.wantInit {
				t.Fatalf("初始间隔 = %d, 期望 %d", config.RetryInitialIntervalSeconds, testCase.wantInit)
			}
			if config.RetryMaxIntervalSeconds != testCase.wantMax {
				t.Fatalf("最大间隔 = %d, 期望 %d", config.RetryMaxIntervalSeconds, testCase.wantMax)
			}
			if config.ManualMaxAttempts != testCase.wantManual {
				t.Fatalf("手动上限 = %d, 期望 %d", config.ManualMaxAttempts, testCase.wantManual)
			}
		})
	}
}

// TestGroupRelayConfigJSONFieldNames 锁定新增字段的 JSON 名称:
// 它们既随分组配置持久化, 也随备份导出与 API 出入, 改名即破坏兼容。
func TestGroupRelayConfigJSONFieldNames(t *testing.T) {
	raw, err := json.Marshal(GroupRelayConfig{})
	if err != nil {
		t.Fatalf("序列化分组 Relay 配置失败: %v", err)
	}
	for _, name := range []string{
		"retry_mode",
		"retry_initial_interval_seconds",
		"retry_max_interval_seconds",
		"manual_max_attempts",
		"member_retry_interval_seconds",
		"member_max_attempts",
		"member_affinity_seconds",
	} {
		if !strings.Contains(string(raw), `"`+name+`"`) {
			t.Fatalf("分组 Relay 配置的 JSON 缺少字段 %s: %s", name, raw)
		}
	}
}

func TestMatchGroupModelNameModes(t *testing.T) {
	tests := []struct {
		name      string
		mode      GroupModelMatchMode
		groupName string
		modelName string
		want      bool
	}{
		{name: "keyword includes suffix", mode: GroupModelMatchModeKeyword, groupName: "claude-opus-5", modelName: "claude-opus-5-5", want: true},
		{name: "keyword ignores case", mode: GroupModelMatchModeKeyword, groupName: "Claude-Opus-5", modelName: "claude-opus-5-5", want: true},
		{name: "keyword trims group name", mode: GroupModelMatchModeKeyword, groupName: "  claude-opus-5  ", modelName: "claude-opus-5-5", want: true},
		{name: "exact rejects suffix", mode: GroupModelMatchModeExact, groupName: "claude-opus-5", modelName: "claude-opus-5-5", want: false},
		{name: "exact ignores case and trims both names", mode: GroupModelMatchModeExact, groupName: "  Claude-Opus-5 ", modelName: "claude-opus-5  ", want: true},
		{name: "empty group name does not match", mode: GroupModelMatchModeKeyword, groupName: "  ", modelName: "anything", want: false},
		{name: "unknown mode does not match", mode: "prefix", groupName: "claude", modelName: "claude-opus-5", want: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := MatchGroupModelName(testCase.mode, testCase.groupName, testCase.modelName); got != testCase.want {
				t.Fatalf("MatchGroupModelName(%q, %q, %q) = %v, 期望 %v", testCase.mode, testCase.groupName, testCase.modelName, got, testCase.want)
			}
		})
	}
}

func TestGroupModelMatchModeValidationAndDefaults(t *testing.T) {
	if got := NormalizeGroupModelMatchMode(""); got != GroupModelMatchModeKeyword {
		t.Fatalf("旧分组匹配模式默认 = %q, 期望 %q", got, GroupModelMatchModeKeyword)
	}
	for _, mode := range []GroupModelMatchMode{GroupModelMatchModeKeyword, GroupModelMatchModeExact} {
		if err := ValidateGroupModelMatchMode(mode); err != nil {
			t.Errorf("合法匹配模式 %q 被拒绝: %v", mode, err)
		}
	}
	if err := ValidateGroupModelMatchMode("prefix"); err == nil {
		t.Fatal("接受了未知匹配模式")
	}
}

func TestAutoMigrateAddsGroupMatchModeWithoutLosingItems(t *testing.T) {
	legacyDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "legacy-groups.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开临时 SQLite 数据库失败: %v", err)
	}
	sqlDB, err := legacyDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := legacyDB.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.AutoMigrate(&Group{}); err != nil {
		t.Fatalf("创建旧分组表失败: %v", err)
	}
	if err := legacyDB.Exec("CREATE TABLE channel_grants (id integer PRIMARY KEY, channel_model_id integer NOT NULL, channel_key_id integer NOT NULL, protocols integer NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.AutoMigrate(&GroupItem{}); err != nil {
		t.Fatalf("创建旧分组成员表失败: %v", err)
	}
	// 删除刚创建的列以模拟升级前 schema, 再放入真实的分组成员引用。
	if err := legacyDB.Exec(`ALTER TABLE groups DROP COLUMN model_match_mode`).Error; err != nil {
		t.Fatalf("模拟旧 groups 表失败: %v", err)
	}
	if err := legacyDB.Exec(`INSERT INTO groups (id, name, mode, active_item_id, relay_config) VALUES (1, 'legacy', 'manual', 0, '{}')`).Error; err != nil {
		t.Fatal(err)
	}
	// 该兼容测试只关心 group_items 对授权主键的保留, 不建立渠道模型/凭据的完整旧表。
	if err := legacyDB.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Exec(`INSERT INTO channel_grants (id, channel_model_id, channel_key_id, protocols) VALUES (7, 1, 1, 2)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Exec(`INSERT INTO group_items (id, group_id, channel_grant_id, priority) VALUES (11, 1, 7, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := legacyDB.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
		t.Fatal(err)
	}

	if err := legacyDB.AutoMigrate(&Group{}, &GroupItem{}); err != nil {
		t.Fatalf("升级旧分组 schema 失败: %v", err)
	}
	var group Group
	if err := legacyDB.First(&group, 1).Error; err != nil {
		t.Fatal(err)
	}
	if group.ModelMatchMode != GroupModelMatchModeKeyword {
		t.Fatalf("旧分组列默认值 = %q, 期望 %q", group.ModelMatchMode, GroupModelMatchModeKeyword)
	}
	var itemCount int64
	if err := legacyDB.Model(&GroupItem{}).Where("group_id = ?", group.ID).Count(&itemCount).Error; err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 {
		t.Fatalf("AutoMigrate 后分组成员数 = %d, 期望保留 1 条", itemCount)
	}
}

func TestRelayConfigValidatesOnlyCurrentModeAndNormalizesAffinity(t *testing.T) {
	config := DefaultGroupRelayConfig()
	config.MemberMaxAttempts, config.MemberCooldownSeconds = -3, -8
	config.MemberAffinitySeconds = 300
	if err := ValidateGroupRelayConfig(config, GroupModeManual); err != nil {
		t.Fatalf("手动模式被非生效字段阻止: %v", err)
	}
	if err := ValidateGroupRelayConfig(config, GroupModeFailover); err == nil {
		t.Fatal("故障转移模式接受了负数预算或冷却")
	}
	NormalizeGroupRelayConfigForMode(&config, GroupModeManual)
	if config.MemberMaxAttempts != -3 || config.MemberCooldownSeconds != -8 || config.MemberAffinitySeconds != 0 {
		t.Fatalf("未保留另一模式参数或未清零兼容字段: %+v", config)
	}
	changed := config
	changed.MemberCooldownSeconds = 99
	if EffectiveGroupRelayConfig(config, GroupModeManual) != EffectiveGroupRelayConfig(changed, GroupModeManual) {
		t.Fatal("手动有效策略包含冷却")
	}
	changed.ManualMaxAttempts = 1
	if EffectiveGroupRelayConfig(config, GroupModeManual) == EffectiveGroupRelayConfig(changed, GroupModeManual) {
		t.Fatal("手动有效策略忽略了手动预算")
	}
	config = DefaultGroupRelayConfig()
	config.ManualMaxAttempts = -1
	if err := ValidateGroupRelayConfig(config, GroupModeFailover); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGroupRelayConfig(config, GroupModeManual); err == nil {
		t.Fatal("手动模式接受了负数预算")
	}
	NormalizeGroupRelayConfigForMode(&config, GroupModeFailover)
	if config.ManualMaxAttempts != -1 || EffectiveGroupRelayConfig(config, GroupModeFailover).ManualMaxAttempts != 0 {
		t.Fatal("故障转移未保留或隔离手动预算")
	}
	config.MemberNonStreamResponseTimeoutSeconds = -1
	for _, mode := range []GroupMode{GroupModeManual, GroupModeFailover} {
		if err := ValidateGroupRelayConfig(config, mode); err == nil {
			t.Fatalf("%s 接受负数公共超时", mode)
		}
	}
}
