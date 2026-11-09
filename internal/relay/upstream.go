package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/rhttp"
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
// 透传响应保留上游响应头; 跨协议响应由客户端协议决定响应头。失败以 error 返回,
// 仅在真实源需要异步结算时额外携带已关闭源的 finalize, 不携带可写出的响应。
type upstreamResponse struct {
	body           []byte                                  // 非流式响应的完整正文。
	header         http.Header                             // 同协议透传时需要原样返回的上游响应头。
	events         streams.Stream[*httpclient.StreamEvent] // 原始流及其可选能力; 先重放 buffered 再继续读取。
	first          *httpclient.StreamEvent                 // 已预读并验证的首个事件。
	last           bool                                    // 首个事件已经终止整个响应流。
	usage          *llm.Usage                              // 上游本次可确认的用量。
	buffered       []*httpclient.StreamEvent               // first 之后已预读的事件, 写出时逐项释放引用。
	sourceComplete bool                                    // 预读已确认源成功终态, 独立于客户端是否交付。
	// closeIdle 非 nil 时为渠道专用代理独占客户端的空闲连接归还入口, 消费方读完事件流后必须调用。
	// 仅流式响应会带上它: 非流式响应返回时连接已经用完, 由发起方就地归还。
	closeIdle func()
	terminal  *validatedStream                        // 原始上游终态，转换器生成的客户端结束事件不能替代它。
	converted *conversionMiddleware                   // 转换前已观察用量与源流推进入口。
	finalize  streams.Stream[*httpclient.StreamEvent] // 预读失败后, 待 handler 释放容量再等待的真实源结算。
	failure   *upstreamFailureCapture                 // 原始上游失败，包括提交后才观察到的错误。
}

