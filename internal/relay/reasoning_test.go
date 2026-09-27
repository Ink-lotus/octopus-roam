package relay

// 本文件覆盖上游 v0.13.8 移植进来的思考等级(reasoning effort)采集:
// 入站转换器统一解析各协议的思考等级, 处理器读取后写入 RequestState 与状态 JSON;
// 未提供或入站解析失败时保持空字符串且不改变既有转发流程。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// chatRequestWithReasoning 构造带思考等级字段的 Chat Completions 客户端请求, 模型名即分组名。
func chatRequestWithReasoning(t *testing.T, groupName, effort string) *http.Request {
	t.Helper()

	body := fmt.Sprintf(`{"model":%q,"reasoning_effort":%q,"messages":[{"role":"user","content":"ping"}]}`, groupName, effort)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// finalSuccessForGroup 排空状态流并返回该分组的成功终态快照。
func finalSuccessForGroup(t *testing.T, stream chan RequestState, groupID int) RequestState {
	t.Helper()

	var final RequestState
	found := false
	for {
		select {
		case state, ok := <-stream:
			if !ok {
				t.Fatal("请求状态 SSE 在完成前关闭")
			}
			if state.GroupID == groupID && state.Status == StatusSuccess {
				final = state
				found = true
			}
		default:
			if !found {
				t.Fatal("状态流中未找到成功终态快照")
			}
			return final
		}
	}
}

// requestStateJSONField 把请求状态编码为 JSON 并取出指定字段, 校验字段确实存在于对外契约。
func requestStateJSONField(t *testing.T, state RequestState, field string) (json.RawMessage, bool) {
	t.Helper()

	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("编码请求状态失败: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("解析请求状态 JSON 失败: %v", err)
	}
	raw, ok := fields[field]
	return raw, ok
}

// TestReasoningEffortRecordedInRequestState 断言提供思考等级时其被写入 RequestState 与状态 JSON, 且响应正常转发。
func TestReasoningEffortRecordedInRequestState(t *testing.T) {
	upstream := newFakeUpstream(t, "reasoning-present", &upstreamCallLog{})
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grantID)
	group, err := op.GroupGetByName(groupName)
	if err != nil {
		t.Fatalf("读取测试分组失败: %v", err)
	}

	_, stream := OpenRequestStream()
	defer CloseRequestStream(stream)

	engine := newRelayEngine()
	recorder := httptest.NewRecorder()
	serveWithTimeout(t, engine, recorder, chatRequestWithReasoning(t, groupName, llm.ReasoningEffortHigh))
	if recorder.Code != http.StatusOK {
		t.Fatalf("最终状态码 = %d, 期望 200; 响应正文 %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "pong") {
		t.Fatalf("响应正文未按原样转发: %s", recorder.Body.String())
	}

	final := finalSuccessForGroup(t, stream, group.ID)
	if final.ReasoningEffort != llm.ReasoningEffortHigh {
		t.Fatalf("ReasoningEffort = %q, 期望 %q; 快照 %+v", final.ReasoningEffort, llm.ReasoningEffortHigh, final)
	}

	raw, present := requestStateJSONField(t, final, "reasoning_effort")
	if !present {
		t.Fatal("状态 JSON 缺少 reasoning_effort 字段")
	}
	var effort string
	if err := json.Unmarshal(raw, &effort); err != nil {
		t.Fatalf("reasoning_effort 字段不是字符串: %v", err)
	}
	if effort != llm.ReasoningEffortHigh {
		t.Fatalf("状态 JSON reasoning_effort = %q, 期望 %q", effort, llm.ReasoningEffortHigh)
	}
}

// TestReasoningEffortEmptyWhenAbsent 断言未提供思考等级时保持空字符串, 字段仍在契约中, 且转发不受影响。
func TestReasoningEffortEmptyWhenAbsent(t *testing.T) {
	upstream := newFakeUpstream(t, "reasoning-absent", &upstreamCallLog{})
	grantID := newTestChannel(t, testName(t, "channel"), upstream.server.URL, "upstream-model")
	groupName := newTestGroup(t, model.GroupModeManual, testRelayConfig(1), grantID)
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

	final := finalSuccessForGroup(t, stream, group.ID)
	if final.ReasoningEffort != "" {
		t.Fatalf("未提供思考等级时 ReasoningEffort 应为空, 实际 %q", final.ReasoningEffort)
	}
	if _, present := requestStateJSONField(t, final, "reasoning_effort"); !present {
		t.Fatal("状态 JSON 应始终包含 reasoning_effort 字段")
	}
}

// TestReasoningEffortExtractionAcrossProtocols 断言处理器采用的入站解析对三种协议均归一出统一思考等级,
// 并确认入站解析失败时返回错误, 由此处理器可保持空字符串。
func TestReasoningEffortExtractionAcrossProtocols(t *testing.T) {
	cases := []struct {
		name    string
		inbound transformer.Inbound
		body    string
		want    string
	}{
		{
			name:    "openai_chat_reasoning_effort",
			inbound: openai.NewInboundTransformer(),
			body:    `{"model":"m","reasoning_effort":"high","messages":[{"role":"user","content":"ping"}]}`,
			want:    llm.ReasoningEffortHigh,
		},
		{
			name:    "openai_responses_reasoning_effort",
			inbound: responses.NewInboundTransformer(),
			body:    `{"model":"m","input":"ping","reasoning":{"effort":"low"}}`,
			want:    llm.ReasoningEffortLow,
		},
		{
			name:    "anthropic_thinking_budget",
			inbound: anthropic.NewInboundTransformer(),
			body:    `{"model":"m","max_tokens":1024,"messages":[{"role":"user","content":"ping"}],"thinking":{"type":"enabled","budget_tokens":20000}}`,
			want:    llm.ReasoningEffortHigh,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := &httpclient.Request{
				Body:    []byte(tc.body),
				Headers: http.Header{"Content-Type": []string{"application/json"}},
			}
			parsed, err := tc.inbound.TransformRequest(context.Background(), raw)
			if err != nil {
				t.Fatalf("入站解析失败: %v", err)
			}
			if parsed.ReasoningEffort != tc.want {
				t.Fatalf("ReasoningEffort = %q, 期望 %q", parsed.ReasoningEffort, tc.want)
			}
		})
	}

	// 入站解析失败时应返回错误, 处理器据此保持空思考等级而不改变转发流程。
	raw := &httpclient.Request{
		Body:    []byte(`{"model":"m","messages":`), // 截断的非法 JSON
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}
	if _, err := openai.NewInboundTransformer().TransformRequest(context.Background(), raw); err == nil {
		t.Fatal("非法请求体应导致入站解析失败")
	}
}
