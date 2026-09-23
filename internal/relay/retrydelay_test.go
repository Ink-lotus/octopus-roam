package relay

// 本文件锁定退避函数 RetryDelay 的取值: 固定模式给出固定间隔, 指数模式按 2 倍递增并受最大间隔约束。
// retryCount 以 1 表示首次重试前的等待, 即"第 N 次尝试失败后等待的就是 RetryDelay(config, N)"。

import (
	"math"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestRetryDelayFixedMode 锁定固定模式: 无论第几次重试都返回 member_retry_interval_seconds。
func TestRetryDelayFixedMode(t *testing.T) {
	config := model.GroupRelayConfig{
		RetryMode:                  model.RetryModeFixed,
		MemberRetryIntervalSeconds: 3,
	}
	// 边界取值一并覆盖: 首次重试, 常规重试与极大的重试次数都不改变固定间隔。
	for _, retryCount := range []int{1, 2, 5, 1000, math.MaxInt} {
		if got := RetryDelay(config, retryCount); got != 3*time.Second {
			t.Fatalf("retryCount=%d 的固定间隔 = %v, 期望 3s", retryCount, got)
		}
	}
}

// TestRetryDelayExponentialSequence 锁定默认参数下的指数序列: 1, 2, 4, 8 ... 直到 600 秒封顶并保持。
func TestRetryDelayExponentialSequence(t *testing.T) {
	config := model.GroupRelayConfig{
		RetryMode:                   model.RetryModeExponential,
		RetryInitialIntervalSeconds: model.RetryInitialIntervalSecondsDefault,
		RetryMaxIntervalSeconds:     model.RetryMaxIntervalSecondsLimit,
	}
	want := []time.Duration{
		1, 2, 4, 8, 16, 32, 64, 128, 256, 512, // 第 1..10 次重试
		600, 600, 600, // 第 11 次起超过上限, 保持上限值
	}
	for i, seconds := range want {
		retryCount := i + 1
		if got := RetryDelay(config, retryCount); got != seconds*time.Second {
			t.Fatalf("第 %d 次重试的等待 = %v, 期望 %v", retryCount, got, seconds*time.Second)
		}
	}
}

// TestRetryDelayExponentialUsesConfiguredIntervals 锁定指数模式采用配置给出的初始间隔与最大间隔。
func TestRetryDelayExponentialUsesConfiguredIntervals(t *testing.T) {
	config := model.GroupRelayConfig{
		RetryMode:                   model.RetryModeExponential,
		RetryInitialIntervalSeconds: 2,
		RetryMaxIntervalSeconds:     10,
	}
	want := []time.Duration{2, 4, 8, 10, 10}
	for i, seconds := range want {
		retryCount := i + 1
		if got := RetryDelay(config, retryCount); got != seconds*time.Second {
			t.Fatalf("第 %d 次重试的等待 = %v, 期望 %v", retryCount, got, seconds*time.Second)
		}
	}
}

// TestRetryDelayExponentialHandlesHugeRetryCount 锁定超大重试次数不产生溢出:
// 等待时间恒为最大间隔, 既不为负也不回落到很小的值。
func TestRetryDelayExponentialHandlesHugeRetryCount(t *testing.T) {
	config := model.GroupRelayConfig{
		RetryMode:                   model.RetryModeExponential,
		RetryInitialIntervalSeconds: model.RetryInitialIntervalSecondsDefault,
		RetryMaxIntervalSeconds:     model.RetryMaxIntervalSecondsLimit,
	}
	for _, retryCount := range []int{62, 63, 64, 1024, math.MaxInt32, math.MaxInt} {
		got := RetryDelay(config, retryCount)
		if got != model.RetryMaxIntervalSecondsLimit*time.Second {
			t.Fatalf("retryCount=%d 的等待 = %v, 期望上限 %ds", retryCount, got, model.RetryMaxIntervalSecondsLimit)
		}
	}
}

// TestRetryDelayExponentialClampsHugeInitialInterval 锁定超大初始间隔不会在换算中溢出:
// time.Duration 是 int64 纳秒, 秒数过大时"先换算再与上限比较"会在乘法里回绕成负数或零,
// 使退避退化成无等待的立即重试; 首次等待本身不得超过最大间隔, 故这类取值一律按最大间隔处理。
func TestRetryDelayExponentialClampsHugeInitialInterval(t *testing.T) {
	// 取值经 platformInt 收口: 32 位平台的 int 装不下这些字面量, 收口后仍远超最大间隔, 覆盖同一分支。
	cases := []struct {
		name    string
		initial int
	}{
		{"换算后为负数", platformInt(9223372037)},       // 9223372037 * 1e9 超出 int64 上限
		{"换算后为零", platformInt(36028797018963968)}, // 回绕后恰好落在 0
		{"int 最大值", math.MaxInt},                   //
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := model.GroupRelayConfig{
				RetryMode:                   model.RetryModeExponential,
				RetryInitialIntervalSeconds: testCase.initial,
				RetryMaxIntervalSeconds:     model.RetryMaxIntervalSecondsLimit,
			}
			for _, retryCount := range []int{1, 2, 3, math.MaxInt} {
				got := RetryDelay(config, retryCount)
				if got <= 0 {
					t.Fatalf("初始间隔 %d 的第 %d 次重试等待 = %v, 必须为正, 否则会立即重试而失去退避保护", testCase.initial, retryCount, got)
				}
				if got > model.RetryMaxIntervalSecondsLimit*time.Second {
					t.Fatalf("初始间隔 %d 的第 %d 次重试等待 = %v, 超出 %d 秒硬上限", testCase.initial, retryCount, got, model.RetryMaxIntervalSecondsLimit)
				}
			}
		})
	}
}

