package relay

// 本文件锁定 Relay 当前的基本故障转移基线: 成员在耗尽 member_max_attempts 次尝试后进入冷却,
// 请求转由后续成员承载并最终成功, 以及由此产生的成员调用顺序与次数。
// 断言全部落在可观察事实上: 上游实际收到的请求序列, 每个成员的请求次数, 最终 HTTP 状态与正文。

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// TestFailoverSwitchesToNextMemberAfterAttemptsExhausted 锁定成员耗尽尝试次数后切换:
// 首要成员按 member_max_attempts 被调用两次后让出路由, 请求随即由第二个成员承载并成功。
func TestFailoverSwitchesToNextMemberAfterAttemptsExhausted(t *testing.T) {
	log := &upstreamCallLog{}
	first := newFakeUpstream(t, "first", log, http.StatusInternalServerError, http.StatusInternalServerError)
	second := newFakeUpstream(t, "second", log)

	firstGrant := newTestChannel(t, testName(t, "channel-first"), first.server.URL, "model-first")
	secondGrant := newTestChannel(t, testName(t, "channel-second"), second.server.URL, "model-second")
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), firstGrant, secondGrant)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "pong") {
		t.Fatalf("最终响应正文未包含上游成功内容: %s", body)
	}

	// 记录中的上游模型名证明本轮实际命中的是哪个成员, 而非仅凭调用次数推断。
	want := []string{"first/model-first", "first/model-first", "second/model-second"}
	if calls := log.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("成员调用顺序 = %v, 期望 %v", calls, want)
	}
	if got := log.count("first"); got != 2 {
		t.Fatalf("首个成员请求次数 = %d, 期望 2 (member_max_attempts)", got)
	}
	if got := log.count("second"); got != 1 {
		t.Fatalf("第二个成员请求次数 = %d, 期望 1", got)
	}
}

// TestFailoverCallOrderAndCounts 用四个成员锁定当前的调用顺序与次数:
// 前两个成员各自耗尽尝试次数, 第三个成员首次即成功, 未参与本场景的第四个成员一次也不应被调用。
func TestFailoverCallOrderAndCounts(t *testing.T) {
	log := &upstreamCallLog{}
	first := newFakeUpstream(t, "first", log, http.StatusInternalServerError, http.StatusInternalServerError)
	second := newFakeUpstream(t, "second", log, http.StatusInternalServerError, http.StatusInternalServerError)
	third := newFakeUpstream(t, "third", log)
	unused := newFakeUpstream(t, "unused", log)

	firstGrant := newTestChannel(t, testName(t, "channel-first"), first.server.URL, "model-first")
	secondGrant := newTestChannel(t, testName(t, "channel-second"), second.server.URL, "model-second")
	thirdGrant := newTestChannel(t, testName(t, "channel-third"), third.server.URL, "model-third")
	unusedGrant := newTestChannel(t, testName(t, "channel-unused"), unused.server.URL, "model-unused")
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), firstGrant, secondGrant, thirdGrant, unusedGrant)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	want := []string{
		"first/model-first", "first/model-first",
		"second/model-second", "second/model-second",
		"third/model-third",
	}
	if calls := log.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("成员调用顺序 = %v, 期望 %v", calls, want)
	}
	if got := log.count("third"); got != 1 {
		t.Fatalf("第三个成员请求次数 = %d, 期望 1", got)
	}
	if got := unused.requestCount(); got != 0 {
		t.Fatalf("未参与本场景的成员被调用了 %d 次, 期望 0", got)
	}
}

// TestFailoverSkipsMemberWhoseOutboundCannotBeBuilt 锁定"未发出上游请求的失败"也要让出路由:
// 首个成员的渠道地址缺失使出站转换器无法构造, 本轮失败发生在请求上游之前, 该成员一次上游请求都不会有;
// 这类失败由配置决定, 重试同一成员不会改变结果, 故请求必须转由第二个成员承载并成功。
func TestFailoverSkipsMemberWhoseOutboundCannotBeBuilt(t *testing.T) {
	log := &upstreamCallLog{}
	broken := newFakeUpstream(t, "broken", log) // 地址被清空, 本场景中不应收到任何请求。
	healthy := newFakeUpstream(t, "healthy", log)

	brokenGrant := newTestChannel(t, testName(t, "channel-broken"), broken.server.URL, "model-broken")
	healthyGrant := newTestChannel(t, testName(t, "channel-healthy"), healthy.server.URL, "model-healthy")
	breakChannelBaseURL(t, brokenGrant)
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(2), brokenGrant, healthyGrant)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, chatRequest(t, groupName, false).WithContext(ctx))
	}()

	// 第二个成员收到请求即证明路由换过了成员; 该断言用 5 秒超时, 失败时报出的是"没有换成员"而非无界等待。
	healthy.awaitRequests(t, 1, 5*time.Second)
	// 等待转发自行结束: 此时才可读取响应记录; 取消只用于避免断言失败时留下未结束的请求。
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("转发未在 5 秒内结束, 疑为停在无法构造出站转换器的成员上")
	}

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "pong") {
		t.Fatalf("最终响应正文未包含上游成功内容: %s", body)
	}
	want := []string{"healthy/model-healthy"}
	if calls := log.snapshot(); !slices.Equal(calls, want) {
		t.Fatalf("成员调用记录 = %v, 期望 %v", calls, want)
	}
	if got := broken.requestCount(); got != 0 {
		t.Fatalf("无法构造出站转换器的成员收到了 %d 次上游请求, 期望 0", got)
	}
}

