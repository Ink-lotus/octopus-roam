package relay

import (
	"context"
	"net/http"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

// TestConversionMiddleware_PreservesClientUserAgent 回归测试：
// 跨协议路径下 AxonHub pipeline 的 MergeInboundRequest 同样过滤客户端 User-Agent（v0.13.8 起），
// conversionMiddleware.OnOutboundRawRequest 须在 applyChannelConfig 前补回客户端 UA，
// 供转发上游与 {client_header:User-Agent} 占位符读取。
func TestConversionMiddleware_PreservesClientUserAgent(t *testing.T) {
	const clientUA = "codex_cli_rs/1.2.3"

	m := &conversionMiddleware{
		channel: model.Channel{
			ChannelConfig: model.ChannelConfig{
				CustomHeader: []model.CustomHeader{
					{HeaderKey: "X-Client-UA", HeaderValue: "{client_header:User-Agent}"},
				},
			},
		},
		clientUserAgent: clientUA,
	}

	// 模拟 pipeline 合并入站请求后、客户端 UA 已被过滤掉的出站请求。
	request := &httpclient.Request{
		Method:  http.MethodPost,
		URL:     "https://upstream.example.com/v1/chat/completions",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"gpt-4o"}`),
	}

	got, err := m.OnOutboundRawRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("OnOutboundRawRequest: %v", err)
	}
	// 1) 客户端 UA 必须补回上游请求头。
	if ua := got.Headers.Get("User-Agent"); ua != clientUA {
		t.Errorf("转发上游 User-Agent = %q, want %q", ua, clientUA)
	}
	// 2) {client_header:User-Agent} 占位符必须解析为客户端 UA。
	if v := got.Headers.Get("X-Client-UA"); v != clientUA {
		t.Errorf("{client_header:User-Agent} 解析结果 = %q, want %q", v, clientUA)
	}
}

// TestConversionMiddleware_NoClientUserAgent 客户端未带 UA 时不应凭空造出 UA 头。
func TestConversionMiddleware_NoClientUserAgent(t *testing.T) {
	m := &conversionMiddleware{channel: model.Channel{}}
	request := &httpclient.Request{
		Method:  http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
	}

	got, err := m.OnOutboundRawRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("OnOutboundRawRequest: %v", err)
	}
	if ua := got.Headers.Get("User-Agent"); ua != "" {
		t.Errorf("无客户端 UA 时不应设置 User-Agent, got %q", ua)
	}
}
