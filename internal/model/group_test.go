package model

// 本文件锁定 GroupRelayConfig 的默认值, 旧配置补默认与新增字段的边界:
// 新增的重试模式, 退避参数与手动上限都是持久化 JSON 的一部分, 旧分组必须无需人工修改即可继续运行。

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
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
