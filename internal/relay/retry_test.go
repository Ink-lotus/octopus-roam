package relay

// 本文件锁定 Relay 的重试行为: 固定间隔重试, 手动模式的尝试上限, 指数退避, 以及取消时的退出。
// 断言只依赖可观察结果: 上游请求次数与顺序, 等待是否按配置发生, 最终 HTTP 状态与响应正文,
// 以及取消后是否还有新的上游请求。

import (
	"context"
	"encoding/json"
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

// TestManualRetryContinuesWhenAttemptLimitIsZero 锁定手动模式不限额时的持续重试:
// manual_max_attempts 为 0 表示不限次数, 上游连续失败多次后仍继续请求, 成功即停止。
func TestManualRetryContinuesWhenAttemptLimitIsZero(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 3)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 0 // 不限额: 成员尝试次数与重试次数都不受限制。
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

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

// TestManualRetryStopsAtAttemptLimitOne 锁定手动上限为 1 时只请求一次:
// 首次请求即为全部预算, 失败后不再重试, 直接以协议合法的上游错误结束请求。
func TestManualRetryStopsAtAttemptLimitOne(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 2)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 1
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))

	assertUpstreamFailure(t, recorder)
	assertRetryAfter(t, recorder, "1")
	if got := upstream.requestCount(); got != 1 {
		t.Fatalf("上游请求次数 = %d, 期望 1 (手动上限为 1 时首次请求即耗尽预算)", got)
	}
}

// TestManualRetryStopsAfterConfiguredAttempts 锁定手动上限为 3 时总共请求三次:
// 上游第 4 次本会成功, 请求仍必须在上限处结束, 证明结束原因是上限而非上游恰好成功。
func TestManualRetryStopsAfterConfiguredAttempts(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 3)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRetryConfigWithMode(model.RetryModeExponential)
	config.ManualMaxAttempts = 3
	config.RetryMaxIntervalSeconds = 8
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	started := time.Now()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	elapsed := time.Since(started)

	assertUpstreamFailure(t, recorder)
	assertRetryAfter(t, recorder, "4")
	if got := upstream.requestCount(); got != 3 {
		t.Fatalf("上游请求次数 = %d, 期望 3 (手动上限为 3)", got)
	}
	// 两次重试分别等待 1 秒和 2 秒, 确认指数退避生效且上限不是靠提前退出达成的。
	if elapsed < 2500*time.Millisecond {
		t.Fatalf("三次尝试仅耗时 %v, 未体现指数退避的等待", elapsed)
	}
}

// TestInvalidRequestRemainsBadRequest 锁定无效输入仍返回 400 invalid_request_error, 且不带上游重试提示。
func TestInvalidRequestRemainsBadRequest(t *testing.T) {
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{"))
	req.Header.Set("Content-Type", "application/json")
	serveWithTimeout(t, engine, recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("无效请求状态码 = %d, 期望 400; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	var failure struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil {
		t.Fatalf("无效请求响应不是合法的错误对象: %v; 响应正文 %s", err, recorder.Body.String())
	}
	if failure.Error.Type != "invalid_request_error" {
		t.Fatalf("无效请求错误类型 = %q, 期望 invalid_request_error; 响应正文 %s", failure.Error.Type, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "" {
		t.Fatalf("无效请求不应设置 Retry-After, 实际为 %q", got)
	}
}

// TestExponentialRetryUsesIncreasingBackoff 锁定指数退避确实取代了固定间隔:
// 初始间隔 1 秒时两次重试分别等待 1 秒与 2 秒, 共至少 3 秒; 若仍按固定 1 秒间隔则只有 2 秒。
func TestExponentialRetryUsesIncreasingBackoff(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 2)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRetryConfigWithMode(model.RetryModeExponential)
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	started := time.Now()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	elapsed := time.Since(started)

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if got := upstream.requestCount(); got != 3 {
		t.Fatalf("上游请求次数 = %d, 期望 3 (两次失败 + 一次成功)", got)
	}
	// 1 秒 + 2 秒 = 3 秒; 下界取 2.5 秒, 与固定间隔的 2 秒拉开距离。
	if elapsed < 2500*time.Millisecond {
		t.Fatalf("三次尝试耗时 %v, 未体现指数退避 (期望至少 1s + 2s)", elapsed)
	}
}

// TestExponentialRetryBackoffStopsOnContextCancel 锁定退避等待可被取消:
// 初始间隔 30 秒时首次失败后的等待很长, 取消请求上下文后转发必须立即结束且不再发起新的上游请求。
func TestExponentialRetryBackoffStopsOnContextCancel(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, failingStatuses(http.StatusInternalServerError, 64)...)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRetryConfigWithMode(model.RetryModeExponential)
	config.RetryInitialIntervalSeconds = 30
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newRelayEngine()
	req := chatRequest(t, groupName, false).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(httptest.NewRecorder(), req)
	}()

	// 上游第一次被调用说明首次尝试已经发生, 此刻请求正处在 30 秒的退避等待中。
	upstream.awaitRequests(t, 1, 10*time.Second)
	// 首次尝试后的 3 秒内不应出现第二次上游请求: 固定间隔为 1 秒时会在这里被抓住。
	select {
	case <-upstream.arrived:
		t.Fatalf("首次尝试后 3 秒内出现了第二次上游请求, 未体现 %d 秒的退避等待", config.RetryInitialIntervalSeconds)
	case <-time.After(3 * time.Second):
	}
	before := upstream.requestCount()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("取消后转发未在 5 秒内结束, 上游已收到 %d 次请求", upstream.requestCount())
	}

	if after := upstream.requestCount(); after != before {
		t.Fatalf("取消后上游请求次数由 %d 增长到 %d, 取消未能阻止新的上游请求", before, after)
	}
}

