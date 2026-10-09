package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
)

func TestRequestTelemetryTracksRetryLifecycle(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "telemetry", log, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(2)
	config.ManualMaxAttempts = 2
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	request := chatRequest(t, groupName, false)
	go func() {
		defer close(done)
		engine.ServeHTTP(recorder, request)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("遥测重试请求未在 10 秒内完成")
	}

	var states []RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 在完成前关闭")
			}
			if state.GroupID == group.ID {
				states = append(states, state)
			}
		default:
			goto drained
		}
	}

drained:
	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var sawSelected, sawFirstAttempt, sawFailure, sawBackoff, sawSecondAttempt bool
	var final RequestState
	for _, state := range states {
		if state.TargetChannel != "" && state.ChannelStatus == ChannelStatusNormal && state.Attempt == 0 {
			sawSelected = true
		}
		if state.Attempt == 1 && state.RetryCount == 0 && state.AttemptLimit == 2 && state.Sending {
			sawFirstAttempt = true
		}
		if state.ErrorCount == 1 && state.LastError != "" && state.ChannelStatus == ChannelStatusError {
			sawFailure = true
		}
		if state.Retrying && state.ChannelStatus == ChannelStatusWaiting && state.NextRetryAt > state.StartedAt.UnixMilli() {
			sawBackoff = true
		}
		if state.Attempt == 2 && state.RetryCount == 1 && state.Sending {
			sawSecondAttempt = true
		}
		if state.Status == StatusSuccess {
			final = state
		}
	}
	if !sawSelected || !sawFirstAttempt || !sawFailure || !sawBackoff || !sawSecondAttempt {
		t.Fatalf("状态流缺少完整重试迁移: selected=%t first=%t failure=%t backoff=%t second=%t; states=%+v",
			sawSelected, sawFirstAttempt, sawFailure, sawBackoff, sawSecondAttempt, states)
	}
	if final.Status != StatusSuccess || final.Attempt != 2 || final.RetryCount != 1 || final.ErrorCount != 1 {
		t.Fatalf("终态遥测不一致: %+v", final)
	}
	if final.ChannelStatus != ChannelStatusNormal || final.Retrying || final.NextRetryAt != 0 || final.LastError == "" {
		t.Fatalf("成功终态应清除等待并保留最近错误诊断: %+v", final)
	}

	encoded, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("编码请求状态失败: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("解析请求状态 JSON 失败: %v", err)
	}
	for _, field := range []string{"id", "status", "started_at", "model", "round", "target_channel", "sending"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("兼容性字段 %q 缺失", field)
		}
	}
	for _, field := range []string{"retry_count", "attempt", "attempt_limit", "retrying", "error_count", "last_error", "next_retry_at", "channel_status"} {
		if _, ok := fields[field]; !ok {
			t.Errorf("遥测字段 %q 缺失", field)
		}
	}
}

func TestRequestTelemetryFinalFailure(t *testing.T) {
	upstream := newFakeUpstream(t, "telemetry-failure", &upstreamCallLog{}, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	config := testRelayConfig(1)
	config.ManualMaxAttempts = 1
	groupName := newTestGroup(t, model.GroupModeManual, config, grantID)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("最终状态码 = %d, 期望 503; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var final RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 提前关闭")
			}
			if state.GroupID == group.ID && state.Status == StatusFailed {
				final = state
			}
		default:
			if final.Status != StatusFailed {
				t.Fatal("状态流中未找到最终失败快照")
			}
			if final.Attempt != 1 || final.RetryCount != 0 || final.AttemptLimit != 1 || final.ErrorCount != 1 {
				t.Fatalf("最终失败计数不一致: %+v", final)
			}
			if final.ChannelStatus != ChannelStatusError || final.Retrying || final.NextRetryAt != 0 || final.LastError == "" {
				t.Fatalf("最终失败状态不一致: %+v", final)
			}
			return
		}
	}
}

func TestRequestTelemetryResetsAttemptsWhenMemberChanges(t *testing.T) {
	first := newFakeUpstream(t, "telemetry-first", &upstreamCallLog{}, http.StatusInternalServerError)
	second := newFakeUpstream(t, "telemetry-second", &upstreamCallLog{})
	firstGrant := newTestChannel(t, testName(t, "channel-first"), first.server.URL, "first-model")
	secondChannel := testName(t, "channel-second")
	secondGrant := newTestChannel(t, secondChannel, second.server.URL, "second-model")
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), firstGrant, secondGrant)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)
	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, false))
	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}

	var switched, final RequestState
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 提前关闭")
			}
			if state.GroupID != group.ID {
				continue
			}
			if state.TargetChannel == secondChannel && state.Attempt == 0 && state.ChannelStatus == ChannelStatusNormal {
				switched = state
			}
			if state.Status == StatusSuccess {
				final = state
			}
		default:
			if switched.TargetChannel == "" {
				t.Fatal("状态流没有发布切换到第二个成员的快照")
			}
			if switched.ErrorCount != 1 || switched.AttemptLimit != 1 {
				t.Fatalf("成员切换快照应保留累计错误并重置成员尝试: %+v", switched)
			}
			if final.Status != StatusSuccess || final.TargetChannel != secondChannel || final.Attempt != 1 || final.RetryCount != 0 {
				t.Fatalf("切换成员后的终态计数不一致: %+v", final)
			}
			return
		}
	}
}