// resolveUpstreamClient 按渠道代理配置取得本轮上游请求使用的客户端。
// 第二个返回值非 nil 时说明客户端是为渠道专用代理新建的独占实例, 不进共享连接池, 用完须调用它归还空闲连接。
func resolveUpstreamClient(channel model.Channel, contexts ...context.Context) (*http.Client, func(), error) {
	switch {
	case !channel.Proxy:
		client, err := rhttp.Direct()
		return client, nil, err
	case channel.ChannelProxy == "":
		if len(contexts) > 0 {
			if attempt, ok := contexts[0].Value(cooldownSendContextKey{}).(cooldownSendContext); ok {
				client, err := rhttp.ProxyForURL(attempt.config.ProxyURL)
				return client, nil, err
			}
		}
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
	inner   http.RoundTripper
	sent    *atomic.Bool
	failure *upstreamFailureCapture
}

// RoundTrip 标记本轮已进入 HTTP 传输后再交给真正的传输层。
func (t *trackingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := checkCooldownSend(request.Context(), t.failure); err != nil {
		return nil, err
	}
	t.sent.Store(true)
	response, err := t.inner.RoundTrip(request)
	if response != nil {
		t.failure.responseStatus(response.StatusCode)
	}
	return response, err
}

// trackUpstreamRoundTrip 返回一个上报"是否已发起 HTTP 传输"的客户端副本及其标志。
// 只包一层传输层: 原客户端的连接池与归还入口仍指向同一个传输层, 故并发与连接复用行为不变。
func trackUpstreamRoundTrip(client *http.Client, failure *upstreamFailureCapture) (*http.Client, *atomic.Bool) {
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	sent := &atomic.Bool{}
	tracked := *client
	tracked.Transport = &trackingTransport{inner: transport, sent: sent, failure: failure}
	return &tracked, sent
}

// sendPassthrough 以同协议透传方式请求上游, 取得的响应无需转换即可回给客户端。
// 第二个返回值说明本轮是否真的向上游发出了请求, 供调用方判定该失败是否消耗成员的尝试次数。
func sendPassthrough(ctx context.Context, format llm.APIFormat, raw *httpclient.Request, channel model.Channel, outbound transformer.Outbound, streaming bool, modelName string) (*upstreamResponse, bool, error) {
	request, err := buildPassthroughRequest(format, raw, channel, outbound, modelName)
	if err != nil {
		return nil, false, err
	}
	httpClient, closeIdle, err := resolveUpstreamClient(channel, ctx)
	if err != nil {
		return nil, false, err
	}
	// 传输层上报必须在发起之前装好, 其后的失败一律算已发出。
	failureCapture := newUpstreamFailureCapture(ctx, channel.ID, format, upstreamRequestModel(request))
	httpClient, sent := trackUpstreamRoundTrip(httpClient, failureCapture)

	if streaming {
		// 流式响应返回后仍在读上游连接, 归还入口随响应交给消费方; 取不到响应时就地归还。
		result, err := sendPassthroughStream(ctx, format, request, httpClient, failureCapture)
		if err != nil {
			if closeIdle != nil {
				closeIdle()
			}
			return nil, sent.Load(), failureCapture.wrap(err)
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
		if errors.As(err, &failure) {
			failureCapture.response(failure.Body, failure.Headers.Get("Content-Type"))
			if len(failure.Body) > 0 {
				err = fmt.Errorf("%w: %s", err, failure.Body)
			}
		}
		return nil, sent.Load(), failureCapture.wrap(err)
	}
	// 同协议下响应可原样回给客户端, 仍需解析一次以取得用量并识别以 200 下发的失败终态。
	failureCapture.response(response.Body, response.Headers.Get("Content-Type"))
	parsed, err := outbound.TransformResponse(ctx, response)
	if err != nil {
		return nil, sent.Load(), failureCapture.wrap(fmt.Errorf("%w: %s", err, response.Body))
	}
	if err := validateResponse(format, parsed); err != nil {
		return nil, sent.Load(), failureCapture.wrap(fmt.Errorf("%w: %s", err, response.Body))
	}
	return &upstreamResponse{body: slices.Clone(response.Body), header: response.Headers.Clone(), usage: parsed.Usage, failure: failureCapture}, sent.Load(), nil
}

// sendPassthroughStream 暂存无输出启动事件, 直到可提交输出或合法成功终态; 此前失败仍可重试。
func sendPassthroughStream(ctx context.Context, format llm.APIFormat, request *httpclient.Request, client *http.Client, failureCapture *upstreamFailureCapture) (*upstreamResponse, error) {
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
		failureCapture.response(failure, response.Header.Get("Content-Type"))
		if readErr != nil {
			return nil, readErr
		}
		return nil, fmt.Errorf("upstream responded %s: %s", response.Status, failure)
	}

	events := observeUpstreamFailureStream(httpclient.NewDefaultSSEDecoder(ctx, response.Body), failureCapture)
	result, err := readStreamStartup(ctx, format, events)
	if err != nil {
		return nil, err
	}
	result.header = response.Header.Clone()
	result.failure = failureCapture
	return result, nil
}

// conversionMiddleware 保存跨协议 pipeline 单次调用需要应用和取得的状态。
type conversionMiddleware struct {
	pipeline.DummyMiddleware               // 提供本次无需处理的其余 pipeline 中间件方法。
	channel                  model.Channel // 本轮上游请求使用的渠道配置。
	format                   llm.APIFormat // 上游渠道协议, 用于校验统一响应终态。
	clientFormat             llm.APIFormat // 客户端协议, 用于保留转换器尚未映射的拒绝文本。
	clientUserAgent          string        // 客户端原始 User-Agent; pipeline 合并入站请求时会过滤掉, 需在出站阶段补回。
	rawBody                  []byte        // 上游非流式响应或错误的原始正文。
	usage                    *llm.Usage    // 最近观察到的统一响应用量。
	finalUsage               *llm.Usage    // 已预读完整空成功时的最终快照, 不被缓存重放倒退。
	terminal                 *validatedStream
	rawStream                streams.Stream[*httpclient.StreamEvent]
	llmStream                streams.Stream[*llm.Response]
	observed                 *conversionLlmStream
	failure                  *upstreamFailureCapture
}

// Observe the provider protocol before conversion can consume or synthesize its
// terminal events. EOF without a protocol terminator is an upstream failure.
type validatedStream struct {
	streams.Stream[*httpclient.StreamEvent]
	format    llm.APIFormat
	complete  bool
	err       error
	closeOnce sync.Once
	closeErr  error
}

func (s *validatedStream) Next() bool {
	if s.complete || s.err != nil {
		return false
	}
	if !s.Stream.Next() {
		s.err = s.Stream.Err()
		if s.err == nil {
			s.err = io.ErrUnexpectedEOF
		}
		return false
	}
	last, err := inspectStreamEvent(s.format, s.Stream.Current())
	s.err = err
	s.complete = last && err == nil
	return err == nil
}

func (s *validatedStream) Err() error { return s.err }

// Pipeline pre-read failure can close both the adapted and original stream.
// The provider connection and any source-owned accounting have one owner.
func (s *validatedStream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.Stream.Close() })
	return s.closeErr
}

