package relay

// 流式路径与非流式路径的关键差异在首帧: 首个有效事件到达前失败仍可换目标重试, 首帧提交后不可再重试。
// 此处只覆盖前一半, 即当前实现确实会在首帧提交之前重试, 并最终把上游事件流转交给客户端。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestStreamingRetryBeforeFirstEventCommit 覆盖流式路径的首帧前重试:
// 上游首次以失败状态拒绝, 重试后给出 SSE 事件流, 客户端最终收到完整的流式响应。
func TestStreamingRetryBeforeFirstEventCommit(t *testing.T) {
	log := &upstreamCallLog{}
	upstream := newFakeUpstream(t, "member", log, http.StatusInternalServerError)
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(2), grantID)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, true))

	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("响应 Content-Type = %q, 期望 text/event-stream", got)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "pong") {
		t.Fatalf("流式响应正文未包含上游事件内容: %s", body)
	}
	if got := upstream.requestCount(); got != 2 {
		t.Fatalf("上游请求次数 = %d, 期望 2 (首帧提交前失败 + 重试成功)", got)
	}
}

// TestStreamingDoesNotFailoverAfterFirstEventCommit 锁定首个有效事件提交后不再切换成员:
// 首个成员提交部分内容后再返回流内错误, 第二成员不得收到请求。
func TestStreamingDoesNotFailoverAfterFirstEventCommit(t *testing.T) {
	var firstCalls atomic.Int64
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("测试上游响应不支持 Flush")
			return
		}
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"upstream\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"late stream error\",\"type\":\"server_error\"}}\n\n"))
		flusher.Flush()
	}))
	defer first.Close()

	second := newFakeUpstream(t, "second", &upstreamCallLog{})
	firstGrant := newTestChannel(t, testName(t, "channel-first"), first.URL, "first-model")
	secondGrant := newTestChannel(t, testName(t, "channel-second"), second.server.URL, "second-model")
	groupName := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), firstGrant, secondGrant)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequest(t, groupName, true))

	if recorder.Code != http.StatusOK {
		t.Fatalf("流式响应提交首个事件后的状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "partial") || !strings.Contains(body, "late stream error") {
		t.Fatalf("客户端没有收到已提交事件和后续错误事件: %s", body)
	}
	if got := firstCalls.Load(); got != 1 {
		t.Fatalf("首个成员请求次数 = %d, 期望 1", got)
	}
	if got := second.requestCount(); got != 0 {
		t.Fatalf("首字节提交后的流内错误触发了渠道切换, 第二成员请求次数 = %d", got)
	}
}
