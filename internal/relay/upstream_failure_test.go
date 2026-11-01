package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
)

const upstreamFailureTestBody = `{"error":{"code":"fixture_code","type":"fixture_type","message":"Fixture QUOTA reached"}}`

func upstreamFailureDirections() []conversionDirection {
	var directions []conversionDirection
	for _, client := range startupProtocols() {
		for _, upstream := range startupProtocols() {
			directions = append(directions, conversionDirection{client: client, upstream: upstream})
		}
	}
	return directions
}

type upstreamFailureSender struct {
	direction conversionDirection
	channel   model.Channel
	outbound  transformer.Outbound
	engine    *CooldownEngine
	token     CooldownToken
	clock     *cooldownTestClock
}

func newUpstreamFailureSender(t *testing.T, direction conversionDirection, url string) *upstreamFailureSender {
	t.Helper()
	channel := model.Channel{ID: 301, ChannelConfig: model.ChannelConfig{BaseURL: url, AllowedProtocols: direction.upstream.protocol}}
	outbound, protocol, passthrough, err := buildOutbound(channel, model.ChannelGrant{Protocols: direction.upstream.protocol},
		model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "fixture-credential"}}, direction.client.protocol)
	if err != nil || protocol != direction.upstream.protocol || passthrough != (direction.client.protocol == direction.upstream.protocol) {
		t.Fatalf("unexpected outbound selection: protocol=%v passthrough=%t error=%v", protocol, passthrough, err)
	}
	clock := newCooldownTestClock()
	engine := NewCooldownEngine(clock, nil)
	t.Cleanup(engine.Close)
	configureCooldownTest(t, engine, 301, 7, model.CooldownRule{Keyword: "quota", CooldownSeconds: 17, Scope: model.CooldownScopeModel})
	return &upstreamFailureSender{direction: direction, channel: channel, outbound: outbound, engine: engine,
		token: captureCooldownTest(t, engine, 301, 7, "fixture-model-a"), clock: clock}
}

func (s *upstreamFailureSender) send(t *testing.T, ctx context.Context, streaming bool) (*upstreamResponse, bool, error) {
	t.Helper()
	extra := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Fixture QUOTA reached"}}}
	if s.direction.client.format == llm.APIFormatOpenAIResponse {
		extra = map[string]any{"input": "Fixture QUOTA reached"}
	}
	req := compatRequest(t, model.Group{Name: s.token.modelName}, s.direction.client.format, streaming, extra)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw := &httpclient.Request{Method: req.Method, URL: req.URL.String(), Headers: req.Header.Clone(), Body: body}
	ctx = withCooldownAttempt(ctx, s.token)
	if s.direction.client.protocol == s.direction.upstream.protocol {
		return sendPassthrough(ctx, s.direction.client.format, raw, s.channel, s.outbound, streaming, s.token.modelName)
	}
	return sendConverted(ctx, s.direction.client.format, raw, s.channel, s.outbound, streaming)
}

func finishUpstreamFailureResponse(result *upstreamResponse) {
	if result == nil {
		return
	}
	if result.events != nil {
		for result.events.Next() {
			_ = result.events.Current()
		}
		if result.converted != nil {
			_ = result.converted.finishSource()
		}
		result.events.Close()
		waitForStreamFinalization(result.events)
	}
	if result.closeIdle != nil {
		result.closeIdle()
	}
	waitForStreamFinalization(result.finalize)
}

func assertUpstreamFailureIdentity(t *testing.T, failure UpstreamFailure, direction conversionDirection, status int, source UpstreamFailureSource) {
	t.Helper()
	if failure.ChannelID != 301 || failure.UpstreamModel != "fixture-model-a" || failure.Generation != 7 ||
		failure.Protocol != direction.upstream.protocol || failure.HTTPStatus != status || failure.Source != source ||
		failure.Code != "fixture_code" || failure.Type != "fixture_type" || failure.Message != "Fixture QUOTA reached" {
		t.Fatalf("raw upstream identity/fields changed: %+v", failure)
	}
}

