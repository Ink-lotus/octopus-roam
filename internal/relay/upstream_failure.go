package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/tidwall/gjson"
)

const (
	maxUpstreamFailureBody       = 64 << 10
	maxUpstreamFailureMessage    = 4096
	maxUpstreamFailureIdentifier = 256
)

type UpstreamFailureSource string

const (
	UpstreamFailureHTTP  UpstreamFailureSource = "http_error"
	UpstreamFailureEvent UpstreamFailureSource = "upstream_error"
)

// UpstreamFailure 是原始上游失败的值快照，不从诊断字符串或客户端转换结果
// 反推 code/type。Generation 由发送前的 CooldownToken 绑定，未接入时为 0。
type UpstreamFailure struct {
	ChannelID        int
	UpstreamModel    string
	Protocol         model.Protocol
	Generation       uint64
	HTTPStatus       int
	Code             string
	Type             string
	Message          string
	Source           UpstreamFailureSource
	MessageTruncated bool
	confirmed        bool // 只有原始 HTTP 失败或协议错误对象/事件能设置。
}

func (f UpstreamFailure) eligible() bool {
	if !f.confirmed || f.ChannelID <= 0 || f.UpstreamModel == "" || f.Generation == 0 {
		return false
	}
	switch f.Protocol {
	case model.ProtocolOpenAIChatCompletion, model.ProtocolOpenAIResponse, model.ProtocolAnthropicMessage:
	default:
		return false
	}
	return (f.Source == UpstreamFailureHTTP && f.HTTPStatus >= 400) || f.Source == UpstreamFailureEvent
}

type upstreamFailureError struct {
	cause   error
	failure UpstreamFailure
}

func (e *upstreamFailureError) Error() string { return e.cause.Error() }
func (e *upstreamFailureError) Unwrap() error { return e.cause }

// UpstreamFailureFromError 保留原 error 的 Error/Is/As 语义，同时返回快照副本。
func UpstreamFailureFromError(err error) (UpstreamFailure, bool) {
	var wrapped *upstreamFailureError
	if errors.As(err, &wrapped) {
		return wrapped.failure, true
	}
	return UpstreamFailure{}, false
}

type cooldownAttemptContextKey struct{}

// W06 在取得一致的配置/凭据快照并 Capture 后，向本次发送上下文绑定令牌。
// 发送端还会核对真实渠道和最终请求的 model，不接受不对应的代次。
func withCooldownAttempt(ctx context.Context, token CooldownToken) context.Context {
	return context.WithValue(ctx, cooldownAttemptContextKey{}, token)
}

type upstreamFailureCapture struct {
	mu       sync.Mutex
	ctx      context.Context
	token    CooldownToken
	identity UpstreamFailure
	failure  *UpstreamFailure
}

func newUpstreamFailureCapture(ctx context.Context, channelID int, format llm.APIFormat, upstreamModel string) *upstreamFailureCapture {
	capture := &upstreamFailureCapture{ctx: ctx, identity: UpstreamFailure{ChannelID: channelID}}
	capture.token, _ = ctx.Value(cooldownAttemptContextKey{}).(CooldownToken)
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		capture.identity.Protocol = model.ProtocolOpenAIChatCompletion
	case llm.APIFormatOpenAIResponse:
		capture.identity.Protocol = model.ProtocolOpenAIResponse
	case llm.APIFormatAnthropicMessage:
		capture.identity.Protocol = model.ProtocolAnthropicMessage
	}
	capture.requestModel(upstreamModel)
	return capture
}

func upstreamRequestModel(request *httpclient.Request) string {
	if request == nil {
		return ""
	}
	// 只取得 model 身份；请求消息和 prompt 永不送入失败提取或匹配器。
	value := gjson.GetBytes(request.Body, "model")
	if value.Type != gjson.String {
		return ""
	}
	return value.Str
}

func (c *upstreamFailureCapture) requestModel(upstreamModel string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.identity.UpstreamModel = strings.Clone(upstreamModel)
	c.identity.Generation = 0
	if c.token.channelID == c.identity.ChannelID && c.token.modelName == upstreamModel {
		c.identity.Generation = c.token.generation
	}
}

func (c *upstreamFailureCapture) responseStatus(status int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.identity.HTTPStatus = status
	c.mu.Unlock()
}

func (c *upstreamFailureCapture) response(body []byte, contentType string) {
	c.observe(body, "", false, contentType)
}