func (m *conversionMiddleware) OnOutboundRawStream(_ context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	stream = observeUpstreamFailureStream(stream, m.failure)
	m.terminal = &validatedStream{Stream: stream, format: m.format}
	m.rawStream = preserveDeliveryCapabilities(stream, m.terminal)
	if m.format == llm.APIFormatOpenAIResponse {
		m.rawStream = preserveDeliveryCapabilities(m.rawStream, streams.Map(m.rawStream, func(event *httpclient.StreamEvent) *httpclient.StreamEvent {
			// The fixed Responses transformer omits refusal deltas. Feed their
			// text through its text path; the original event remains observable
			// on terminal, and Chat's refusal field is restored below.
			var parsed responses.StreamEvent
			if event == nil || json.Unmarshal(event.Data, &parsed) != nil || parsed.Type != "response.refusal.delta" {
				return event
			}
			parsed.Type = responses.StreamEventTypeOutputTextDelta
			data, err := json.Marshal(parsed)
			if err != nil {
				return event
			}
			adapted := *event
			adapted.Type, adapted.Data = string(parsed.Type), data
			return &adapted
		}))
	}
	return m.rawStream, nil
}

func (m *conversionMiddleware) OnOutboundLlmStream(_ context.Context, stream streams.Stream[*llm.Response]) (streams.Stream[*llm.Response], error) {
	m.observed = &conversionLlmStream{Stream: stream}
	observed := streams.Map(m.observed, func(response *llm.Response) *llm.Response {
		if response == nil {
			return nil
		}
		if response.Usage != nil {
			usage := *response.Usage
			m.usage = &usage
		}
		var sourceRefusal bool
		if m.format == llm.APIFormatOpenAIResponse && m.terminal != nil && m.terminal.Current() != nil {
			var source struct{ Type string }
			if json.Unmarshal(m.terminal.Current().Data, &source) == nil {
				sourceRefusal = source.Type == "response.refusal.delta"
			}
		}
		var sourceToolArguments bool
		if m.format == llm.APIFormatAnthropicMessage && m.clientFormat == llm.APIFormatOpenAIChatCompletion && m.terminal != nil && m.terminal.Current() != nil {
			var source struct {
				Type  string
				Delta struct{ Type string }
			}
			if json.Unmarshal(m.terminal.Current().Data, &source) == nil {
				sourceToolArguments = source.Type == "content_block_delta" && source.Delta.Type == "input_json_delta"
			}
		}
		for index, choice := range response.Choices {
			if choice.Delta == nil {
				continue
			}
			if (sourceRefusal && m.clientFormat == llm.APIFormatOpenAIChatCompletion) || (choice.Delta.Refusal != "" && m.clientFormat != llm.APIFormatOpenAIChatCompletion) {
				adapted := *response
				adapted.Choices = slices.Clone(response.Choices)
				delta := *choice.Delta
				if sourceRefusal && m.clientFormat == llm.APIFormatOpenAIChatCompletion {
					if delta.Content.Content != nil {
						delta.Refusal = *delta.Content.Content
						delta.Content.Content = nil
					}
				} else {
					// Responses/Anthropic's fixed inbound transformers carry
					// visible refusal text through their text content blocks.
					text := delta.Refusal
					if delta.Content.Content != nil {
						text = *delta.Content.Content + text
					}
					delta.Content.Content, delta.Refusal = &text, ""
				}
				adapted.Choices[index].Delta = &delta
				response = &adapted
			}
			if sourceToolArguments && len(choice.Delta.ToolCalls) > 0 {
				// Anthropic's converter repeats full identity fields on argument
				// deltas. Chat clients concatenate those fields by tool index;
				// the preceding tool-use start already supplied their identity.
				adapted := *response
				adapted.Choices = slices.Clone(response.Choices)
				delta := *adapted.Choices[index].Delta
				delta.ToolCalls = slices.Clone(delta.ToolCalls)
				for toolIndex := range delta.ToolCalls {
					delta.ToolCalls[toolIndex].ID = ""
					delta.ToolCalls[toolIndex].Type = ""
				}
				adapted.Choices[index].Delta = &delta
				response = &adapted
			}
		}
		return response
	})
	m.llmStream = preserveDeliveryCapabilities(m.rawStream, preserveDeliveryCapabilities(stream, observed))
	return m.llmStream, nil
}

