package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// request 状态流确认转发已进入等待后再修改配置, 避免用睡眠猜测调度顺序。
func startManualWaitForward(t *testing.T, name string) (*httptest.ResponseRecorder, <-chan struct{}, context.CancelFunc, chan RequestState) {
	t.Helper()
	_, stream := OpenRequestStream()
	t.Cleanup(func() { CloseRequestStream(stream) })
	ctx, cancel := context.WithCancel(context.Background())
	request := chatRequest(t, name, false).WithContext(ctx)
	engine := newRelayEngine()
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		engine.ServeHTTP(response, request)
	}()
	t.Cleanup(func() {
		cancel()
		awaitManualForwardDone(t, done)
	})
	return response, done, cancel, stream
}

func awaitManualForwardDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("转发未及时结束, 可能仍在等待旧的长间隔")
	}
}

func awaitManualRequestState(t *testing.T, stream <-chan RequestState, name string, accept func(RequestState) bool) RequestState {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("等待请求状态时订阅已关闭")
			}
			if state.Model == name && accept(state) {
				return state
			}
		case <-timer.C:
			t.Fatal("未及时收到期望的请求状态")
		}
	}
}

func manualWaitGroup(t *testing.T, name string) model.Group {
	t.Helper()
	group, err := op.GroupGetByName(name)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}
	return group
}

func updateManualWaitGroup(t *testing.T, groupID int, update model.GroupUpdateRequest) model.Group {
	t.Helper()
	group, err := op.GroupUpdate(groupID, &update, t.Context())
	if err != nil {
		t.Fatalf("更新测试分组失败: %v", err)
	}
	ResetRouteStateForGroup(*group)
	return *group
}

func isManualWaiting(state RequestState) bool { return state.Retrying }

func TestManualUnselectedNotificationsKeepWaitingUntilSelected(t *testing.T) {
	upstream := newFakeUpstream(t, "member", &upstreamCallLog{})
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(1)
	config.MemberRetryIntervalSeconds = 60
	config.ManualMaxAttempts = 1
	name := newTestGroup(t, model.GroupModeManual, config, grantID)
	group := manualWaitGroup(t, name)
	itemID, noSelection := group.ActiveItemID, 0
	group = updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{ActiveItemID: &noSelection})

	response, done, _, stream := startManualWaitForward(t, name)
	for i := 0; i < 3; i++ {
		state := awaitManualRequestState(t, stream, name, isManualWaiting)
		if state.NextRetryAt != 0 || state.Attempt != 0 || state.Status != StatusRunning {
			t.Fatalf("未选择成员应无限等待且不消耗次数: %+v", state)
		}
		if i == 0 {
			NotifyRouteStateChanged(group)
		} else if i == 1 {
			config.MemberRetryIntervalSeconds = 90
			group = updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{RelayConfig: &config})
		}
	}
	select {
	case <-done:
		t.Fatal("通知后仍未选择成员, 请求却已结束")
	default:
	}
	if got := upstream.requestCount(); got != 0 {
		t.Fatalf("未选择成员时收到 %d 次上游请求", got)
	}

	updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{ActiveItemID: &itemID})
	awaitManualForwardDone(t, done)
	if response.Code != http.StatusOK || upstream.requestCount() != 1 {
		t.Fatalf("选择后未立即成功: status=%d requests=%d", response.Code, upstream.requestCount())
	}
}

func TestManualUnavailableWaitRespondsToAvailabilityAndMode(t *testing.T) {
	for _, change := range []string{"availability", "grant", "mode", "cancel"} {
		t.Run(change, func(t *testing.T) {
			upstream := newFakeUpstream(t, "member", &upstreamCallLog{})
			grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
			config := testRelayConfig(1)
			config.MemberRetryIntervalSeconds = 60
			name := newTestGroup(t, model.GroupModeManual, config, grantID)
			group := manualWaitGroup(t, name)
			channelID := group.Items[0].ChannelID
			if change == "availability" || change == "grant" {
				if change == "grant" {
					setManualWaitKeyEnabled(t, channelID, false)
				} else if err := op.ChannelEnabled(channelID, false, t.Context()); err != nil {
					t.Fatal(err)
				}
				group = manualWaitGroup(t, name)
				NotifyRouteStateChanged(group)
			} else {
				noSelection := 0
				group = updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{ActiveItemID: &noSelection})
			}
			response, done, cancel, stream := startManualWaitForward(t, name)
			awaitManualRequestState(t, stream, name, isManualWaiting)
			switch change {
			case "availability":
				if err := op.ChannelEnabled(channelID, true, t.Context()); err != nil {
					t.Fatal(err)
				}
				NotifyRouteStateChanged(manualWaitGroup(t, name))
			case "grant":
				setManualWaitKeyEnabled(t, channelID, true)
				NotifyRouteStateChanged(manualWaitGroup(t, name))
			case "mode":
				mode := model.GroupModeFailover
				updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{Mode: &mode})
			case "cancel":
				cancel()
			}
			awaitManualForwardDone(t, done)
			if change == "cancel" {
				awaitManualRequestState(t, stream, name, func(state RequestState) bool { return state.Status == StatusCanceled })
				if upstream.requestCount() != 0 {
					t.Fatal("取消无选择等待后仍发出请求")
				}
			} else if response.Code != http.StatusOK || upstream.requestCount() != 1 {
				t.Fatalf("更新后未立即成功: status=%d requests=%d", response.Code, upstream.requestCount())
			}
		})
	}
}

