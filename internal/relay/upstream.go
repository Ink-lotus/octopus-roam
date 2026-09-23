package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync/atomic"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// upstreamResponse 是已验证但尚未写给客户端的上游成功响应; events 为 nil 表示非流式响应。
// 透传响应保留上游响应头; 跨协议响应由客户端协议决定响应头。失败一律以 error 返回。
type upstreamResponse struct {
	body   []byte                                  // 非流式响应的完整正文。
	header http.Header                             // 同协议透传时需要原样返回的上游响应头。
	events streams.Stream[*httpclient.StreamEvent] // 流式响应中首个事件之后的剩余事件。
	first  *httpclient.StreamEvent                 // 已预读并验证的首个事件。
	last   bool                                    // 首个事件已经终止整个响应流。
	usage  *llm.Usage                              // 上游本次可确认的用量。
	// closeIdle 非 nil 时为渠道专用代理独占客户端的空闲连接归还入口, 消费方读完事件流后必须调用。
	// 仅流式响应会带上它: 非流式响应返回时连接已经用完, 由发起方就地归还。
	closeIdle func()
}

// resolveUpstreamClient 按渠道代理配置取得本轮上游请求使用的客户端。
// 第二个返回值非 nil 时说明客户端是为渠道专用代理新建的独占实例, 不进共享连接池, 用完须调用它归还空闲连接。
func resolveUpstreamClient(channel model.Channel) (*http.Client, func(), error) {
	switch {
	case !channel.Proxy:
		client, err := rhttp.Direct()
		return client, nil, err
	case channel.ChannelProxy == "":
		client, err := rhttp.Proxy()
		return client, nil, err
	default:
		client, err := rhttp.New(channel.ChannelProxy)
		if err != nil {
			return nil, nil, err
		}
		return client, client.CloseIdleConnections, nil
	}
}

// trackingTransport 记录所属客户端是否真的发起过一轮 HTTP 传输。
// 发送前的失败(请求构造, 参数解析, 代理初始化)不经过 RoundTrip, 故调用方据此可区分
// "本轮根本没发出上游请求"与"已发出但失败(连接失败, 响应超时, 上游报错)"。
type trackingTransport struct {
	inner http.RoundTripper
	sent  *atomic.Bool
}

// RoundTrip 标记本轮已进入 HTTP 传输后再交给真正的传输层。
func (t *trackingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.sent.Store(true)
	return t.inner.RoundTrip(request)
}

// trackUpstreamRoundTrip 返回一个上报"是否已发起 HTTP 传输"的客户端副本及其标志。
// 只包一层传输层: 原客户端的连接池与归还入口仍指向同一个传输层, 故并发与连接复用行为不变。
func trackUpstreamRoundTrip(client *http.Client) (*http.Client, *atomic.Bool) {
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	sent := &atomic.Bool{}
	tracked := *client
	tracked.Transport = &trackingTransport{inner: transport, sent: sent}
	return &tracked, sent
}

// sendPassthrough 以同协议透传方式请求上游, 取得的响应无需转换即可回给客户端。
// 第二个返回值说明本轮是否真的向上游发出了请求, 供调用方判定该失败是否消耗成员的尝试次数。
func sendPassthrough(ctx context.Context, format llm.APIFormat, raw *httpclient.Request, channel model.Channel, outbound transformer.Outbound, streaming bool, modelName string) (*upstreamResponse, bool, error) {
	request, err := buildPassthroughRequest(format, raw, channel, outbound, modelName)
	if err != nil {
		return nil, false, err
	}
	httpClient, closeIdle, err := resolveUpstreamClient(channel)
	if err != nil {
		return nil, false, err
	}
	// 传输层上报必须在发起之前装好, 其后的失败一律算已发出。
	httpClient, sent := trackUpstreamRoundTrip(httpClient)

	if streaming {
		// 流式响应返回后仍在读上游连接, 归还入口随响应交给消费方; 取不到响应时就地归还。
		result, err := sendPassthroughStream(ctx, format, request, httpClient)
		if err != nil {
			if closeIdle != nil {
				closeIdle()
			}
			return nil, sent.Load(), err
		}
		result.closeIdle = closeIdle
		return result, sent.Load(), nil
	}
	// 非流式响应在 Do 返回时正文已读完, 连接可以立即归还。
	if closeIdle != nil {
		defer closeIdle()
	}

	response, err := httpclient.NewHttpClientWithClient(httpClient).Do(ctx, request)
	if err != nil {
		var failure *httpclient.Error
		if errors.As(err, &failure) && len(failure.Body) > 0 {
			return nil, sent.Load(), fmt.Errorf("%w: %s", err, failure.Body)
		}
		return nil, sent.Load(), err
	}
	// 同协议下响应可原样回给客户端, 仍需解析一次以取得用量并识别以 200 下发的失败终态。
	parsed, err := outbound.TransformResponse(ctx, response)
	if err != nil {
		return nil, sent.Load(), fmt.Errorf("%w: %s", err, response.Body)
	}
	if err := validateResponse(format, parsed); err != nil {
		return nil, sent.Load(), fmt.Errorf("%w: %s", err, response.Body)
	}
	return &upstreamResponse{body: slices.Clone(response.Body), header: response.Headers.Clone(), usage: parsed.Usage}, sent.Load(), nil
}