func TestUpstreamFailureHTTPDirections(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", direction.name(), streaming), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var request struct{ Model string }
					if json.NewDecoder(r.Body).Decode(&request) != nil || request.Model != "fixture-model-a" {
						t.Error("actual request did not use the upstream model")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Retry-After", "600")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, upstreamFailureTestBody)
				}))
				defer server.Close()
				sender := newUpstreamFailureSender(t, direction, server.URL)
				result, sent, err := sender.send(t, t.Context(), streaming)
				defer finishUpstreamFailureResponse(result)
				failure, ok := UpstreamFailureFromError(err)
				if err == nil || !sent || calls.Load() != 1 || !ok {
					t.Fatalf("HTTP failure lost: sent=%t calls=%d snapshot=%t error=%v", sent, calls.Load(), ok, err)
				}
				assertUpstreamFailureIdentity(t, failure, direction, 429, UpstreamFailureHTTP)
				if len(sender.engine.Snapshot(301)) != 0 {
					t.Fatal("W05 send path implicitly recorded production cooldown")
				}
				if !sender.engine.Record(sender.token, failure) || !sender.engine.Observe(301, "fixture-model-a").Until.Equal(sender.clock.Now().Add(17*time.Second)) {
					t.Fatal("Retry-After replaced the configured duration")
				}
			})
		}
	}
}

func TestUpstreamFailureOrdinary200Directions(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		t.Run(direction.name(), func(t *testing.T) {
			body := upstreamFailureTestBody
			if direction.upstream.protocol == model.ProtocolOpenAIResponse {
				body = `{"id":"resp-fixture","object":"response","status":"failed","output":[],"error":{"code":"fixture_code","type":"fixture_type","message":"Fixture QUOTA reached"}}`
			} else if direction.upstream.protocol == model.ProtocolAnthropicMessage {
				body = upstreamFailureEvent(direction.upstream.protocol)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			sender := newUpstreamFailureSender(t, direction, server.URL)
			result, sent, err := sender.send(t, t.Context(), false)
			defer finishUpstreamFailureResponse(result)
			failure, ok := UpstreamFailureFromError(err)
			if err == nil {
				failure, ok = result.upstreamFailure()
			}
			if !sent || !ok {
				t.Fatalf("ordinary HTTP 200 failure lost raw fields: %v", err)
			}
			assertUpstreamFailureIdentity(t, failure, direction, 200, UpstreamFailureEvent)
			t.Logf("original send error returned=%t; snapshot retained independently", err != nil)
		})
	}
}

func TestUpstreamFailureHTTPStatusAfterBodyReadError(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", direction.name(), streaming), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", "100")
					w.WriteHeader(429)
					_, _ = io.WriteString(w, "x")
				}))
				defer server.Close()
				sender := newUpstreamFailureSender(t, direction, server.URL)
				result, sent, err := sender.send(t, t.Context(), streaming)
				defer finishUpstreamFailureResponse(result)
				failure, ok := UpstreamFailureFromError(err)
				if err == nil || !sent || !ok || failure.HTTPStatus != 429 || failure.Source != UpstreamFailureHTTP || failure.Code != "" || failure.Type != "" {
					t.Fatalf("known HTTP status lost during body read error: snapshot=%+v error=%v", failure, err)
				}
			})
		}
	}
}

func upstreamFailureEvent(protocol model.Protocol) string {
	switch protocol {
	case model.ProtocolOpenAIResponse:
		return `{"type":"response.failed","response":{"status":"failed","error":{"code":"fixture_code","type":"fixture_type","message":"Fixture QUOTA reached"}}}`
	case model.ProtocolAnthropicMessage:
		return `{"type":"error","error":{"code":"fixture_code","type":"fixture_type","message":"Fixture QUOTA reached"}}`
	default:
		return upstreamFailureTestBody
	}
}