// TestRetryDelayFixedModeClampsHugeInterval 锁定超大固定间隔同样不会回绕:
// 固定间隔字段沿用既有语义, 故不按最大间隔收窄, 但换算不得溢出成负数或零。
func TestRetryDelayFixedModeClampsHugeInterval(t *testing.T) {
	for _, seconds := range []int{platformInt(9223372037), platformInt(36028797018963968), math.MaxInt} {
		config := model.GroupRelayConfig{RetryMode: model.RetryModeFixed, MemberRetryIntervalSeconds: seconds}
		if got := RetryDelay(config, 1); got <= 0 {
			t.Fatalf("固定间隔 %d 的等待 = %v, 必须为正, 否则会立即重试而失去退避保护", seconds, got)
		}
	}
}

// TestRetryDelayUnnormalizedConfigStaysInBounds 锁定未经 Normalize 的配置也安全:
// 空配置与零值参数下等待不为零, 不会退化成无等待的忙循环。
func TestRetryDelayUnnormalizedConfigStaysInBounds(t *testing.T) {
	// 零值配置: 重试模式为空且固定间隔为 0, 相当于直接取自数据库的旧数据。
	if got := RetryDelay(model.GroupRelayConfig{}, 1); got < time.Second {
		t.Fatalf("空配置的等待 = %v, 期望至少 1s 以免忙循环", got)
	}
	// 指数模式下初始间隔为 0: 同样必须落在 1 秒到硬上限之间。
	exponential := model.GroupRelayConfig{RetryMode: model.RetryModeExponential}
	for _, retryCount := range []int{1, 10, math.MaxInt} {
		got := RetryDelay(exponential, retryCount)
		if got < time.Second || got > model.RetryMaxIntervalSecondsLimit*time.Second {
			t.Fatalf("零值指数配置 retryCount=%d 的等待 = %v, 期望落在 1s 与 %ds 之间", retryCount, got, model.RetryMaxIntervalSecondsLimit)
		}
	}
}

// TestRetryDelayTreatsNonPositiveRetryCountAsFirstRetry 锁定非正的重试序号按首次重试处理。
func TestRetryDelayTreatsNonPositiveRetryCountAsFirstRetry(t *testing.T) {
	config := model.GroupRelayConfig{
		RetryMode:                   model.RetryModeExponential,
		RetryInitialIntervalSeconds: 4,
		RetryMaxIntervalSeconds:     model.RetryMaxIntervalSecondsLimit,
	}
	first := RetryDelay(config, 1)
	for _, retryCount := range []int{0, -1} {
		if got := RetryDelay(config, retryCount); got != first {
			t.Fatalf("retryCount=%d 的等待 = %v, 期望与首次重试一致 %v", retryCount, got, first)
		}
	}
}