func (c *upstreamFailureCapture) streamEvent(event *httpclient.StreamEvent) {
	if event != nil {
		c.observe(event.Data, event.Type, true, "")
	}
}

func (c *upstreamFailureCapture) observe(body []byte, eventType string, streaming bool, contentType string) {
	if c == nil || c.ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure != nil || c.ctx.Err() != nil {
		return
	}
	if failure, ok := extractUpstreamFailure(c.identity, body, eventType, streaming, contentType); ok {
		c.failure = &failure
	}
}

func (c *upstreamFailureCapture) snapshot() (UpstreamFailure, bool) {
	if c == nil {
		return UpstreamFailure{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failure == nil {
		return UpstreamFailure{}, false
	}
	return *c.failure, true
}

func (c *upstreamFailureCapture) wrap(err error) error {
	if c == nil || err == nil || c.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := UpstreamFailureFromError(err); ok {
		return err
	}
	// HTTP 错误体读取失败仍保留传输层已经确认的实际状态。没有响应的
	// 连接失败/超时以及普通响应的本地解析错误不会据此产生快照。
	c.response(nil, "")
	if failure, ok := c.snapshot(); ok {
		return &upstreamFailureError{cause: err, failure: failure}
	}
	return err
}

func (r *upstreamResponse) upstreamFailure() (UpstreamFailure, bool) {
	if r == nil {
		return UpstreamFailure{}, false
	}
	return r.failure.snapshot()
}

// 观察原始事件但不消费、过滤、改写或合成事件；原有启动缓冲和终态校验
// 继续决定请求行为。保留底层的交付确认、终态边界和结算等待能力。
type upstreamFailureStream struct {
	streams.Stream[*httpclient.StreamEvent]
	capture *upstreamFailureCapture
}

func (s *upstreamFailureStream) Next() bool {
	if !s.Stream.Next() {
		return false
	}
	s.capture.streamEvent(s.Stream.Current())
	return true
}

func observeUpstreamFailureStream(stream streams.Stream[*httpclient.StreamEvent], capture *upstreamFailureCapture) streams.Stream[*httpclient.StreamEvent] {
	if capture == nil {
		return stream
	}
	return preserveDeliveryCapabilities(stream, &upstreamFailureStream{Stream: stream, capture: capture})
}

// extractUpstreamFailure 只解析最多 64 KiB 的原始响应/单事件，仅读取协议
// 指定的错误字段。正常输出、任意业务 message、incomplete reason 不参与。
func extractUpstreamFailure(identity UpstreamFailure, body []byte, eventType string, streaming bool, contentType string) (UpstreamFailure, bool) {
	failure := identity
	failure.Code, failure.Type, failure.Message = "", "", ""
	failure.Source, failure.confirmed, failure.MessageTruncated = "", false, false
	body = body[:min(len(body), maxUpstreamFailureBody)]
	root, validObject := rawFailureObject(body)
	var fields map[string]json.RawMessage
	suppressType := false
	if failure.HTTPStatus >= 400 {
		failure.Source = UpstreamFailureHTTP
		fields, _ = rawFailureObject(root["error"])
		if fields == nil && failure.Protocol == model.ProtocolOpenAIResponse {
			response, _ := rawFailureObject(root["response"])
			fields, _ = rawFailureObject(response["error"])
		}
		if !validObject && !looksLikeFailureJSON(body, contentType) {
			failure.Message, failure.MessageTruncated = boundedFailureMessage(string(body))
		}
	} else if streaming {
		kind := rawFailureString(root["type"])
		switch failure.Protocol {
		case model.ProtocolOpenAIChatCompletion:
			var hasError bool
			fields, hasError = rawFailureObject(root["error"])
			if eventType != "error" && !hasError {
				return UpstreamFailure{}, false
			}
		case model.ProtocolOpenAIResponse:
			switch {
			case kind == "error" || (eventType == "error" && !strings.HasPrefix(kind, "response.")):
				fields, _ = rawFailureObject(root["error"])
				if fields == nil {
					fields, suppressType = root, kind == "error"
				}
			case kind == "response.failed" || kind == "response.incomplete" || kind == "response.cancelled":
				response, _ := rawFailureObject(root["response"])
				fields, _ = rawFailureObject(response["error"])
			case kind == "response.completed":
				response, _ := rawFailureObject(root["response"])
				status := rawFailureString(response["status"])
				if status == "" || status == "completed" {
					return UpstreamFailure{}, false
				}
				fields, _ = rawFailureObject(response["error"])
			default:
				return UpstreamFailure{}, false
			}
		case model.ProtocolAnthropicMessage:
			if kind != "error" || (eventType != "" && eventType != kind) {
				return UpstreamFailure{}, false
			}
			fields, _ = rawFailureObject(root["error"])
		default:
			return UpstreamFailure{}, false
		}
		failure.Source = UpstreamFailureEvent
	} else {
		if !validObject {
			return UpstreamFailure{}, false
		}
		var hasError bool
		fields, hasError = rawFailureObject(root["error"])
		switch failure.Protocol {
		case model.ProtocolOpenAIChatCompletion:
			if !hasError {
				return UpstreamFailure{}, false
			}
		case model.ProtocolOpenAIResponse:
			response, _ := rawFailureObject(root["response"])
			if !hasError {
				fields, hasError = rawFailureObject(response["error"])
			}
			if !hasError && !failedResponseStatus(root["status"]) && !failedResponseStatus(response["status"]) {
				return UpstreamFailure{}, false
			}
		case model.ProtocolAnthropicMessage:
			if rawFailureString(root["type"]) != "error" {
				return UpstreamFailure{}, false
			}
		default:
			return UpstreamFailure{}, false
		}
		failure.Source = UpstreamFailureEvent
	}
	failure.confirmed = true
	failure.Code = rawFailureCode(fields["code"])
	if !suppressType {
		failure.Type = boundedFailureIdentifier(rawFailureString(fields["type"]))
	}
	if fields != nil {
		failure.Message, failure.MessageTruncated = boundedFailureMessage(rawFailureString(fields["message"]))
	}
	return failure, true
}

func rawFailureObject(data []byte) (map[string]json.RawMessage, bool) {
	var object map[string]json.RawMessage
	if !utf8.Valid(data) || json.Unmarshal(data, &object) != nil || object == nil {
		return nil, false
	}
	return object, true
}

func rawFailureString(data []byte) string {
	var value string
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	return value
}

func failedResponseStatus(data []byte) bool {
	switch rawFailureString(data) {
	case "failed", "incomplete", "cancelled":
		return true
	default:
		return false
	}
}

func looksLikeFailureJSON(body []byte, contentType string) bool {
	body = bytes.TrimSpace(body)
	return strings.Contains(strings.ToLower(contentType), "json") || json.Valid(body) ||
		(len(body) > 0 && (body[0] == '{' || body[0] == '[' || body[0] == '"'))
}

func boundedFailureIdentifier(value string) string {
	if len(value) > maxUpstreamFailureIdentifier {
		return ""
	}
	return strings.Clone(value)
}

func boundedFailureMessage(value string) (string, bool) {
	value = strings.ToValidUTF8(value, "\uFFFD")
	truncated := len(value) > maxUpstreamFailureMessage
	if truncated {
		value = value[:maxUpstreamFailureMessage]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.Clone(value), truncated
}

func rawFailureCode(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	if value := rawFailureString(data); value != "" {
		return boundedFailureIdentifier(value)
	}
	var number json.Number
	if json.Unmarshal(data, &number) != nil || number == "" {
		return ""
	}
	// Decimal expansion avoids float64 rounding and makes 4.29e2 match 429.
	// The output limit is checked before allocating exponent-sized strings.
	value := number.String()
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	mantissa, exponentText := value, "0"
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		mantissa, exponentText = value[:index], value[index+1:]
	}
	point := strings.IndexByte(mantissa, '.')
	if point < 0 {
		point = len(mantissa)
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	trimmed := strings.TrimLeft(digits, "0")
	if trimmed == "" {
		return "0"
	}
	exponent, err := strconv.ParseInt(exponentText, 10, 32)
	if err != nil || exponent > maxUpstreamFailureBody || exponent < -maxUpstreamFailureBody {
		return ""
	}
	point += int(exponent) - (len(digits) - len(trimmed))
	digits = strings.TrimRight(trimmed, "0")
	var result string
	switch {
	case point <= 0:
		if 2-point+len(digits) > maxUpstreamFailureIdentifier {
			return ""
		}
		result = "0." + strings.Repeat("0", -point) + digits
	case point >= len(digits):
		if point > maxUpstreamFailureIdentifier {
			return ""
		}
		result = digits + strings.Repeat("0", point-len(digits))
	default:
		if len(digits)+1 > maxUpstreamFailureIdentifier {
			return ""
		}
		result = digits[:point] + "." + digits[point:]
	}
	if negative {
		result = "-" + result
	}
	return boundedFailureIdentifier(result)
}
