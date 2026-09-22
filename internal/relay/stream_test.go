package relay

// 流式路径与非流式路径的关键差异在首帧: 首个有效事件到达前失败仍可换目标重试, 首帧提交后不可再重试。
// 此处只覆盖前一半, 即当前实现确实会在首帧提交之前重试, 并最终把上游事件流转交给客户端。

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