func TestUpstreamFailureStreamDirections(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, phase := range []string{"first", "startup", "later", "later-status-203"} {
			t.Run(direction.name()+"/"+phase, func(t *testing.T) {
				var events []string
				if phase != "first" {
					events = slices.Clone(direction.upstream.prelude)
				}
				later := strings.HasPrefix(phase, "later")
				if later {
					events = append(events, direction.upstream.content)
				}
				events = append(events, upstreamFailureEvent(direction.upstream.protocol))
				status := http.StatusOK
				if phase == "later-status-203" {
					status = http.StatusNonAuthoritativeInfo
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, compatSSE(events...))
				}))
				defer server.Close()
				sender := newUpstreamFailureSender(t, direction, server.URL)
				result, sent, err := sender.send(t, t.Context(), true)
				var failure UpstreamFailure
				var ok bool
				if later {
					if err != nil || result == nil || result.events == nil {
						t.Fatalf("meaningful output could not be committed: %v", err)
					}
					finishUpstreamFailureResponse(result)
					failure, ok = result.upstreamFailure()
				} else {
					finishUpstreamFailureResponse(result)
					if err == nil {
						t.Fatal("startup error was exposed as a successful response")
					}
					failure, ok = UpstreamFailureFromError(err)
				}
				if !sent || !ok {
					t.Fatalf("original streaming failure missing: sent=%t snapshot=%t error=%v", sent, ok, err)
				}
				assertUpstreamFailureIdentity(t, failure, direction, status, UpstreamFailureEvent)
				if len(sender.engine.Snapshot(301)) != 0 {
					t.Fatal("stream observation implicitly wrote cooldown state")
				}
			})
		}
	}
}

func upstreamFailureNormalBody(protocol model.Protocol) string {
	switch protocol {
	case model.ProtocolOpenAIResponse:
		return `{"id":"resp-fixture","object":"response","created_at":1,"model":"fixture-model-a","status":"completed","output":[{"type":"message","id":"msg-fixture","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Fixture QUOTA reached","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	case model.ProtocolAnthropicMessage:
		return `{"id":"msg-fixture","type":"message","role":"assistant","model":"fixture-model-a","content":[{"type":"text","text":"Fixture QUOTA reached"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	default:
		return strings.ReplaceAll(chatCompletionBody, "pong", "Fixture QUOTA reached")
	}
}

func TestUpstreamFailureNormalOutputAndSuccess(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream-%t", direction.name(), streaming), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, strings.ReplaceAll(direction.success(false), "pong", "Fixture QUOTA reached"))
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, upstreamFailureNormalBody(direction.upstream.protocol))
					}
				}))
				defer server.Close()
				sender := newUpstreamFailureSender(t, direction, server.URL)
				failure := failureForCooldownTest(t, sender.token, direction.upstream.protocol, 429, upstreamFailureTestBody)
				sender.engine.Record(sender.token, failure)
				before := sender.engine.Observe(301, "fixture-model-a")
				sender.clock.advance(5 * time.Second)
				result, sent, err := sender.send(t, t.Context(), streaming)
				if err != nil || !sent || result == nil {
					t.Fatalf("normal output rejected: sent=%t error=%v", sent, err)
				}
				finishUpstreamFailureResponse(result)
				if failure, ok := result.upstreamFailure(); ok {
					t.Fatalf("normal output/prompt was treated as an error: %+v", failure)
				}
				if !sender.engine.Observe(301, "fixture-model-a").Until.Equal(before.Until) {
					t.Fatal("successful request shortened configured cooldown")
				}
				assertCooldownSignal(t, before.Changed, false)
			})
		}
	}
}

