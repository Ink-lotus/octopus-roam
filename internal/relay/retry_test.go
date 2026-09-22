package relay

// 本文件锁定 Relay 当前的重试基线: 固定间隔重试, 手动模式的持续重试, 以及取消时的退出。
// 这些行为是后续重试策略重构的对照物, 断言只依赖可观察结果: 上游请求次数与顺序, 等待是否发生,
// 最终 HTTP 状态与响应正文, 以及取消后是否还有新的上游请求。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestFixedRetrySucceedsAfterSecondAttempt 锁定固定间隔重试: 上游首次失败后按
// member_retry_interval_seconds 等待再试, 第二次成功即返回成功结果, 首次失败不暴露给客户端。
func TestFixedRetrySucceedsAfterSecondAttempt(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(2), grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()

	started := time.Now()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	elapsed := time.Since(started)

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "pong") {
		t.Fatalf("最终响应正文未包含上游成功内容: %s", body)
	}
	if got := upstream.requestCount(); got != 2 {
		t.Fatalf("上游请求次数 = %d, 期望 2 (首次失败 + 重试成功)", got)
	}
	want := []string{"member/upstream-model", "member/upstream-model"}
	if calls := log.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("上游调用记录 = %v, 期望 %v", calls, want)
	}
	// 两次尝试之间应当按配置等待: 间隔为 1 秒时下界取 700 毫秒, 只用于区分"等待过"与"立即忙循环",
	// 不锁定精确调度。
	if elapsed < 700*time.Millisecond {
		t.Fatalf("两次尝试仅耗时 %v, 未体现 member_retry_interval_seconds 的等待", elapsed)
	}
}

// TestManualRetryContinuesAfterRepeatedFailures 锁定手动模式的持续重试: 上游连续失败多次后仍继续请求,
// 成功即停止, 且请求次数超出 member_max_attempts 也不影响手动模式继续重试。
func TestManualRetryContinuesAfterRepeatedFailures(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 3)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	// member_max_attempts 为 2, 手动模式据此进入冷却; 手动模式下该配置不参与, 重试次数不受它限制。
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(2), grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()

	started := time.Now()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	elapsed := time.Since(started)

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "pong") {
		t.Fatalf("最终响应正文未包含上游成功内容: %s", body)
	}
	if got, want := upstream.requestCount(), 4; got != want {
		t.Fatalf("上游请求次数 = %d, 期望 %d (三次失败 + 一次成功)", got, want)
	}
	// 三次重试各等待 1 秒; 下界取 2 秒, 若未等待则该耗时只会在毫秒量级。
	if elapsed < 2*time.Second {
		t.Fatalf("四次尝试仅耗时 %v, 未体现三次按固定间隔的等待, 疑为立即忙循环", elapsed)
	}
}

// TestManualRetryStopsOnContextCancel 锁定手动模式在取消时的退出: 上游持续失败期间取消请求上下文后
// 转发及时结束, 且不再发起新的上游请求。
func TestManualRetryStopsOnContextCancel(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 64)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(2), grantID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newRelayEngine()
	req := chatRequest(t, groupName, false).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(httptest.NewRecorder(), req)
	}()

	// 上游第二次被调用说明首次失败后的等待与重试确已发生, 此时请求正处在重试循环中。
	upstream.awaitRequests(t, 2, 10*time.Second)
	before := upstream.requestCount()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("取消后转发未在 5 秒内结束, 上游已收到 %d 次请求", upstream.requestCount())
	}

	// 取消后若仍会发起新的上游请求, 次数会在下一次固定间隔到达时增长; 此处请求已经结束, 次数必须保持不变。
	if after := upstream.requestCount(); after != before {
		t.Fatalf("取消后上游请求次数由 %d 增长到 %d, 取消未能阻止新的上游请求", before, after)
	}
	select {
	case <-upstream.arrived:
		t.Fatal("取消后仍有新的上游请求到达")
	default:
	}
}