// The pipeline stops at a finish_reason, which can precede provider completion
// and final usage. For an empty result, prefetch that tail before committing,
// then replay it through the same converter without losing its usage events.
type conversionLlmStream struct {
	streams.Stream[*llm.Response]
	current                       *llm.Response
	pending                       []*llm.Response
	output                        bool
	metadataEvents, metadataBytes int
}

func (s *conversionLlmStream) Next() bool {
	fresh := len(s.pending) == 0
	if len(s.pending) > 0 {
		s.current = s.pending[0]
		s.pending[0] = nil
		s.pending = s.pending[1:]
	} else if s.Stream.Next() {
		s.current = s.Stream.Current()
	} else {
		return false
	}
	if response := s.current; response != nil {
		for _, choice := range response.Choices {
			for _, message := range []*llm.Message{choice.Delta, choice.Message} {
				if message != nil && ((message.Content.Content != nil && *message.Content.Content != "") ||
					len(message.Content.MultipleContent) > 0 || len(message.ToolCalls) > 0 || message.Refusal != "" ||
					(message.ReasoningContent != nil && *message.ReasoningContent != "") ||
					(message.Reasoning != nil && *message.Reasoning != "") ||
					(message.ReasoningSignature != nil && *message.ReasoningSignature != "") ||
					(message.Audio != nil && (message.Audio.Data != "" || message.Audio.Transcript != ""))) {
					s.output = true
				}
			}
		}
	}
	if fresh && !s.output {
		s.metadataEvents++
		encoded, err := json.Marshal(s.current)
		if err != nil {
			s.metadataBytes = maxStreamStartupBytes + 1
		} else {
			s.metadataBytes += len(encoded)
		}
	}
	return true
}

func (s *conversionLlmStream) Current() *llm.Response { return s.current }

func (m *conversionMiddleware) prefetchEmptyTail() error {
	var pending []*llm.Response
	for m.llmStream.Next() {
		event := m.llmStream.Current()
		pending = append(pending, event)
		if m.observed.output {
			m.observed.pending = pending
			return nil
		}
		// Prefix and tail share one metadata budget. A real provider terminal
		// (or meaningful output above) is a valid commitment boundary itself.
		if !m.terminal.complete && (m.observed.metadataEvents > maxStreamStartupEvents || m.observed.metadataBytes > maxStreamStartupBytes) {
			return pipeline.ErrPreCommitBufferExceeded
		}
	}
	if err := m.llmStream.Err(); err != nil {
		return err
	}
	if !m.terminal.complete {
		return io.ErrUnexpectedEOF
	}
	m.finalUsage = m.usage
	// The tail has already passed the observation/normalization map. Replaying
	// it preserves client wire order; usage is a snapshot, never an increment.
	m.observed.pending = pending
	return nil
}

func (s *conversionLlmStream) Close() error {
	clear(s.pending)
	s.pending, s.current = nil, nil
	return s.Stream.Close()
}

// Keep advancing the source transformer when the client converter terminates
// early. This observes final usage as well as the provider's real terminator.
func (m *conversionMiddleware) finishSource() error {
	for m.llmStream.Next() {
		// Map observes usage in Current, not Next. Drain queued LLM events
		// too: one raw terminal may produce several final usage chunks.
		_ = m.llmStream.Current()
	}
	if m.terminal.complete {
		return nil
	}
	if err := m.llmStream.Err(); err != nil {
		return err
	}
	for !m.terminal.complete && m.terminal.Err() == nil && m.terminal.Next() {
	}
	if m.terminal.complete {
		return nil
	}
	return m.terminal.Err()
}

func (m *conversionMiddleware) observedUsage() *llm.Usage {
	if m.finalUsage != nil {
		return m.finalUsage
	}
	return m.usage
}

func (m *conversionMiddleware) failedResponse() *upstreamResponse {
	if m.rawStream == nil {
		return nil
	}
	m.rawStream.Close()
	if _, ok := m.rawStream.(streams.FinalizationWaiter); ok {
		return &upstreamResponse{finalize: m.rawStream, failure: m.failure}
	}
	return nil
}

func (m *conversionMiddleware) OnInboundRawStream(_ context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*httpclient.StreamEvent], error) {
	return preserveDeliveryCapabilities(m.llmStream, stream), nil
}