func TestUpstreamFailureExcludedErrors(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		for _, mode := range []string{"local-override", "transport", "cancelled", "malformed-response", "malformed-stream", "EOF"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if mode == "EOF" || mode == "malformed-stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, compatSSE(direction.upstream.prelude...))
						if mode == "malformed-stream" {
							_, _ = io.WriteString(w, "data: {invalid-json\n\n")
						}
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"error":`)
					}
				}))
				defer server.Close()
				sender := newUpstreamFailureSender(t, direction, server.URL)
				if mode == "local-override" {
					sender.channel.ParamOverride = "{invalid-json"
				}
				if mode == "transport" {
					server.Close()
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if mode == "cancelled" {
					cancel()
				}
				result, sent, err := sender.send(t, ctx, mode == "EOF" || mode == "malformed-stream")
				finishUpstreamFailureResponse(result)
				if err == nil {
					t.Fatal("fixture did not fail")
				}
				if _, ok := UpstreamFailureFromError(err); ok {
					t.Fatalf("local/cancel/transport/EOF error became matchable: %v", err)
				}
				if _, ok := result.upstreamFailure(); ok || len(sender.engine.Snapshot(301)) != 0 {
					t.Fatal("excluded failure left a snapshot or cooldown")
				}
				if mode == "local-override" && sent {
					t.Fatal("local validation unexpectedly sent a request")
				}
			})
		}
	}
}

func TestUpstreamFailureCancellationDuringStartup(t *testing.T) {
	for _, direction := range upstreamFailureDirections() {
		t.Run(direction.name(), func(t *testing.T) {
			arrived := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatSSE(direction.upstream.prelude...))
				w.(http.Flusher).Flush()
				close(arrived)
				<-r.Context().Done()
			}))
			defer server.Close()
			sender := newUpstreamFailureSender(t, direction, server.URL)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type outcome struct {
				response *upstreamResponse
				sent     bool
				err      error
			}
			finished := make(chan outcome, 1)
			go func() {
				response, sent, err := sender.send(t, ctx, true)
				finished <- outcome{response, sent, err}
			}()
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture upstream did not receive the request")
			}
			cancel()
			select {
			case result := <-finished:
				finishUpstreamFailureResponse(result.response)
				if !result.sent || result.err == nil {
					t.Fatal("cancel did not end the pending startup")
				}
				if _, ok := UpstreamFailureFromError(result.err); ok {
					t.Fatal("client cancellation was treated as upstream failure")
				}
				if _, ok := result.response.upstreamFailure(); ok {
					t.Fatal("cancelled startup retained a matchable error")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled sender did not return")
			}
		})
	}
}

func TestUpstreamFailureModelProbeIsIndependent(t *testing.T) {
	var success atomic.Bool
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet {
			t.Error("model probe unexpectedly used a Relay request")
		}
		w.Header().Set("Content-Type", "application/json")
		if !success.Load() {
			w.WriteHeader(429)
			_, _ = io.WriteString(w, upstreamFailureTestBody)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"fixture-model-a"}]}`)
	}))
	defer server.Close()
	sender := newUpstreamFailureSender(t, upstreamFailureDirections()[0], server.URL)
	ctx := withCooldownAttempt(t.Context(), sender.token)
	result, err := op.ChannelFetchModelsWithClient(ctx, sender.channel.ChannelConfig, "fixture-credential", server.Client())
	if result.OpenAIError == "" || calls.Load() != 1 {
		t.Fatal("model probe failure fixture was not exercised")
	}
	if _, ok := UpstreamFailureFromError(err); ok || len(sender.engine.Snapshot(301)) != 0 {
		t.Fatal("model probe entered the Relay rule engine")
	}
	failure := failureForCooldownTest(t, sender.token, model.ProtocolOpenAIChatCompletion, 429, upstreamFailureTestBody)
	sender.engine.Record(sender.token, failure)
	before := sender.engine.Observe(301, sender.token.modelName)
	success.Store(true)
	result, err = op.ChannelFetchModelsWithClient(ctx, sender.channel.ChannelConfig, "fixture-credential", server.Client())
	if err != nil || result.OpenAIError != "" || len(result.Models) != 1 || calls.Load() != 2 {
		t.Fatalf("model probe success fixture failed: %v", err)
	}
	if !sender.engine.Observe(301, sender.token.modelName).Until.Equal(before.Until) {
		t.Fatal("model probe success cleared a user cooldown")
	}
	assertCooldownSignal(t, before.Changed, false)
}