// sendPassthroughStream 发起同协议流式请求并预读首个有效事件, 首个事件通过验证才算本轮取得可提交响应。
func sendPassthroughStream(ctx context.Context, format llm.APIFormat, request *httpclient.Request, client *http.Client) (*upstreamResponse, error) {
	rawRequest, err := httpclient.BuildHttpRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	// 客户端的 Accept 属于库自管头不会透传, 需显式声明才能让上游按 SSE 返回。
	rawRequest.Header.Set("Accept", "text/event-stream")

	response, err := client.Do(rawRequest)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		failure, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("upstream responded %s: %s", response.Status, failure)
	}

	events := httpclient.NewDefaultSSEDecoder(ctx, response.Body)
	for events.Next() {
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			events.Close()
			return nil, fmt.Errorf("%w: %s", err, event.Data)
		}
		return &upstreamResponse{header: response.Header.Clone(), events: events, first: event, last: last}, nil
	}

	err = events.Err()
	events.Close()
	if err == nil {
		err = errors.New("upstream stream ended before first event")
	}
	return nil, err
}

// conversionMiddleware 保存跨协议 pipeline 单次调用需要应用和取得的状态。
type conversionMiddleware struct {
	pipeline.DummyMiddleware // 提供本次无需处理的其余 pipeline 中间件方法。
	channel model.Channel // 本轮上游请求使用的渠道配置。
	format  llm.APIFormat // 上游渠道协议, 用于校验统一响应终态。
	rawBody []byte        // 上游非流式响应或错误的原始正文。
	usage   *llm.Usage    // 非流式统一响应中确认的用量。
}

// OnOutboundRawRequest 在转换后的上游请求上应用渠道参数和自定义 Header。
func (m *conversionMiddleware) OnOutboundRawRequest(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	return request, applyChannelConfig(m.channel, request)
}

// OnOutboundRawError 保留上游错误状态码携带的原始正文。
func (m *conversionMiddleware) OnOutboundRawError(_ context.Context, err error) {
	var failure *httpclient.Error
	if errors.As(err, &failure) {
		m.rawBody = slices.Clone(failure.Body)
	}
}

// OnOutboundRawResponse 保留上游成功响应的原始正文, 供后续转换或终态校验失败时诊断。
func (m *conversionMiddleware) OnOutboundRawResponse(_ context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.rawBody = slices.Clone(response.Body)
	return response, nil
}

// OnOutboundLlmResponse 取得非流式用量并在回转客户端协议前校验上游终态。
func (m *conversionMiddleware) OnOutboundLlmResponse(_ context.Context, response *llm.Response) (*llm.Response, error) {
	if err := validateResponse(m.format, response); err != nil {
		return nil, err
	}
	m.usage = response.Usage
	return response, nil
}

// sendConverted 经 axonhub pipeline 把客户端请求转换成渠道协议后请求上游, 响应再转换回客户端协议。
// 第二个返回值说明本轮是否真的向上游发出了请求; 转换与渠道参数应用都发生在 pipeline 内部,
// 故只能由传输层上报, 不能按 pipeline 是否返回错误判断。
func sendConverted(ctx context.Context, format llm.APIFormat, raw *httpclient.Request, channel model.Channel, outbound transformer.Outbound, streaming bool) (*upstreamResponse, bool, error) {
	var inbound transformer.Inbound
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
	default:
		inbound = openai.NewInboundTransformer()
	}

	httpClient, closeIdle, err := resolveUpstreamClient(channel)
	if err != nil {
		return nil, false, err
	}
	// 传输层上报必须在发起之前装好, 其后的失败一律算已发出。
	httpClient, sent := trackUpstreamRoundTrip(httpClient)

	// 流式响应要等消费方读完才能归还连接, 故只在提交流式结果那一处移交, 其余出口一律就地归还。
	committed := false
	if closeIdle != nil {
		defer func() {
			if !committed {
				closeIdle()
			}
		}()
	}
	middleware := &conversionMiddleware{channel: channel, format: outbound.APIFormat()}
	processor := pipeline.NewFactory(httpclient.NewHttpClientWithClient(httpClient)).Pipeline(
		inbound,
		outbound,
		pipeline.WithMiddlewares(middleware),
	)
	result, err := processor.Process(ctx, raw)
	if err != nil {
		if len(middleware.rawBody) > 0 {
			return nil, sent.Load(), fmt.Errorf("%w: %s", err, middleware.rawBody)
		}
		return nil, sent.Load(), err
	}
	if !streaming {
		return &upstreamResponse{body: slices.Clone(result.Response.Body), usage: middleware.usage}, sent.Load(), nil
	}

	events := result.EventStream
	for events.Next() {
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			events.Close()
			return nil, sent.Load(), fmt.Errorf("%w: %s", err, event.Data)
		}
		committed = true
		return &upstreamResponse{events: events, first: event, last: last, closeIdle: closeIdle}, sent.Load(), nil
	}

	err = events.Err()
	events.Close()
	if err == nil {
		err = errors.New("upstream stream ended before first event")
	}
	return nil, sent.Load(), err
}