func setManualWaitKeyEnabled(t *testing.T, channelID int, enabled bool) {
	t.Helper()
	detail, err := op.ChannelDetailGet(channelID)
	if err != nil {
		t.Fatal(err)
	}
	detail.Keys[0].Enabled = enabled
	if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestManualRetryWaitReloadsPolicy(t *testing.T) {
	for _, mode := range []model.RetryMode{model.RetryModeFixed, model.RetryModeExponential} {
		t.Run(string(mode), func(t *testing.T) {
			upstream := newFakeUpstream(t, "member", &upstreamCallLog{}, failingStatuses(http.StatusInternalServerError, 3)...)
			grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
			config := testRelayConfig(1)
			config.RetryMode = mode
			config.MemberRetryIntervalSeconds = 60
			config.RetryInitialIntervalSeconds = 60
			name := newTestGroup(t, model.GroupModeManual, config, grantID)
			group := manualWaitGroup(t, name)
			response, done, _, stream := startManualWaitForward(t, name)
			state := awaitManualRequestState(t, stream, name, isManualWaiting)
			if state.Attempt != 1 || state.NextRetryAt < time.Now().Add(30*time.Second).UnixMilli() {
				t.Fatalf("未进入配置的长重试间隔: %+v", state)
			}

			config.ManualMaxAttempts = 1
			config.MemberRetryIntervalSeconds = 1
			config.RetryInitialIntervalSeconds = 1
			updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{RelayConfig: &config})
			awaitManualForwardDone(t, done)
			assertUpstreamFailure(t, response)
			assertRetryAfter(t, response, "1")
			if got := upstream.requestCount(); got != 2 {
				t.Fatalf("新策略应立即生效并在一次新尝试后结束, 实际请求 %d 次", got)
			}
		})
	}
}

func TestManualUnsentFailureWaitRespondsToChannelNotification(t *testing.T) {
	upstream := newFakeUpstream(t, "member", &upstreamCallLog{})
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	setChannelParamOverride(t, grantID, "{ invalid json")
	config := testRelayConfig(1)
	config.MemberRetryIntervalSeconds = 60
	config.ManualMaxAttempts = 1
	name := newTestGroup(t, model.GroupModeManual, config, grantID)
	response, done, _, stream := startManualWaitForward(t, name)
	state := awaitManualRequestState(t, stream, name, isManualWaiting)
	if state.Attempt != 0 || state.ErrorCount != 1 || upstream.requestCount() != 0 {
		t.Fatalf("发送前错误不应消耗请求次数: attempt=%d errors=%d requests=%d", state.Attempt, state.ErrorCount, upstream.requestCount())
	}
	setChannelParamOverride(t, grantID, "")
	NotifyRouteStateChanged(manualWaitGroup(t, name))
	awaitManualForwardDone(t, done)
	if response.Code != http.StatusOK || upstream.requestCount() != 1 {
		t.Fatalf("渠道配置修复后未立即成功: status=%d requests=%d", response.Code, upstream.requestCount())
	}
}

func TestManualStaleConfigUsesShortRereadDelay(t *testing.T) {
	upstream := newFakeUpstream(t, "member", &upstreamCallLog{})
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(1)
	config.MemberRetryIntervalSeconds = 60
	name := newTestGroup(t, model.GroupModeManual, config, grantID)
	group := manualWaitGroup(t, name)
	updated := group
	updated.RelayConfig.ManualMaxAttempts = 1
	ResetRouteStateForGroup(updated)
	response, done, _, stream := startManualWaitForward(t, name)
	awaitManualRequestState(t, stream, name, isManualWaiting)

	// 指纹先更新、请求仍持有旧缓存的窗口内, 旧快照不能重建路由。
	routeMu.Lock()
	route := routes[group.ID]
	routeMu.Unlock()
	if route != nil || upstream.requestCount() != 0 {
		t.Fatal("旧配置在指纹更新后重建了路由或发送了请求")
	}
	if _, err := op.GroupUpdate(group.ID, &model.GroupUpdateRequest{RelayConfig: &updated.RelayConfig}, t.Context()); err != nil {
		t.Fatal(err)
	}
	// 此处不再通知: 已失配的选择必须遵循短重读截止时间, 不能回落到旧的 60 秒间隔。
	awaitManualForwardDone(t, done)
	if response.Code != http.StatusOK || upstream.requestCount() != 1 {
		t.Fatalf("配置对齐后未及时成功: status=%d requests=%d", response.Code, upstream.requestCount())
	}
}

func TestManualInFlightFailureCannotApplyStaleAttemptLimit(t *testing.T) {
	arrived, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"scripted upstream failure","type":"server_error"}}`))
	}))
	t.Cleanup(upstream.Close)
	grantID := newTestChannel(t, testName(t, "old-channel"), upstream.URL, "old-model")
	replacement := newFakeUpstream(t, "new", &upstreamCallLog{})
	newGrantID := newTestChannel(t, testName(t, "new-channel"), replacement.server.URL, "new-model")
	config := testRelayConfig(1)
	config.ManualMaxAttempts = 1
	name := newTestGroup(t, model.GroupModeManual, config, grantID, newGrantID)
	group := manualWaitGroup(t, name)
	response, done, _, _ := startManualWaitForward(t, name)
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("旧成员未收到请求")
	}
	newItemID := group.Items[1].ID
	updateManualWaitGroup(t, group.ID, model.GroupUpdateRequest{ActiveItemID: &newItemID})
	close(release)
	awaitManualForwardDone(t, done)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pong") || replacement.requestCount() != 1 {
		t.Fatalf("旧成员失败后未使用新选择: status=%d requests=%d", response.Code, replacement.requestCount())
	}
}