// OnOutboundRawRequest 在转换后的上游请求上应用渠道参数和自定义 Header。
func (m *conversionMiddleware) OnOutboundRawRequest(ctx context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	// pipeline 的 MergeInboundRequest 已过滤客户端 User-Agent, 在应用渠道配置前补回, 供转发上游与占位符读取。
	preserveClientUserAgent(request, m.clientUserAgent)
	err := applyChannelConfig(m.channel, request)
	if err == nil {
		err = sanitizeResponsesRequestBody(m.channel, m.format, request)
	}
	if err == nil {
		m.failure.requestModel(upstreamRequestModel(request))
	}
	return request, err
}

// OnOutboundRawError 保留上游错误状态码携带的原始正文。
func (m *conversionMiddleware) OnOutboundRawError(_ context.Context, err error) {
	var failure *httpclient.Error
	if errors.As(err, &failure) {
		m.rawBody = slices.Clone(failure.Body)
		m.failure.response(failure.Body, failure.Headers.Get("Content-Type"))
	}
}

// OnOutboundRawResponse 保留上游成功响应的原始正文, 供后续转换或终态校验失败时诊断。
func (m *conversionMiddleware) OnOutboundRawResponse(_ context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.rawBody = slices.Clone(response.Body)
	m.failure.response(response.Body, response.Headers.Get("Content-Type"))
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

	httpClient, closeIdle, err := resolveUpstreamClient(channel, ctx)
	if err != nil {
		return nil, false, err
	}
	// 传输层上报必须在发起之前装好, 其后的失败一律算已发出。
	failureCapture := newUpstreamFailureCapture(ctx, channel.ID, outbound.APIFormat(), upstreamRequestModel(raw))
	httpClient, sent := trackUpstreamRoundTrip(httpClient, failureCapture)

	// 流式响应要等消费方读完才能归还连接, 故只在提交流式结果那一处移交, 其余出口一律就地归还。
	committed := false
	if closeIdle != nil {
		defer func() {
			if !committed {
				closeIdle()
			}
		}()
	}
	middleware := &conversionMiddleware{channel: channel, format: outbound.APIFormat(), clientFormat: format, clientUserAgent: raw.Headers.Get("User-Agent"), failure: failureCapture}
	// A redirect would issue another HTTP request inside one Forward attempt.
	// Keep the actual-send contract independent of both library retries and
	// net/http's automatic redirect policy.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	processor := pipeline.NewFactory(httpclient.NewHttpClientWithClient(httpClient)).Pipeline(
		inbound,
		outbound,
		// A positive budget enables startup pre-read. buildOutbound's concrete
		// transformers do not implement either pipeline retry interface; Forward
		// remains the sole retry scheduler (verified by real RoundTrip counts).
		pipeline.WithRetry(1, 0, 0),
		pipeline.WithMiddlewares(middleware),
	)
	result, err := processor.Process(ctx, raw)
	if err != nil {
		failed := middleware.failedResponse()
		if len(middleware.rawBody) > 0 {
			err = fmt.Errorf("%w: %s", err, middleware.rawBody)
		}
		return failed, sent.Load(), failureCapture.wrap(err)
	}
	if !streaming {
		return &upstreamResponse{body: slices.Clone(result.Response.Body), usage: middleware.usage, failure: failureCapture}, sent.Load(), nil
	}
	if !middleware.observed.output {
		if err := middleware.prefetchEmptyTail(); err != nil {
			result.EventStream.Close()
			return middleware.failedResponse(), sent.Load(), failureCapture.wrap(err)
		}
	}

	// Pipeline has confirmed LLM output, or the empty tail above has confirmed
	// provider completion. Its original buffer owns the startup budget; do not
	// apply a second budget to larger representations of those same events.
	events := result.EventStream
	for events.Next() {
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			events.Close()
			return middleware.failedResponse(), sent.Load(), failureCapture.wrap(fmt.Errorf("%w: %s", err, event.Data))
		}
		committed = true
		return &upstreamResponse{events: events, first: cloneStartupEvent(event), last: last,
			closeIdle: closeIdle, terminal: middleware.terminal, converted: middleware,
			sourceComplete: middleware.terminal.complete, usage: middleware.observedUsage(), failure: failureCapture}, sent.Load(), nil
	}
	err = events.Err()
	events.Close()
	if err == nil {
		err = errors.New("upstream stream ended before first event")
	}
	return middleware.failedResponse(), sent.Load(), failureCapture.wrap(err)
}