// TestFailoverCooldownWaitStopsOnRequestCancellation 验证全员冷却期间不继续固定轮询,
// 且请求取消会立即打断最早冷却截止时间等待。
func TestFailoverCooldownWaitStopsOnRequestCancellation(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "cooling", log, http.StatusInternalServerError)
	grant := newTestChannel(t, testName(t, "channel-cooling"), upstream.server.URL, "model-cooling")
	config := testRelayConfig(1)
	config.MemberCooldownSeconds = 30
	groupName := newTestGroup(t, model.GroupModeFailover, config, grant)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	stream := OpenRouteStream()
	defer CloseRouteStream(stream)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, chatRequest(t, groupName, false).WithContext(ctx))
	}()

	upstream.awaitRequests(t, 1, 5*time.Second)
	deadline := time.After(5 * time.Second)
	for {
		select {
		case state := <-stream:
			if state.GroupID == group.ID && state.CurrentItemID == 0 && state.Cooldowns[group.Items[0].ID] > time.Now().UnixMilli() {
				goto cooling
			}
		case <-deadline:
			t.Fatal("成员失败后未进入冷却等待")
		}
	}

cooling:
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消未打断全员冷却等待")
	}
	if got := upstream.requestCount(); got != 1 {
		t.Fatalf("冷却期间重复请求上游 %d 次, 期望 1 次", got)
	}
}

// TestFailoverRecoveryProbeGetsFreshAttemptBudget 验证同一成员再次成为探测候选时,
// 恢复探测仍拥有完整 member_max_attempts 预算。
func TestFailoverRecoveryProbeGetsFreshAttemptBudget(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "recovering", log,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
	)
	grant := newTestChannel(t, testName(t, "channel-recovering"), upstream.server.URL, "model-recovering")
	config := testRelayConfig(2)
	config.MemberCooldownSeconds = 1
	groupName := newTestGroup(t, model.GroupModeFailover, config, grant)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	if recorder.Code != http.StatusOK {
		t.Fatalf("恢复探测最终状态码 = %d, 期望 200; 正文 %s", recorder.Code, recorder.Body.String())
	}
	if got := upstream.requestCount(); got != 4 {
		t.Fatalf("上游请求次数 = %d, 期望首轮 2 次加恢复探测 2 次", got)
	}
}

func TestFailoverCanceledProbeBackoffReleasesLease(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "probe-cancel", log,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
		http.StatusInternalServerError,
	)
	grant := newTestChannel(t, testName(t, "channel-probe-cancel"), upstream.server.URL, "model-probe-cancel")
	config := testRelayConfig(2)
	config.MemberCooldownSeconds = 1
	groupName := newTestGroup(t, model.GroupModeFailover, config, grant)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}
	_, requestStream := OpenRequestStream()
	defer CloseRequestStream(requestStream)

	ctx, cancel := context.WithCancel(context.Background())
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, chatRequest(t, groupName, false).WithContext(ctx))
	}()
	upstream.awaitRequests(t, 3, 8*time.Second)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case state := <-requestStream:
			if state.GroupID == group.ID && state.Round == 3 && !state.Sending && state.Error != "" {
				goto probeBackoff
			}
		case <-deadline:
			cancel()
			t.Fatal("恢复探测失败后未进入退避")
		}
	}

probeBackoff:
	if state := RouteStateOf(group); state.ProbeItemID != group.Items[0].ID {
		cancel()
		t.Fatalf("退避期间探测令牌已提前释放: %+v", state)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("取消未结束恢复探测请求")
	}
	if state := RouteStateOf(group); state.ProbeItemID != 0 {
		t.Fatalf("取消退避后未释放探测占用: %+v", state)
	}
}
