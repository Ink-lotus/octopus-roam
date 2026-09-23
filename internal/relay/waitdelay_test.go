package relay

// 本文件锁定 wait 的等待行为: 它把秒数换算成时长后真实阻塞, 且超大秒数不会因换算回绕而立即返回。
// RetryDelay 与 wait 必须做同一套安全换算: 前者算出等待时长, 后者把它落到定时器上;
// 只要有一处漏掉, "退避"就会在那一类路径上退化成无等待的快速重轮。

import (
	"context"
	"testing"
	"time"
)

// TestWaitBlocksForConfiguredSeconds 锁定 wait 真正等待配置的时长之后才返回 true。
func TestWaitBlocksForConfiguredSeconds(t *testing.T) {
	// 只用到等待与取消定稿, 故不登记到请求表, 也不占请求 ID。
	request := &RequestState{}

	start := time.Now()
	if ok := request.wait(context.Background(), 1); !ok {
		t.Fatal("正常等待返回 false, 期望 true")
	}
	// 宽松下界: 只证明它确实等了, 不锁定调度精度。
	if elapsed := time.Since(start); elapsed < 700*time.Millisecond {
		t.Fatalf("等待 1 秒实际耗时 %v, 期望至少 700ms", elapsed)
	}
}

// TestWaitSaturatesHugeIntervalAndStopsOnCancel 锁定超大的固定间隔不会让等待立即返回:
// 手动模式下"发送前配置错误"与"无可用成员"等路径都调用 wait, 而秒数在换算为 time.Duration
// (int64 纳秒) 时, 过大的取值会在乘法里回绕成负数或零, time.After 随即到期——退避失效。
// 同时锁定取消路径: 等待期间客户端断开时立即返回 false, 不再靠定时器到期。
func TestWaitSaturatesHugeIntervalAndStopsOnCancel(t *testing.T) {
	request := &RequestState{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan bool, 1)
	go func() { done <- request.wait(ctx, platformInt(9223372037)) }()

	select {
	case ok := <-done:
		t.Fatalf("超大固定间隔的等待立即返回 (ok=%v), 期望阻塞直到取消", ok)
	case <-time.After(300 * time.Millisecond):
	}

	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("取消后的等待返回 true, 期望 false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后等待未在 5 秒内返回")
	}
}