func TestUpstreamFailureProtocolFields(t *testing.T) {
	for _, test := range []struct {
		name, event, body, code, kind, message string
		protocol                               model.Protocol
		stream                                 bool
		status                                 int
		want                                   bool
	}{
		{name: "HTTP-text", protocol: model.ProtocolOpenAIChatCompletion, status: 503, body: "Fixture quota unavailable", message: "Fixture quota unavailable", want: true},
		{name: "HTTP-unknown-JSON", protocol: model.ProtocolOpenAIChatCompletion, status: 429, body: `{"message":"quota","prompt":"quota","type":"vendor_json"}`, want: true},
		{name: "HTTP-invalid-JSON", protocol: model.ProtocolOpenAIResponse, status: 429, body: `{"error":{"message":"quota"`, want: true},
		{name: "missing-fields", protocol: model.ProtocolOpenAIChatCompletion, status: 429, body: `{"error":{}}`, want: true},
		{name: "identifiers-not-trimmed", protocol: model.ProtocolOpenAIChatCompletion, status: 429, body: `{"error":{"code":" fixture ","type":"MiXeD"}}`, code: " fixture ", kind: "MiXeD", want: true},
		{name: "chat-event-error", protocol: model.ProtocolOpenAIChatCompletion, status: 200, stream: true, event: "error", body: `{}`, want: true},
		{name: "responses-flat-event", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"error","code":"fixture","message":"quota"}`, code: "fixture", message: "quota", want: true},
		{name: "responses-flat-vendor-type", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, event: "error", body: `{"type":"vendor_error","code":"fixture","message":"quota"}`, code: "fixture", kind: "vendor_error", message: "quota", want: true},
		{name: "failed-no-fallback", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"response.failed","response":{"status":"failed"}}`, want: true},
		{name: "incomplete-no-reason-match", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"quota"}}}`, want: true},
		{name: "cancelled-upstream", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"response.cancelled","response":{"error":{"code":"vendor_cancel","message":"quota"}}}`, code: "vendor_cancel", message: "quota", want: true},
		{name: "completed-failure", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"response.completed","response":{"status":"failed","error":{"type":"vendor_error","message":"quota"}}}`, kind: "vendor_error", message: "quota", want: true},
		{name: "ordinary-responses-failed", protocol: model.ProtocolOpenAIResponse, status: 200, body: `{"status":"failed","error":{"code":"fixture","message":"quota"}}`, code: "fixture", message: "quota", want: true},
		{name: "ordinary-responses-wrapped", protocol: model.ProtocolOpenAIResponse, status: 200, body: `{"response":{"status":"failed","error":{"code":"fixture","type":"vendor_error","message":"quota"}}}`, code: "fixture", kind: "vendor_error", message: "quota", want: true},
		{name: "ordinary-anthropic-error", protocol: model.ProtocolAnthropicMessage, status: 200, body: `{"type":"error","error":{"code":429,"type":"overloaded_error","message":"quota"}}`, code: "429", kind: "overloaded_error", message: "quota", want: true},
		{name: "anthropic-type-mismatch", protocol: model.ProtocolAnthropicMessage, status: 200, stream: true, event: "message_start", body: `{"type":"error","error":{"message":"quota"}}`},
		{name: "anthropic-empty-type", protocol: model.ProtocolAnthropicMessage, status: 200, stream: true, body: `{"error":{"message":"quota"}}`},
		{name: "normal-JSON-prompt", protocol: model.ProtocolOpenAIChatCompletion, status: 200, body: `{"prompt":"quota","choices":[{"message":{"content":"quota"}}]}`},
		{name: "normal-completed", protocol: model.ProtocolOpenAIResponse, status: 200, stream: true, body: `{"type":"response.completed","response":{"status":"completed","output":[{"text":"quota"}]}}`},
		{name: "normal-message", protocol: model.ProtocolAnthropicMessage, status: 200, body: `{"type":"message","content":[{"type":"text","text":"quota"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := UpstreamFailure{ChannelID: 301, Generation: 7, UpstreamModel: "fixture-model-a", Protocol: test.protocol, HTTPStatus: test.status}
			failure, ok := extractUpstreamFailure(identity, []byte(test.body), test.event, test.stream, "")
			if ok != test.want || (ok && (failure.Code != test.code || failure.Type != test.kind || failure.Message != test.message)) {
				t.Fatalf("fields: ok=%t failure=%+v", ok, failure)
			}
			if ok && (failure.HTTPStatus != test.status || failure.UpstreamModel != identity.UpstreamModel) {
				t.Fatal("response body changed actual identity/status")
			}
		})
	}
}

