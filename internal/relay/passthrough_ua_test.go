package relay

import (
	"net/http"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// TestBuildPassthroughRequest_PreservesClientUserAgent 回归测试：
// AxonHub v0.13.8 起将 User-Agent 列入 httpclient.blockedHeaders，MergeInboundRequest 不再透传客户端 UA，
// 库约定由透传方显式保留。透传路径须把客户端 UA 补回上游请求头，供：
//  1. 原样转发给上游（否则被 BuildHttpRequest 替换成默认 axonhub/1.0）；
//  2. {client_header:User-Agent} 自定义 Header 占位符读取（applyChannelConfig 从合并后的请求头取值）。
func TestBuildPassthroughRequest_PreservesClientUserAgent(t *testing.T) {
	const clientUA = "codex_cli_rs/1.2.3"

	outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType:   openai.PlatformOpenAI,
		BaseURL:        "https://upstream.example.com",
		EndpointPath:   "/v1/chat/completions",
		APIKeyProvider: auth.NewStaticKeyProvider("sk-test"),
	})
	if err != nil {
		t.Fatalf("build outbound: %v", err)
	}

	raw := &httpclient.Request{
		Method: http.MethodPost,
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"User-Agent":   []string{clientUA},
		},
		Body: []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}

	channel := model.Channel{
		ChannelConfig: model.ChannelConfig{
			CustomHeader: []model.CustomHeader{
				{HeaderKey: "X-Client-UA", HeaderValue: "{client_header:User-Agent}"},
			},
		},
	}

	request, err := buildPassthroughRequest(llm.APIFormatOpenAIChatCompletion, raw, channel, outbound, "gpt-4o")
	if err != nil {
		t.Fatalf("buildPassthroughRequest: %v", err)
	}

	// 1) 客户端 UA 必须透传给上游请求头。
	if got := request.Headers.Get("User-Agent"); got != clientUA {
		t.Errorf("透传上游 User-Agent = %q, want %q", got, clientUA)
	}
	// 2) {client_header:User-Agent} 占位符必须解析为客户端 UA，而非空串。
	if got := request.Headers.Get("X-Client-UA"); got != clientUA {
		t.Errorf("{client_header:User-Agent} 解析结果 = %q, want %q", got, clientUA)
	}
}

// TestBuildPassthroughRequest_NoClientUserAgent 客户端未带 UA 时不应凭空造出 UA 头，
// 保持库的默认行为（后续由 BuildHttpRequest 填 axonhub/1.0）。
func TestBuildPassthroughRequest_NoClientUserAgent(t *testing.T) {
	outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{
		PlatformType:   openai.PlatformOpenAI,
		BaseURL:        "https://upstream.example.com",
		EndpointPath:   "/v1/chat/completions",
		APIKeyProvider: auth.NewStaticKeyProvider("sk-test"),
	})
	if err != nil {
		t.Fatalf("build outbound: %v", err)
	}

	raw := &httpclient.Request{
		Method:  http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
	}

	request, err := buildPassthroughRequest(llm.APIFormatOpenAIChatCompletion, raw, model.Channel{}, outbound, "gpt-4o")
	if err != nil {
		t.Fatalf("buildPassthroughRequest: %v", err)
	}

	if got := request.Headers.Get("User-Agent"); got != "" {
		t.Errorf("无客户端 UA 时不应设置 User-Agent, got %q", got)
	}
}