// TestManualRetryDoesNotConsumeAttemptWhenSendFailsBeforeUpstream 锁定"仅实际发起的成员请求消耗尝试次数":
// 渠道参数覆盖为非法 JSON 时, 发送层在构造上游请求时即失败, 上游一次都没有收到请求;
// 这类失败不得消耗手动尝试额度, 故 manual_max_attempts 为 1 时请求不会就此以 503 终结。
func TestManualRetryDoesNotConsumeAttemptWhenSendFailsBeforeUpstream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "non-stream"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			log := &upstreamCallLog{}
			upstream := newFakeUpstream(t, "member", log)
			grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
			setChannelParamOverride(t, grantID, "{ not json")
			config := testRelayConfig(2)
			config.ManualMaxAttempts = 1
			groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			engine := newRelayEngine()
			recorder := httptest.NewRecorder()
			done := make(chan struct{})
			go func() {
				defer close(done)
				engine.ServeHTTP(recorder, chatRequest(t, groupName, streaming).WithContext(ctx))
			}()

			// 额度若被这种未发出的失败消耗, 首次失败后请求会立即以 503 结束; 仍在运行即证明额度未被消耗。
			select {
			case <-done:
				t.Fatalf("未发出的失败消耗了手动尝试额度: 请求立即结束, 状态码 = %d, 响应正文 %s", recorder.Code, recorder.Body.String())
			case <-time.After(3 * time.Second):
			}
			if got := upstream.requestCount(); got != 0 {
				t.Fatalf("上游收到 %d 次请求, 期望 0 (发送前即失败)", got)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("取消后转发未在 5 秒内结束")
			}
		})
	}
}

// TestManualRetryCountsConnectionFailure 锁定连接失败仍消耗尝试额度:
// 上游不可达属于真正发起过的请求, 手动上限为 1 时会因此耗尽并以 503 结束;
// 若连接失败不计数, 请求会一直重试而不结束, 由 serveWithTimeout 的 30 秒上限抓住。
func TestManualRetryCountsConnectionFailure(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	upstream.server.Close() // 关掉测试上游: 之后的连接一律被拒。

	config := testRelayConfig(2)
	config.ManualMaxAttempts = 1
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))

	assertUpstreamFailure(t, recorder)
}

// TestManualRetryCountsResponseTimeout 锁定响应超时仍消耗尝试额度:
// 上游在非流式超时内不返回响应, 该轮属于真正发起过的请求, 手动上限为 1 时会因此耗尽并以 503 结束。
func TestManualRetryCountsResponseTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 长于下面配置的 1 秒超时, 但短到不会明显拖慢测试收尾。
		time.Sleep(2500 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)

	grantID := newTestChannel(t, testName(t, "channel"), slow.URL, "upstream-model")
	config := testRelayConfig(2)
	config.MemberNonStreamResponseTimeoutSeconds = 1
	config.ManualMaxAttempts = 1
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))

	assertUpstreamFailure(t, recorder)
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