func TestUpstreamFailureNumericCodes(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"429", "429"}, {"4.29e2", "429"}, {"429.000", "429"}, {"-4.29E+2", "-429"},
		{"1.50e-2", "0.015"}, {"9007199254740993", "9007199254740993"}, {"0e99999999", "0"},
		{"1e100000000", ""}, {"1e-300", ""}, {"null", ""}, {"true", ""}, {`"00429"`, "00429"},
	} {
		t.Run(test.raw, func(t *testing.T) {
			failure, ok := extractUpstreamFailure(UpstreamFailure{HTTPStatus: 429, Protocol: model.ProtocolOpenAIChatCompletion},
				[]byte(`{"error":{"code":`+test.raw+`}}`), "", false, "application/json")
			if !ok || failure.Code != test.want {
				t.Fatalf("numeric code = %q, want %q", failure.Code, test.want)
			}
		})
	}
}

func TestUpstreamFailureBounds(t *testing.T) {
	for _, test := range []struct {
		name, message, code, kind string
		wantLength                int
		truncated, discardID      bool
	}{
		{name: "exact", message: strings.Repeat("x", 4096), code: strings.Repeat("c", 256), kind: strings.Repeat("t", 256), wantLength: 4096},
		{name: "UTF8-boundary", message: strings.Repeat("x", 4094) + "额度quota", code: strings.Repeat("c", 257), kind: strings.Repeat("t", 257), wantLength: 4094, truncated: true, discardID: true},
		{name: "keyword-outside", message: strings.Repeat("x", 4096) + "quota", wantLength: 4096, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"error": map[string]string{"message": test.message, "code": test.code, "type": test.kind}})
			failure, ok := extractUpstreamFailure(UpstreamFailure{HTTPStatus: 429, Protocol: model.ProtocolOpenAIChatCompletion}, body, "", false, "application/json")
			if !ok || len(failure.Message) != test.wantLength || !utf8.ValidString(failure.Message) || failure.MessageTruncated != test.truncated {
				t.Fatalf("message boundary: length=%d valid=%t truncated=%t", len(failure.Message), utf8.ValidString(failure.Message), failure.MessageTruncated)
			}
			if test.discardID {
				if failure.Code != "" || failure.Type != "" {
					t.Fatal("oversized identifier was truncated into a matchable value")
				}
			} else if failure.Code != test.code || failure.Type != test.kind {
				t.Fatal("in-bounds identifier changed")
			}
			if matchesCooldownRule(model.CooldownRule{Keyword: "quota"}, failure) {
				t.Fatal("keyword outside the bounded message matched")
			}
		})
	}
	for _, body := range []string{
		strings.Repeat(" ", maxUpstreamFailureBody) + upstreamFailureTestBody,
		`{"error":{"message":"` + strings.Repeat("x", maxUpstreamFailureBody) + `quota"}}`,
	} {
		failure, ok := extractUpstreamFailure(UpstreamFailure{HTTPStatus: 429, Protocol: model.ProtocolOpenAIChatCompletion}, []byte(body), "", false, "application/json")
		if !ok || failure.HTTPStatus != 429 || failure.Code != "" || failure.Type != "" || failure.Message != "" {
			t.Fatal("invalid bounded JSON was scanned past 64 KiB or used as text")
		}
	}
}

