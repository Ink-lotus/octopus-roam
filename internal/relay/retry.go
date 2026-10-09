package relay

import (
	"math"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
)

// maxWaitSeconds 是换算为 time.Duration 之前允许的最大秒数。
// time.Duration 是 int64 纳秒, 秒数超过该值时乘法会回绕, 得到负数或零, 使等待退化成无等待的立即重试。
const maxWaitSeconds = int64(math.MaxInt64) / int64(time.Second)

// secondsToDuration 把秒数换算为等待时长, 超出可表示范围的秒数饱和到最长可表示时长。
// 判断必须在换算之前: 回绕发生在乘法里, 换算之后再比较已经无效。
// 取饱和而非取余, 以免"超长等待"被折成"立即重试"。
func secondsToDuration(seconds int) time.Duration {
	if int64(seconds) > maxWaitSeconds {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(seconds) * time.Second
}

// RetryDelay 返回同一成员第 retryCount 次重试之前的等待时间, retryCount 为 1 表示首次重试前的等待,
// 即"第 N 次尝试失败后等待的是 RetryDelay(config, N)"。
// 固定模式恒取 member_retry_interval_seconds; 指数模式取 min(初始间隔 * 2^(retryCount-1), 最大间隔)。
// 配置可能来自尚未经过 NormalizeGroupRelayConfig 的旧数据, 故此处自行夹住取值:
// 等待至少 1 秒以免退化成无等待的忙循环; 指数模式遵守配置的最大间隔, 换算及翻倍均不得回绕。
func RetryDelay(config model.GroupRelayConfig, retryCount int) time.Duration {
	if config.RetryMode == model.RetryModeExponential {
		return exponentialRetryDelay(config, retryCount)
	}
	// 空值或未知的重试模式按固定模式处理, 与 NormalizeGroupRelayConfig 的回落保持一致。
	// 固定间隔字段沿用既有语义, 故只防回绕, 不按最大间隔收窄。
	seconds := config.MemberRetryIntervalSeconds
	if seconds < 1 {
		seconds = 1
	}
	return secondsToDuration(seconds)
}

// exponentialRetryDelay 按固定倍数 2 递增等待时间, 并以配置的最大间隔封顶。
// 递增用循环而非左移: 重试次数可能大到使移位溢出, 而等待一旦达到上限即可停止递增,
// 故循环次数只取决于上限与初始间隔之比。
func exponentialRetryDelay(config model.GroupRelayConfig, retryCount int) time.Duration {
	initial := config.RetryInitialIntervalSeconds
	if initial < 1 {
		initial = model.RetryInitialIntervalSecondsDefault
	}
	limit := config.RetryMaxIntervalSeconds
	if limit < 1 {
		limit = model.RetryMaxIntervalSecondsDefault
	}

	// 首次等待本身不得超过最大间隔, 且在换算之前夹住: 超大的初始间隔会让换算回绕,
	// 回绕之后的取值与上限比较已经无意义, 反而可能得到负数或零。
	if initial > limit {
		initial = limit
	}

	delay := secondsToDuration(initial)
	ceiling := secondsToDuration(limit)
	for remaining := retryCount; remaining > 1 && delay < ceiling; remaining-- {
		// 先比较剩余空间, 再翻倍: 即使 ceiling 已饱和到 time.Duration 的最大值也不会溢出。
		if delay >= ceiling-delay {
			return ceiling
		}
		delay *= 2
	}
	return delay
}