func TestUpstreamFailureErrorCompatibilityAndOwnership(t *testing.T) {
	sender := newUpstreamFailureSender(t, upstreamFailureDirections()[0], "http://example.invalid")
	ctx := withCooldownAttempt(t.Context(), sender.token)
	capture := newUpstreamFailureCapture(ctx, 301, llm.APIFormatOpenAIChatCompletion, "fixture-model-a")
	capture.responseStatus(429)
	body := []byte(upstreamFailureTestBody)
	capture.response(body, "application/json")
	clear(body)
	typed := &httpclient.Error{Method: "POST", URL: "http://example.invalid", StatusCode: 429, Status: "original diagnostic"}
	original := fmt.Errorf("existing wrapper: %w", typed)
	wrapped := capture.wrap(original)
	var gotTyped *httpclient.Error
	if wrapped.Error() != original.Error() || !errors.Is(wrapped, typed) || !errors.As(wrapped, &gotTyped) || gotTyped != typed {
		t.Fatal("snapshot wrapping changed error identity or message")
	}
	failure, ok := UpstreamFailureFromError(wrapped)
	if !ok || failure.Code != "fixture_code" {
		t.Fatal("snapshot aliased original response storage")
	}
	failure.Code = "mutated"
	if again, _ := UpstreamFailureFromError(wrapped); again.Code != "fixture_code" {
		t.Fatal("returned snapshot was not a value copy")
	}
	if capture.wrap(wrapped) != wrapped {
		t.Fatal("same error was wrapped twice")
	}
	for _, excluded := range []error{context.Canceled, context.DeadlineExceeded} {
		if _, ok := UpstreamFailureFromError(capture.wrap(excluded)); ok {
			t.Fatal("cancellation/local timeout received a matchable snapshot")
		}
	}
	wrongModel := newUpstreamFailureCapture(ctx, 301, llm.APIFormatOpenAIChatCompletion, "public-group-name")
	wrongModel.responseStatus(429)
	wrongModel.response([]byte(upstreamFailureTestBody), "application/json")
	wrong, _ := wrongModel.snapshot()
	if wrong.Generation != 0 || sender.engine.Record(sender.token, wrong) {
		t.Fatal("attempt generation was attached to a different actual model")
	}
}

func TestUpstreamFailureStreamOwnershipAndCapabilities(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			capture := newUpstreamFailureCapture(t.Context(), 301, protocol.format, "fixture-model-a")
			capture.responseStatus(200)
			events := compatEvents(t, compatSSE(upstreamFailureEvent(protocol.protocol), protocol.content))
			source := &startupReusingStream{events: events}
			probe := &deliveryProbe{Stream: source, format: protocol.format}
			observed := observeUpstreamFailureStream(probe, capture)
			if !observed.Next() {
				t.Fatal("observer consumed the first event")
			}
			first, ok := capture.snapshot()
			if !ok || first.Message != "Fixture QUOTA reached" || !observed.Next() {
				t.Fatal("observer changed event iteration or missed the error")
			}
			if after, _ := capture.snapshot(); after != first {
				t.Fatal("later source buffer reuse changed the captured failure")
			}
			if _, ok := observed.(streams.DeliveryBoundary); !ok {
				t.Fatal("delivery boundary was hidden")
			}
			ack, ok := observed.(streams.DeliveryAcknowledger)
			if !ok {
				t.Fatal("delivery acknowledgement was hidden")
			}
			ack.AcknowledgeDelivery()
			observed.Close()
			waitForStreamFinalization(observed)
			if probe.acks != 1 || probe.closes != 1 || probe.waits != 1 || source.closed != 1 {
				t.Fatal("observer changed ownership/finalization/acknowledgement")
			}
		})
	}
}
