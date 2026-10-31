package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/Ink-lotus/octopus-roam/internal/rhttp"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/gjson"
)

// Direction names always mean client protocol -> actual upstream protocol.
type conversionDirection struct {
	client, upstream startupProtocol
}

func TestStreamConversionLimits(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, boundary := range []string{"events-exact", "events-over", "bytes-exact", "bytes-over", "large-output"} {
			t.Run(direction.name()+"/"+boundary, func(t *testing.T) {
				prelude, content, count := direction.upstream.prelude[0], direction.upstream.content, 1
				if strings.HasPrefix(boundary, "events-") {
					count = maxStreamStartupEvents
					if boundary == "events-over" {
						count++
					}
				}
				if strings.HasPrefix(boundary, "bytes-") {
					// Size the actual outbound LLM representation that the fixed
					// pipeline counts, not the raw SSE whitespace it discards.
					_, outbound := conversionOutbound(t, direction, "http://example.invalid")
					sample, err := outbound.TransformStream(t.Context(), &httpclient.Request{}, streams.SliceStream(compatEvents(t, compatSSE(prelude))))
					if err != nil || !sample.Next() {
						t.Fatalf("could not measure real transformer metadata: %v", err)
					}
					encoded, err := json.Marshal(sample.Current())
					sample.Close()
					if err != nil {
						t.Fatal(err)
					}
					size := maxStreamStartupBytes - len(encoded) + len("upstream-model")
					if boundary == "bytes-over" {
						size++
					}
					prelude = strings.Replace(prelude, "upstream-model", strings.Repeat("m", size), 1)
				}
				if boundary == "large-output" {
					content = strings.Replace(content, "pong", strings.Repeat("x", maxStreamStartupBytes+1), 1)
				}
				var wire strings.Builder
				for range count {
					wire.WriteString(compatSSE(prelude))
				}
				wire.WriteString(compatSSE(append([]string{content}, direction.upstream.ending...)...))
				trips := countConversionRoundTrips(t)
				engine, group := compatRelay(t, direction.upstream.protocol, direction.client.format, func(w http.ResponseWriter, _ *http.Request) {
					// Omit Date so source metadata sizes are reproducible.
					w.Header()["Date"] = nil
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire.String())
				})
				setConcurrency(t, group.Items[0].ChannelID, 1)
				writer := httptest.NewRecorder()
				serveWithTimeout(t, engine, writer, compatRequest(t, group, direction.client.format, true, nil))
				state := compatState(t, group)
				if trips.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 {
					t.Fatalf("buffer boundary changed attempts: trips=%d attempt=%d retries=%d", trips.Load(), state.Attempt, state.RetryCount)
				}
				if strings.HasSuffix(boundary, "-over") {
					if state.Status != StatusFailed || writer.Code != http.StatusServiceUnavailable || writer.Flushed || state.OutputChars != 0 || !strings.Contains(state.LastError, "buffer limit exceeded") {
						t.Fatalf("overflow exposed startup: status=%s error=%s flushed=%t", state.Status, state.LastError, writer.Flushed)
					}
					assertStartupStats(t, group.Items[0], 0, 1)
				} else {
					if state.Status != StatusSuccess || writer.Code != http.StatusOK {
						t.Fatalf("valid boundary rejected: status=%s error=%s", state.Status, state.LastError)
					}
					if boundary == "large-output" {
						if !strings.Contains(writer.Body.String(), strings.Repeat("x", maxStreamStartupBytes+1)) {
							t.Fatal("valid large output was truncated")
						}
					} else {
						assertConvertedContent(t, direction, writer.Body.String(), false)
					}
					assertStartupStats(t, group.Items[0], 1, 0)
				}
			})
		}
	}
}

func TestStreamConversionEarlyTerminal(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, output := range []bool{false, true} {
			for _, ending := range []string{"success", "error", "eof"} {
				t.Run(fmt.Sprintf("%s/output-%t/%s", direction.name(), output, ending), func(t *testing.T) {
					trips := countConversionRoundTrips(t)
					earlyTerminal := output && direction.upstream.name == "chat"
					writer := &conversionTerminalWriter{
						startupDeliveryWriter: &startupDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), terminal: conversionTerminal(direction.client.format)},
						delivered:             make(chan struct{}),
					}
					var firstCalls, spareCalls atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						firstCalls.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						events := slices.Clone(direction.upstream.prelude)
						if output {
							events = append(events, direction.upstream.content)
						}
						if direction.upstream.name == "responses" {
							// Responses has no independent finish marker: its output
							// item completion may precede the real response outcome.
							events = append(events, `{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg-start","status":"completed","role":"assistant","content":[]}}`)
						} else {
							events = append(events, direction.upstream.ending[:len(direction.upstream.ending)-1]...)
						}
						if direction.upstream.name == "chat" {
							// A finish_reason alone does not complete Responses or
							// Anthropic. Their converters finish on this usage-only
							// chunk, before Chat's real [DONE] marker.
							events = append(events, `{"id":"chat-start","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`)
						}
						if earlyTerminal {
							_, _ = io.WriteString(w, compatSSE(events...))
							w.(http.Flusher).Flush()
							events = nil
							// Do not reveal the source outcome until the converted
							// success terminal has actually reached the client.
							select {
							case <-writer.delivered:
							case <-r.Context().Done():
								return
							}
						}
						if ending == "success" {
							events = append(events, direction.upstream.ending[len(direction.upstream.ending)-1])
						} else if ending == "error" {
							events = append(events, direction.upstream.failure)
						}
						_, _ = io.WriteString(w, compatSSE(events...))
					}))
					defer server.Close()
					spare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						spareCalls.Add(1)
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, strings.ReplaceAll(direction.success(false), "pong", "recovered"))
					}))
					defer spare.Close()
					group := startupGroup(t, direction.upstream, model.GroupModeFailover, testRelayConfig(1), server.URL, spare.URL)
					ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
					defer cancel()
					startupEngine(direction.client).ServeHTTP(writer, compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx))
					state := compatState(t, group)
					wantCalls, wantStatus := int32(1), StatusSuccess
					if ending != "success" {
						if output {
							wantStatus = StatusFailed
						} else {
							wantCalls = 2
						}
					}
					if firstCalls.Load() != 1 || spareCalls.Load() != wantCalls-1 || trips.Load() != wantCalls || state.Status != wantStatus {
						t.Fatalf("source terminal replaced by converter finish: first=%d spare=%d trips=%d state=%s error=%s", firstCalls.Load(), spareCalls.Load(), trips.Load(), state.Status, state.LastError)
					}
					if wantCalls == 2 && (!strings.Contains(writer.Body.String(), "recovered") || strings.Contains(writer.Body.String(), "pong")) {
						t.Fatal("empty failed attempt contaminated the recovered response")
					}
					clientTerminals := 0
					for _, event := range compatEvents(t, writer.Body.String()) {
						if last, err := inspectStreamEvent(direction.client.format, event); last && err == nil {
							clientTerminals++
						}
					}
					if wantStatus == StatusSuccess || earlyTerminal {
						assertConvertedContent(t, direction, strings.ReplaceAll(writer.Body.String(), "recovered", "pong"), !output && wantCalls == 1)
						if !writer.terminalFlushed {
							t.Fatal("converted success terminal was not fully written and flushed")
						}
					} else if clientTerminals != 0 || !strings.Contains(writer.Body.String(), "pong") {
						t.Fatalf("provider failure lost output or invented a success terminal: terminals=%d body=%.1500s", clientTerminals, writer.Body.String())
					}
					wantFailure := 0
					if ending != "success" {
						wantFailure = 1
					}
					// Failover starts the spare member's first attempt; it does
					// not increment that member's same-target RetryCount.
					if state.Round != int(wantCalls) || state.Attempt != 1 || state.RetryCount != 0 || state.ErrorCount != wantFailure {
						t.Fatalf("source outcome changed Forward accounting: round=%d attempt=%d retries=%d errors=%d", state.Round, state.Attempt, state.RetryCount, state.ErrorCount)
					}
					if earlyTerminal && (state.Usage.PromptTokens != 3 || state.Usage.CompletionTokens != 5) {
						t.Fatalf("early client terminal lost observed source usage: %+v", state.Usage)
					}
					if healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != wantFailure {
						t.Fatal("converter completion hid a provider failure")
					}
					assertStartupStats(t, group.Items[0], int64(1-wantFailure), int64(wantFailure))
					assertStartupStats(t, group.Items[1], int64(wantCalls-1), 0)
					t.Logf("earlyClientTerminal=%t clientSuccessTerminals=%d sourceEnding=%s RoundTrip=%d Forward.Attempt=%d RetryCount=%d", earlyTerminal, clientTerminals, ending, trips.Load(), state.Attempt, state.RetryCount)
				})
			}
		}
	}
}

type conversionTerminalWriter struct {
	*startupDeliveryWriter
	delivered chan struct{}
	once      sync.Once
}

func (w *conversionTerminalWriter) Flush() {
	w.startupDeliveryWriter.Flush()
	if w.terminalFlushed {
		w.once.Do(func() { close(w.delivered) })
	}
}

func conversionDirections() []conversionDirection {
	var directions []conversionDirection
	for _, client := range startupProtocols() {
		for _, upstream := range startupProtocols() {
			if client.protocol != upstream.protocol {
				directions = append(directions, conversionDirection{client, upstream})
			}
		}
	}
	return directions
}

func (d conversionDirection) name() string { return d.client.name + "-to-" + d.upstream.name }

func (d conversionDirection) success(empty bool) string {
	events := slices.Clone(d.upstream.prelude)
	if !empty {
		events = append(events, d.upstream.content)
	}
	return compatSSE(append(events, d.upstream.ending...)...)
}

// Count at the real transport boundary, including attempts that never reach a
// server. The production sent bool is deliberately not used as this evidence.
func countConversionRoundTrips(t *testing.T) *atomic.Int32 {
	t.Helper()
	client, err := rhttp.Direct()
	if err != nil {
		t.Fatal(err)
	}
	original := client.Transport
	count := &atomic.Int32{}
	client.Transport = compatRoundTripper(func(r *http.Request) (*http.Response, error) {
		count.Add(1)
		return original.RoundTrip(r)
	})
	t.Cleanup(func() { client.Transport = original })
	return count
}

func assertConvertedContent(t *testing.T, direction conversionDirection, body string, empty bool) int {
	t.Helper()
	events := compatEvents(t, body)
	contents, terminals := 0, 0
	for _, event := range events {
		last, err := inspectStreamEvent(direction.client.format, event)
		if err != nil {
			t.Fatalf("successful client response contains an error: %v", err)
		}
		if last {
			terminals++
		}
		parsed := gjson.ParseBytes(event.Data)
		switch direction.client.name {
		case "chat":
			if parsed.Get("choices.0.delta.content").String() == "pong" {
				contents++
			}
		case "responses":
			if parsed.Get("type").String() == "response.output_text.delta" && parsed.Get("delta").String() == "pong" {
				contents++
			}
		case "anthropic":
			if parsed.Get("type").String() == "content_block_delta" && parsed.Get("delta.text").String() == "pong" {
				contents++
			}
		}
	}
	wantContent := 1
	if empty {
		wantContent = 0
	}
	if contents != wantContent || terminals != 1 {
		t.Fatalf("client content/terminal counts = %d/%d, want %d/1; body=%.1500s", contents, terminals, wantContent, body)
	}
	return len(events)
}

func TestStreamConversionStartup(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, mode := range []string{"content", "retry-error", "retry-eof", "retry-http", "startup-eof", "startup-read-error", "malformed", "empty", "empty-success"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				trips := countConversionRoundTrips(t)
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					n := calls.Add(1)
					body := direction.success(mode == "empty-success")
					prelude := strings.NewReplacer("chat-start", "failed-attempt", "resp-compat", "failed-attempt", "msg-start", "failed-attempt").Replace(compatSSE(direction.upstream.prelude...))
					switch mode {
					case "retry-error":
						if n == 1 {
							body = prelude + compatSSE(direction.upstream.failure)
						}
					case "retry-eof":
						if n == 1 {
							body = prelude
						}
					case "retry-http":
						if n == 1 {
							w.WriteHeader(http.StatusTooManyRequests)
							_, _ = io.WriteString(w, `{"error":{"message":"synthetic rate limit","type":"rate_limit_error"}}`)
							return
						}
					case "startup-eof", "startup-read-error":
						body = prelude
					case "malformed":
						body = prelude + "data: {\"broken\":\n\n"
					case "empty":
						body = ""
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if mode == "startup-read-error" {
						w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
					}
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				config := testRelayConfig(2)
				config.ManualMaxAttempts = 1
				wantCalls := 1
				if strings.HasPrefix(mode, "retry-") {
					config.ManualMaxAttempts, wantCalls = 2, 2
				}
				group := startupGroup(t, direction.upstream, model.GroupModeManual, config, server.URL)
				setConcurrency(t, group.Items[0].ChannelID, 1)
				writer := httptest.NewRecorder()
				serveWithTimeout(t, startupEngine(direction.client), writer, compatRequest(t, group, direction.client.format, true, nil))
				state := compatState(t, group)
				if int(trips.Load()) != wantCalls || int(calls.Load()) != wantCalls || state.Round != wantCalls || state.Attempt != wantCalls || state.RetryCount != wantCalls-1 {
					t.Fatalf("HTTP sends differ from Forward attempts: roundTrips=%d server=%d state=%+v", trips.Load(), calls.Load(), state)
				}
				success := mode == "content" || mode == "empty-success" || strings.HasPrefix(mode, "retry-")
				wantSuccess, wantErrors := int64(0), 1
				if success {
					wantSuccess, wantErrors = 1, wantCalls-1
					if state.Status != StatusSuccess || writer.Code != http.StatusOK {
						t.Fatalf("valid conversion failed: state=%+v body=%.1500s", state, writer.Body.String())
					}
					events := assertConvertedContent(t, direction, writer.Body.String(), mode == "empty-success")
					if state.OutputChars != events || state.FirstTokenDuration < 0 || state.FirstTokenDuration > state.Duration {
						t.Fatalf("output/timing contains failed startup: %+v", state)
					}
				} else if state.Status != StatusFailed || writer.Code != http.StatusServiceUnavailable || writer.Flushed || state.OutputChars != 0 || state.FirstTokenDuration != 0 {
					t.Fatalf("failed startup committed: status=%d flushed=%t state=%+v body=%.1000s", writer.Code, writer.Flushed, state, writer.Body.String())
				}
				if strings.Contains(writer.Body.String(), "failed-attempt") || state.ErrorCount != wantErrors {
					t.Fatalf("failed round leaked or error counted twice: %+v", state)
				}
				health := healthOf(t, group.ID).Members[group.Items[0].ID]
				if health.FailureStreak != 1-int(wantSuccess) || (wantCalls == 2 && health.Epoch != 1) {
					t.Fatalf("startup changed health early: %+v", health)
				}
				assertStartupStats(t, group.Items[0], wantSuccess, int64(wantCalls)-wantSuccess)
				t.Logf("RoundTrip=%d Forward.Attempt=%d RetryCount=%d clientEvents=%d", trips.Load(), state.Attempt, state.RetryCount, state.OutputChars)
			})
		}
	}
}

func conversionOutbound(t *testing.T, direction conversionDirection, url string) (model.Channel, transformer.Outbound) {
	t.Helper()
	channel := model.Channel{BaseURL: url, AllowedProtocols: direction.upstream.protocol}
	outbound, selected, passthrough, err := buildOutbound(channel, model.ChannelGrant{Protocols: direction.upstream.protocol}, model.ChannelKey{Key: "synthetic-credential"}, direction.client.protocol)
	if err != nil || selected != direction.upstream.protocol || passthrough {
		t.Fatalf("actual buildOutbound selection: protocol=%v passthrough=%t err=%v", selected, passthrough, err)
	}
	return channel, outbound
}

func conversionRawRequest(t *testing.T, direction conversionDirection) *httpclient.Request {
	t.Helper()
	req := compatRequest(t, model.Group{Name: "upstream-model"}, direction.client.format, true, nil)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return &httpclient.Request{Method: http.MethodPost, URL: req.URL.String(), Headers: req.Header.Clone(), Body: body}
}

func TestStreamConversionOneSendAndErrorIdentity(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, mode := range []string{"stream-error", "eof", "http-error", "transport-error", "redirect"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				trips := countConversionRoundTrips(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "redirect" && r.URL.Path != "/redirected" {
						w.Header().Set("Location", "/redirected")
						w.WriteHeader(http.StatusTemporaryRedirect)
						return
					}
					if mode == "http-error" {
						w.WriteHeader(http.StatusTooManyRequests)
						_, _ = io.WriteString(w, `{"error":{"message":"synthetic limit","type":"rate_limit_error","code":"synthetic_code"}}`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, compatSSE(direction.upstream.prelude...))
					if mode == "stream-error" {
						_, _ = io.WriteString(w, compatSSE(direction.upstream.failure))
					}
				}))
				defer server.Close()
				url := server.URL
				if mode == "transport-error" {
					server.Close()
				}
				channel, outbound := conversionOutbound(t, direction, url)
				_, retryable := outbound.(pipeline.Retryable)
				_, channelRetryable := outbound.(pipeline.ChannelRetryable)
				if retryable || channelRetryable {
					t.Fatalf("%T unexpectedly owns retries: cross=%t same=%t", outbound, retryable, channelRetryable)
				}
				var executor pipeline.Executor = httpclient.NewHttpClientWithClient(server.Client())
				if custom, ok := outbound.(pipeline.ChannelCustomizedExecutor); ok {
					executor = custom.CustomizeExecutor(executor)
				}
				result, sent, err := sendConverted(context.Background(), direction.client.format, conversionRawRequest(t, direction), channel, outbound, true)
				if result != nil || !sent || err == nil || trips.Load() != 1 {
					t.Fatalf("one sendConverted attempt: result=%v sent=%t error=%v RoundTrip=%d", result, sent, err, trips.Load())
				}
				if mode == "stream-error" || mode == "http-error" {
					var failure *llm.ResponseError
					if !errors.As(err, &failure) || failure.Detail.Message == "" {
						t.Fatalf("typed upstream error was lost: %T %v", err, err)
					}
					if mode == "stream-error" && direction.upstream.name != "anthropic" && failure.Detail.Code != "synthetic_code" {
						t.Fatalf("upstream code changed: %+v", failure.Detail)
					}
				}
				t.Logf("outbound=%T Retryable=%t ChannelRetryable=%t executor=%T RoundTrip=%d", outbound, retryable, channelRetryable, executor, trips.Load())
			})
		}
	}
}

func TestStreamConversionOutputStopsRetry(t *testing.T) {
	for _, direction := range conversionDirections() {
		if direction.upstream.name == "responses" {
			direction.upstream.thinking = `{"type":"response.reasoning_text.delta","item_id":"reasoning-start","output_index":0,"content_index":0,"delta":"thinking"}`
		}
		if direction.upstream.name == "anthropic" {
			// Anthropic represents refusal text in ordinary text blocks; its
			// refusal stop_reason alone is a finish marker with no text.
			direction.upstream.refusal = `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"refused"}}`
		}
		extended := strings.TrimSuffix(direction.upstream.content, "}") + `,"vendor_metadata":{"value":"opaque"}}`
		for _, kind := range []startupKind{{"content", direction.upstream.content}, {"tool", direction.upstream.tool}, {"thinking", direction.upstream.thinking}, {"refusal", direction.upstream.refusal}, {"extension", extended}} {
			t.Run(direction.name()+"/"+kind.name, func(t *testing.T) {
				trips := countConversionRoundTrips(t)
				var firstCalls, spareCalls atomic.Int32
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					firstCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					events := append(slices.Clone(direction.upstream.prelude), kind.event)
					if direction.upstream.name == "responses" && kind.name == "thinking" {
						// Anthropic must group an item's thinking with its final
						// signature before it can emit that thinking block.
						events = append(events, `{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"reasoning-start","encrypted_content":"synthetic-signature","summary":[{"type":"summary_text","text":"thinking"}]}}`)
					}
					_, _ = io.WriteString(w, compatSSE(append(events, direction.upstream.failure)...))
				}))
				defer first.Close()
				spare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					spareCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, direction.success(false))
				}))
				defer spare.Close()
				group := startupGroup(t, direction.upstream, model.GroupModeFailover, testRelayConfig(1), first.URL, spare.URL)
				engine := startupEngine(direction.client)
				writer := httptest.NewRecorder()
				serveWithTimeout(t, engine, writer, compatRequest(t, group, direction.client.format, true, nil))
				state := compatState(t, group)
				if firstCalls.Load() != 1 || spareCalls.Load() != 0 || trips.Load() != 1 || writer.Code != http.StatusOK || !writer.Flushed || state.Status != StatusFailed || state.Attempt != 1 || state.RetryCount != 0 || state.ErrorCount != 1 {
					t.Fatalf("output was buffered/replayed: first=%d spare=%d trips=%d state=%+v body=%.1200s", firstCalls.Load(), spareCalls.Load(), trips.Load(), state, writer.Body.String())
				}
				want := map[string]string{"content": "pong", "tool": "lookup", "thinking": "thinking", "refusal": "refus", "extension": "pong"}[kind.name]
				if !strings.Contains(writer.Body.String(), want) {
					t.Fatalf("converted %s output missing: %.1800s", kind.name, writer.Body.String())
				}
				if healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 1 {
					t.Fatal("late source failure did not enter shared health")
				}
				assertStartupStats(t, group.Items[0], 0, 1)
				next := httptest.NewRecorder()
				serveWithTimeout(t, engine, next, compatRequest(t, group, direction.client.format, true, nil))
				if firstCalls.Load() != 1 || spareCalls.Load() != 1 || trips.Load() != 2 || compatState(t, group).Status != StatusSuccess {
					t.Fatal("next request ignored shared cooldown")
				}
				assertConvertedContent(t, direction, next.Body.String(), false)
				assertStartupStats(t, group.Items[1], 1, 0)
			})
		}
	}
}

func TestStreamConversionWaitsForOutput(t *testing.T) {
	for _, direction := range conversionDirections() {
		t.Run(direction.name(), func(t *testing.T) {
			arrived, release := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatSSE(direction.upstream.prelude...))
				w.(http.Flusher).Flush()
				close(arrived)
				select {
				case <-release:
					_, _ = io.WriteString(w, compatSSE(append([]string{direction.upstream.content}, direction.upstream.ending...)...))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			trips := countConversionRoundTrips(t)
			group := startupGroup(t, direction.upstream, model.GroupModeManual, testRelayConfig(1), server.URL)
			setConcurrency(t, group.Items[0].ChannelID, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer := &startupWriter{ResponseRecorder: httptest.NewRecorder()}
			done := make(chan struct{})
			request := compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx)
			go func() { defer close(done); startupEngine(direction.client).ServeHTTP(writer, request) }()
			awaitStartupSignal(t, arrived)
			// Hold the provider independently of the Relay clock. Even after a
			// scheduling opportunity no startup frame may commit the response.
			select {
			case <-done:
				t.Fatal("startup ended the request before output")
			case <-time.After(40 * time.Millisecond):
			}
			state := compatState(t, group)
			if writer.writes.Load() != 0 || writer.flushes.Load() != 0 || state.OutputChars != 0 || state.FirstTokenDuration != 0 || ChannelConcurrencyStats(group.Items[0].ChannelID).Active != 1 {
				t.Fatalf("startup committed or released active capacity: %+v", state)
			}
			waited := time.Since(state.RoundStartedAt)
			close(release)
			awaitStartupSignal(t, done)
			state = compatState(t, group)
			if state.Status != StatusSuccess || state.FirstTokenDuration < waited || trips.Load() != 1 {
				t.Fatalf("first-token duration lost startup wait: waited=%s state=%+v trips=%d", waited, state, trips.Load())
			}
			assertConvertedContent(t, direction, writer.Body.String(), false)
			assertStartupStats(t, group.Items[0], 1, 0)
		})
	}
}

func TestStreamConversionTimeout(t *testing.T) {
	for _, direction := range conversionDirections() {
		t.Run(direction.name(), func(t *testing.T) {
			closed := make(chan struct{})
			var frames atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "text/event-stream")
				ticker := time.NewTicker(20 * time.Millisecond)
				defer ticker.Stop()
				for {
					_, _ = io.WriteString(w, compatSSE(direction.upstream.prelude[0]))
					w.(http.Flusher).Flush()
					frames.Add(1)
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
					}
				}
			}))
			defer server.Close()
			trips := countConversionRoundTrips(t)
			config := testRelayConfig(1)
			config.ManualMaxAttempts, config.MemberStreamFirstEventTimeoutSeconds = 1, 1
			group := startupGroup(t, direction.upstream, model.GroupModeManual, config, server.URL)
			writer := httptest.NewRecorder()
			began := time.Now()
			serveWithTimeout(t, startupEngine(direction.client), writer, compatRequest(t, group, direction.client.format, true, nil))
			elapsed := time.Since(began)
			awaitStartupSignal(t, closed)
			state := compatState(t, group)
			if elapsed < 800*time.Millisecond || elapsed > 2500*time.Millisecond || trips.Load() != 1 || frames.Load() < 3 {
				t.Fatalf("startup events changed timeout: duration=%s trips=%d frames=%d", elapsed, trips.Load(), frames.Load())
			}
			if writer.Code != http.StatusServiceUnavailable || writer.Flushed || state.Status != StatusFailed || state.OutputChars != 0 || state.FirstTokenDuration != 0 || state.ErrorCount != 1 || !strings.Contains(state.LastError, "first event timeout") {
				t.Fatalf("timeout leaked or changed accounting: %+v", state)
			}
			assertStartupStats(t, group.Items[0], 0, 1)
		})
	}
}

func conversionTerminal(format llm.APIFormat) string {
	switch format {
	case llm.APIFormatOpenAIResponse:
		return `"type":"response.completed"`
	case llm.APIFormatAnthropicMessage:
		return `"type":"message_stop"`
	default:
		return "[DONE]"
	}
}

func TestStreamConversionCancellationAndQueue(t *testing.T) {
	for _, direction := range conversionDirections() {
		protocol := direction.upstream
		t.Run(direction.name(), func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			trips := countConversionRoundTrips(t)
			t.Cleanup(func() {
				if trips.Load() != calls.Load() {
					t.Errorf("transport/server sends differ: %d/%d", trips.Load(), calls.Load())
				}
			})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, startupWire("canceled", protocol.prelude...))
				w.(http.Flusher).Flush()
				close(arrived)
				<-r.Context().Done()
				close(closed)
			}))
			defer upstream.Close()
			group := startupGroup(t, protocol, model.GroupModeManual, testRelayConfig(1), upstream.URL)
			setConcurrency(t, group.Items[0].ChannelID, 1)
			engine := startupEngine(direction.client)
			start := func(ctx context.Context) (*startupWriter, <-chan struct{}) {
				writer := &startupWriter{ResponseRecorder: httptest.NewRecorder()}
				done := make(chan struct{})
				request := compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx)
				go func() { defer close(done); engine.ServeHTTP(writer, request) }()
				return writer, done
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			first, firstDone := start(ctx)
			awaitStartupSignal(t, arrived)
			firstID := compatState(t, group).ID
			queuedCtx, cancelQueued := context.WithCancel(t.Context())
			defer cancelQueued()
			queued, queuedDone := start(queuedCtx)
			awaitCapacity(t, group.Items[0].ChannelID, 1, 1)
			queuedID := compatState(t, group).ID
			cancelQueued()
			awaitStartupSignal(t, queuedDone)
			awaitCapacity(t, group.Items[0].ChannelID, 1, 0)
			if state := startupState(t, queuedID); state.Status != StatusCanceled || state.Attempt != 0 || state.ErrorCount != 0 || queued.writes.Load() != 0 {
				t.Fatalf("queued cancellation sent a request: %+v", state)
			}
			cancel()
			awaitStartupSignal(t, firstDone)
			awaitStartupSignal(t, closed)
			if state := startupState(t, firstID); state.Status != StatusCanceled || state.Attempt != 1 || state.ErrorCount != 0 || state.FirstTokenDuration != 0 || state.OutputChars != 0 || first.writes.Load() != 0 || first.flushes.Load() != 0 {
				t.Fatalf("pre-read cancellation leaked startup data or counted failure: %+v", state)
			}
			if calls.Load() != 1 || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 0 {
				t.Fatal("cancellation duplicated sends or changed shared health")
			}
			assertStartupStats(t, group.Items[0], 0, 0)
		})
	}
}

func TestStreamConversionExclusiveRecovery(t *testing.T) {
	for _, direction := range conversionDirections() {
		protocol := direction.upstream
		t.Run(direction.name(), func(t *testing.T) {
			arrived := make(chan int32, 16)
			var calls atomic.Int32
			trips := countConversionRoundTrips(t)
			t.Cleanup(func() {
				if trips.Load() != calls.Load() {
					t.Errorf("transport/server sends differ: %d/%d", trips.Load(), calls.Load())
				}
			})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, startupWire("probe", protocol.prelude...))
				w.(http.Flusher).Flush()
				arrived <- n
				switch n {
				case 1:
					_, _ = io.WriteString(w, compatSSE(protocol.failure))
				case 2:
					<-r.Context().Done()
				default:
					_, _ = io.WriteString(w, compatSSE(append([]string{protocol.content}, protocol.ending...)...))
				}
			}))
			defer upstream.Close()
			config := testRelayConfig(1)
			config.ManualMaxAttempts = 1
			group := startupGroup(t, protocol, model.GroupModeManual, config, upstream.URL)
			setConcurrency(t, group.Items[0].ChannelID, 1)
			engine := startupEngine(direction.client)
			engine.ServeHTTP(httptest.NewRecorder(), compatRequest(t, group, direction.client.format, true, nil))
			<-arrived
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			writers := make([]*startupWriter, 5)
			for i := range writers {
				writers[i] = &startupWriter{ResponseRecorder: httptest.NewRecorder()}
				request := compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx)
				wg.Go(func() { engine.ServeHTTP(writers[i], request) })
			}
			select {
			case n := <-arrived:
				if n != 2 {
					t.Fatalf("wrong recovery send: %d", n)
				}
			case <-ctx.Done():
				t.Fatal("recovery probe did not send")
			}
			select {
			case n := <-arrived:
				t.Fatalf("startup frame released exclusive recovery: send=%d", n)
			case <-time.After(60 * time.Millisecond):
			}
			for _, writer := range writers {
				if writer.writes.Load() != 0 || writer.flushes.Load() != 0 {
					t.Fatal("recovery startup reached a client")
				}
			}
			if health := healthOf(t, group.ID).Members[group.Items[0].ID]; health.FailureStreak != 1 || health.Epoch != 0 {
				t.Fatalf("startup prematurely recovered member health: %+v", health)
			}
			awaitCapacity(t, group.Items[0].ChannelID, 1, 0)
			cancel()
			wg.Wait()
			awaitCapacity(t, group.Items[0].ChannelID, 0, 0)
			if calls.Load() != 2 || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 1 {
				t.Fatal("canceled competitors changed recovery state or sent duplicates")
			}
			response := httptest.NewRecorder()
			serveWithTimeout(t, engine, response, compatRequest(t, group, direction.client.format, true, nil))
			if calls.Load() != 3 || compatState(t, group).Status != StatusSuccess || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 0 {
				t.Fatal("canceled probe did not release recovery ownership")
			}
			assertStartupStats(t, group.Items[0], 1, 1)
		})
	}
}

func TestStreamConversionBackoffReleasesCapacity(t *testing.T) {
	for _, direction := range conversionDirections() {
		protocol := direction.upstream
		t.Run(direction.name(), func(t *testing.T) {
			var calls atomic.Int32
			trips := countConversionRoundTrips(t)
			t.Cleanup(func() {
				if trips.Load() != calls.Load() {
					t.Errorf("HTTP/server sends differ: %d/%d", trips.Load(), calls.Load())
				}
			})
			wire := startupWire("success", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if calls.Add(1) == 1 {
					_, _ = io.WriteString(w, startupWire("failed-round", append(slices.Clone(protocol.prelude), protocol.failure)...))
				} else {
					_, _ = io.WriteString(w, wire)
				}
			}))
			defer upstream.Close()
			config := testRelayConfig(2)
			config.ManualMaxAttempts = 2
			group := startupGroup(t, protocol, model.GroupModeManual, config, upstream.URL)
			item := group.Items[0]
			setConcurrency(t, item.ChannelID, 1)
			otherName := newTestGroup(t, model.GroupModeManual, config, item.ChannelGrantID)
			other, err := op.GroupGetByName(otherName)
			if err != nil {
				t.Fatal(err)
			}
			_, updates := OpenRequestStream()
			defer CloseRequestStream(updates)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			engine := startupEngine(direction.client)
			first := httptest.NewRecorder()
			done := make(chan struct{})
			request := compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx)
			go func() { defer close(done); engine.ServeHTTP(first, request) }()
			var waiting RequestState
			for !waiting.Retrying {
				select {
				case state := <-updates:
					if state.GroupID == group.ID {
						waiting = state
					}
				case <-ctx.Done():
					t.Fatal("startup failure never entered backoff")
				}
			}
			if waiting.Attempt != 1 || waiting.ErrorCount != 1 || waiting.OutputChars != 0 {
				t.Fatalf("startup failure state incorrect: %+v", waiting)
			}
			awaitCapacity(t, item.ChannelID, 0, 0)
			second := httptest.NewRecorder()
			engine.ServeHTTP(second, compatRequest(t, other, direction.client.format, true, nil).WithContext(ctx))
			if compatState(t, other).Status != StatusSuccess || calls.Load() != 2 || time.Now().UnixMilli() >= waiting.NextRetryAt {
				t.Fatal("backoff held the channel's capacity or allowed an early retry")
			}
			assertConvertedContent(t, direction, second.Body.String(), false)
			awaitStartupSignal(t, done)
			state := compatState(t, group)
			if calls.Load() != 3 || state.Status != StatusSuccess || state.Attempt != 2 || state.RetryCount != 1 || state.ErrorCount != 1 {
				t.Fatalf("backoff retry was duplicated or miscounted: calls=%d state=%+v", calls.Load(), state)
			}
			assertConvertedContent(t, direction, first.Body.String(), false)
			assertStartupStats(t, item, 2, 1)
		})
	}
}

func TestStreamConversionRouteChanges(t *testing.T) {
	for _, direction := range conversionDirections() {
		protocol := direction.upstream
		for _, change := range []string{"select", "interrupt", "config", "disable", "delete"} {
			t.Run(direction.name()+"/"+change, func(t *testing.T) {
				arrived, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var oldCalls, newCalls atomic.Int32
				trips := countConversionRoundTrips(t)
				t.Cleanup(func() {
					if trips.Load() != oldCalls.Load()+newCalls.Load() {
						t.Errorf("HTTP/server sends differ: %d/%d", trips.Load(), oldCalls.Load()+newCalls.Load())
					}
				})
				oldWire := startupWire("old", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
				oldWire = strings.Replace(oldWire, "pong", "old-output", 1)
				newWire := startupWire("new", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
				newWire = strings.Replace(newWire, "pong", "fresh-output", 1)
				old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					oldCalls.Add(1)
					defer close(closed)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, startupWire("old", protocol.prelude...))
					w.(http.Flusher).Flush()
					close(arrived)
					select {
					case <-release:
						if change == "config" {
							_, _ = io.WriteString(w, compatSSE(protocol.failure))
						} else {
							// Preserve the same event IDs as one uninterrupted response.
							_, _ = io.WriteString(w, strings.TrimPrefix(oldWire, startupWire("old", protocol.prelude...)))
						}
					case <-r.Context().Done():
					}
				}))
				defer old.Close()
				fresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					newCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, newWire)
				}))
				defer fresh.Close()
				group := startupGroup(t, protocol, model.GroupModeFailover, testRelayConfig(1), old.URL, fresh.URL)
				firstItem, spareItem := group.Items[0], group.Items[1]
				setConcurrency(t, firstItem.ChannelID, 1)
				setConcurrency(t, spareItem.ChannelID, 1)
				ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
				defer cancel()
				engine := startupEngine(direction.client)
				start := func() (*startupWriter, <-chan struct{}) {
					writer := &startupWriter{ResponseRecorder: httptest.NewRecorder()}
					done := make(chan struct{})
					request := compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx)
					go func() { defer close(done); engine.ServeHTTP(writer, request) }()
					return writer, done
				}
				firstWriter, firstDone := start()
				awaitStartupSignal(t, arrived)
				firstState := compatState(t, group)
				queuedWriter, queuedDone := start()
				awaitCapacity(t, firstItem.ChannelID, 1, 1)
				queuedID := compatState(t, group).ID
				switch change {
				case "select", "interrupt":
					state := RouteStateOf(group)
					if _, err := SelectRouteItem(group, spareItem.ID, state.SelectionVersion); err != nil {
						t.Fatal(err)
					}
					if change == "interrupt" {
						Interrupt(firstState.ID, firstState.Round)
					}
				case "config":
					detail, err := op.ChannelDetailGet(firstItem.ChannelID)
					if err != nil {
						t.Fatal(err)
					}
					detail.BaseURL, detail.MaxConcurrency = fresh.URL, 2
					if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
						t.Fatal(err)
					}
				case "disable":
					if err := op.ChannelEnabled(firstItem.ChannelID, false, t.Context()); err != nil {
						t.Fatal(err)
					}
				case "delete":
					if err := op.ChannelDel(firstItem.ChannelID, t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				currentGroup, err := op.GroupGetByName(group.Name)
				if err != nil {
					t.Fatal(err)
				}
				if change == "config" || change == "disable" || change == "delete" {
					// The management handler publishes the same notification after op.
					NotifyRouteStateChanged(currentGroup)
				}
				awaitStartupSignal(t, queuedDone)
				if state := startupState(t, queuedID); state.Status != StatusSuccess || state.Attempt != 1 || state.RetryCount != 0 || state.ErrorCount != 0 {
					t.Fatalf("queued request retained stale configuration or counted a wait as send: %+v", state)
				}
				if !strings.Contains(queuedWriter.Body.String(), "fresh-output") || strings.Contains(queuedWriter.Body.String(), "old-output") {
					t.Fatal("queued request did not use fresh target")
				}
				if change != "interrupt" {
					if firstWriter.writes.Load() != 0 || firstWriter.flushes.Load() != 0 {
						t.Fatal("old startup was committed during a route/configuration change")
					}
					select {
					case <-closed:
						t.Fatal("configuration/card change interrupted an already sent request")
					default:
					}
					close(release)
				}
				awaitStartupSignal(t, firstDone)
				awaitStartupSignal(t, closed)
				wantCalls, wantWire, wantRound, wantErrors := int32(1), oldWire, 1, 0
				if change == "interrupt" || change == "config" {
					wantCalls, wantWire, wantRound = 2, newWire, 2
					if change == "config" {
						wantErrors = 1
					}
				}
				firstState = startupState(t, firstState.ID)
				if oldCalls.Load() != 1 || newCalls.Load() != wantCalls || firstState.Status != StatusSuccess || firstState.Round != wantRound || firstState.Attempt != 1 || firstState.RetryCount != 0 || firstState.ErrorCount != wantErrors {
					t.Fatalf("configuration change duplicated/replayed an attempt: old=%d new=%d state=%+v", oldCalls.Load(), newCalls.Load(), firstState)
				}
				wantText := "old-output"
				if strings.Contains(wantWire, "fresh-output") {
					wantText = "fresh-output"
				}
				if !strings.Contains(firstWriter.Body.String(), wantText) {
					t.Fatalf("selected source output missing: %.500s", firstWriter.Body.String())
				}
				if change == "interrupt" {
					Interrupt(firstState.ID, firstState.Round-1)
				}
				expectedItem := spareItem.ID
				if change == "config" {
					expectedItem = firstItem.ID
				}
				if RouteStateOf(currentGroup).CurrentItemID != expectedItem {
					t.Fatal("old result reclaimed the route selection")
				}
				for _, health := range healthOf(t, group.ID).Members {
					if health.FailureStreak != 0 {
						t.Fatalf("old/canceled response contaminated new health: %+v", health)
					}
				}
				awaitCapacity(t, firstItem.ChannelID, 0, 0)
				awaitCapacity(t, spareItem.ChannelID, 0, 0)
			})
		}
	}
}

func conversionInbound(format llm.APIFormat) transformer.Inbound {
	switch format {
	case llm.APIFormatOpenAIResponse:
		return responses.NewInboundTransformer()
	case llm.APIFormatAnthropicMessage:
		return anthropic.NewInboundTransformer()
	default:
		return openai.NewInboundTransformer()
	}
}

func TestStreamConversionCapabilities(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, mode := range []string{"none", "ack-only", "boundary-waiter", "all", "short-terminal", "write-error", "cancel-before-terminal", "cancel-after-terminal", "startup-error", "startup-overflow"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				body := direction.success(false)
				if mode == "startup-error" {
					body = compatSSE(append(slices.Clone(direction.upstream.prelude), direction.upstream.failure)...)
				} else if mode == "startup-overflow" {
					body = strings.Repeat(compatSSE(direction.upstream.prelude[0]), maxStreamStartupEvents+1)
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, body)
				}))
				defer server.Close()
				client := server.Client()
				original := client.Transport
				var trips atomic.Int32
				client.Transport = compatRoundTripper(func(r *http.Request) (*http.Response, error) { trips.Add(1); return original.RoundTrip(r) })
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				writer := &startupDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, terminal: conversionTerminal(direction.client.format), cancel: cancel}
				var probe *deliveryProbe
				executor := &deliveryExecutor{HttpClient: httpclient.NewHttpClientWithClient(client)}
				executor.wrap = func(source streams.Stream[*httpclient.StreamEvent]) streams.Stream[*httpclient.StreamEvent] {
					probe = &deliveryProbe{Stream: source, format: direction.upstream.format}
					probe.ackCheck = func() {
						if !writer.terminalFlushed {
							t.Error("source acknowledged before full client terminal delivery")
						}
					}
					switch mode {
					case "none":
						return &struct {
							streams.Stream[*httpclient.StreamEvent]
						}{probe}
					case "ack-only":
						return &struct {
							streams.Stream[*httpclient.StreamEvent]
							streams.DeliveryAcknowledger
						}{probe, probe}
					case "boundary-waiter":
						return &struct {
							streams.Stream[*httpclient.StreamEvent]
							streams.DeliveryBoundary
							streams.FinalizationWaiter
						}{probe, probe, probe}
					default:
						return probe
					}
				}
				_, outbound := conversionOutbound(t, direction, server.URL)
				middleware := &conversionMiddleware{format: direction.upstream.format, clientFormat: direction.client.format}
				processor := pipeline.NewFactory(executor).Pipeline(conversionInbound(direction.client.format), outbound, pipeline.WithRetry(1, 0, 0), pipeline.WithMiddlewares(middleware))
				result, err := processor.Process(ctx, conversionRawRequest(t, direction))
				var finalizing streams.Stream[*httpclient.StreamEvent]
				if strings.HasPrefix(mode, "startup-") {
					if err == nil || result != nil {
						t.Fatal("pre-read failure returned a client stream")
					}
					failed := middleware.failedResponse()
					if failed == nil || failed.finalize == nil {
						t.Fatal("source-owned finalization disappeared on pre-read failure")
					}
					finalizing = failed.finalize
				} else {
					if err != nil {
						t.Fatal(err)
					}
					stream := result.EventStream
					for stream.Next() {
						event := stream.Current()
						last, inspectErr := inspectStreamEvent(direction.client.format, event)
						if inspectErr != nil {
							t.Fatal(inspectErr)
						}
						data := []byte(compatSSE(string(event.Data)))
						err = deliverStreamEvent(ctx, stream, last, func() error {
							n, writeErr := writer.Write(data)
							if writeErr == nil && n != len(data) {
								writeErr = io.ErrShortWrite
							}
							if writeErr == nil {
								writer.Flush()
							}
							return writeErr
						})
						if err != nil || last {
							break
						}
					}
					if mode == "short-terminal" && !errors.Is(err, io.ErrShortWrite) {
						t.Fatalf("terminal short write was ignored: %v", err)
					}
					stream.Close()
					finalizing = stream
				}
				cancel()
				probe.waitCheck = func() {
					if probe.closes != 1 || ctx.Err() == nil {
						t.Error("finalization waited before close/cancel")
					}
				}
				waitForStreamFinalization(finalizing)
				wantAck, wantWait := 1, 1
				if mode == "none" || mode == "short-terminal" || mode == "write-error" || mode == "cancel-before-terminal" || strings.HasPrefix(mode, "startup-") {
					wantAck = 0
				}
				if mode == "none" || mode == "ack-only" {
					wantWait = 0
				}
				if probe.acks != wantAck || probe.waits != wantWait || probe.closes != 1 || trips.Load() != 1 {
					t.Fatalf("capabilities/ownership: ack=%d/%d wait=%d/%d close=%d sends=%d", probe.acks, wantAck, probe.waits, wantWait, probe.closes, trips.Load())
				}
				if mode == "boundary-waiter" && probe.boundaries == 0 {
					t.Fatal("standalone boundary/waiter disappeared")
				}
			})
		}
	}
}

func TestStreamConversionDeliveryAndConnections(t *testing.T) {
	for _, direction := range conversionDirections() {
		protocol := direction.upstream
		for _, mode := range []string{"success", "cancel-before-terminal", "startup-error", "startup-overflow"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				events := append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)
				if mode == "startup-error" {
					events = append(slices.Clone(protocol.prelude), protocol.failure)
				} else if mode == "startup-overflow" {
					events = make([]string, maxStreamStartupEvents+1)
					for i := range events {
						events[i] = protocol.prelude[0]
					}
				}
				wire := startupWire("delivery", events...)
				closed := make(chan struct{}, 4)
				var calls, connections, closes atomic.Int32
				proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				}))
				proxy.Config.ConnState = func(_ net.Conn, state http.ConnState) {
					if state == http.StateNew {
						connections.Add(1)
					} else if state == http.StateClosed {
						closes.Add(1)
						closed <- struct{}{}
					}
				}
				proxy.Start()
				defer proxy.Close()
				config := testRelayConfig(1)
				config.ManualMaxAttempts = 1
				group := startupGroup(t, protocol, model.GroupModeManual, config, proxy.URL)
				detail, err := op.ChannelDetailGet(group.Items[0].ChannelID)
				if err != nil {
					t.Fatal(err)
				}
				detail.Proxy, detail.ChannelProxy, detail.MaxConcurrency = true, proxy.URL, 1
				if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				writer := &startupDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, terminal: conversionTerminal(direction.client.format), cancel: cancel}
				startupEngine(direction.client).ServeHTTP(writer, compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx))
				awaitStartupSignal(t, closed)
				if connections.Load() != 1 || closes.Load() != 1 || calls.Load() != 1 {
					t.Fatalf("connection/send ownership: opened=%d closed=%d sent=%d", connections.Load(), closes.Load(), calls.Load())
				}
				state := compatState(t, group)
				wantStatus, wantSuccess, wantFailures := StatusFailed, int64(0), 0
				switch mode {
				case "success":
					wantStatus, wantSuccess = StatusSuccess, 1
					if !writer.terminalFlushed {
						t.Fatal("successful terminal was not flushed")
					}
					assertConvertedContent(t, direction, writer.Body.String(), false)
				case "cancel-before-terminal":
					wantStatus = StatusCanceled
				case "startup-error", "startup-overflow":
					wantFailures = 1
					if writer.Code != http.StatusServiceUnavailable || writer.Flushed || state.OutputChars != 0 {
						t.Fatal("failed startup committed a response")
					}
				}
				if state.Status != wantStatus || state.Attempt != 1 || state.RetryCount != 0 || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != wantFailures {
					t.Fatalf("client and source results were confused: %+v", state)
				}
				assertStartupStats(t, group.Items[0], wantSuccess, 1-wantSuccess)
				if mode == "success" {
					channel, _ := op.ChannelGet(group.Items[0].ChannelID)
					if channel.InputToken != 3 || channel.OutputToken != 5 {
						t.Fatalf("observed source usage lost or duplicated: %+v, want 3/5", channel.StatsMetrics)
					}
				}
			})
		}
	}
}

func TestStreamConversionEmptyTailLimits(t *testing.T) {
	for _, direction := range conversionDirections() {
		// Responses' response.completed is already a source terminal. Chat
		// and Anthropic have a separate finish_reason before their terminator.
		if direction.upstream.name == "responses" {
			continue
		}
		for _, boundary := range []string{"events-exact", "events-over", "bytes-over"} {
			t.Run(direction.name()+"/"+boundary, func(t *testing.T) {
				prelude := direction.upstream.prelude[0]
				prefix, tail := 512, 511
				if boundary == "events-over" {
					tail++
				} else if boundary == "bytes-over" {
					prefix, tail = 1, 1
					divisor := 2
					if direction.upstream.name == "anthropic" {
						divisor = 3 // Anthropic also repeats its model at finish_reason.
					}
					prelude = strings.Replace(prelude, "upstream-model", strings.Repeat("m", maxStreamStartupBytes/divisor+100), 1)
				}
				wire := strings.Repeat(compatSSE(prelude), prefix) + compatSSE(direction.upstream.ending[:len(direction.upstream.ending)-1]...) +
					strings.Repeat(compatSSE(prelude), tail) + compatSSE(direction.upstream.ending[len(direction.upstream.ending)-1])
				trips := countConversionRoundTrips(t)
				engine, group := compatRelay(t, direction.upstream.protocol, direction.client.format, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				})
				writer := httptest.NewRecorder()
				serveWithTimeout(t, engine, writer, compatRequest(t, group, direction.client.format, true, nil))
				state := compatState(t, group)
				if trips.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 {
					t.Fatal("empty-tail pre-read changed actual sends")
				}
				if boundary == "events-exact" {
					if state.Status != StatusSuccess {
						t.Fatalf("valid combined boundary failed: %s", state.LastError)
					}
					assertConvertedContent(t, direction, writer.Body.String(), true)
					assertStartupStats(t, group.Items[0], 1, 0)
				} else {
					if state.Status != StatusFailed || writer.Flushed || state.OutputChars != 0 || !strings.Contains(state.LastError, "buffer limit exceeded") {
						t.Fatalf("prefix/tail exceeded the shared budget: status=%s error=%s", state.Status, state.LastError)
					}
					assertStartupStats(t, group.Items[0], 0, 1)
				}
			})
		}
	}
}

func TestStreamConversionDelivery(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, mode := range []string{"success", "short-terminal", "write-error", "cancel-before-terminal", "cancel-after-terminal", "short-first-empty", "write-first-empty", "cancel-after-empty-terminal"} {
			t.Run(direction.name()+"/"+mode, func(t *testing.T) {
				empty := strings.Contains(mode, "empty")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, direction.success(empty))
				}))
				defer server.Close()
				trips := countConversionRoundTrips(t)
				config := testRelayConfig(1)
				config.ManualMaxAttempts = 1
				group := startupGroup(t, direction.upstream, model.GroupModeManual, config, server.URL)
				setConcurrency(t, group.Items[0].ChannelID, 1)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				writer := &startupDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, terminal: conversionTerminal(direction.client.format), cancel: cancel}
				startupEngine(direction.client).ServeHTTP(writer, compatRequest(t, group, direction.client.format, true, nil).WithContext(ctx))
				state := compatState(t, group)
				wantStatus, wantSuccess := StatusFailed, int64(0)
				switch mode {
				case "success", "cancel-after-terminal", "cancel-after-empty-terminal":
					wantStatus, wantSuccess = StatusSuccess, 1
					if !writer.terminalFlushed {
						t.Fatal("client success terminal was not fully written/flushed")
					}
				case "cancel-before-terminal":
					wantStatus = StatusCanceled
				}
				if state.Status != wantStatus || trips.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 0 {
					t.Fatalf("delivery/source/cancel boundary changed: trips=%d state=%+v", trips.Load(), state)
				}
				assertStartupStats(t, group.Items[0], wantSuccess, 1-wantSuccess)
				if mode != "cancel-before-terminal" {
					channel, err := op.ChannelGet(group.Items[0].ChannelID)
					if err != nil {
						t.Fatal(err)
					}
					if channel.InputToken != 3 || channel.OutputToken != 5 {
						t.Fatalf("observed usage lost or counted twice: %+v, want 3/5", channel.StatsMetrics)
					}
				}
			})
		}
	}
}

func TestStreamConversionLateUsage(t *testing.T) {
	for _, direction := range conversionDirections() {
		for _, empty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/empty-%t", direction.name(), empty), func(t *testing.T) {
				events := slices.Clone(direction.upstream.prelude)
				if !empty {
					events = append(events, direction.upstream.content)
				}
				switch direction.upstream.name {
				case "chat":
					// The client converter can finish on the first usage-only
					// chunk. The real source still owns the final usage snapshot.
					events = append(events, direction.upstream.ending[0],
						`{"id":"chat-start","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
						`{"id":"chat-start","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":17,"total_tokens":30}}`,
						`[DONE]`)
				case "anthropic":
					events = append(events, direction.upstream.ending[:len(direction.upstream.ending)-1]...)
					events = append(events,
						`{"type":"message_delta","delta":{},"usage":{"input_tokens":13,"output_tokens":17}}`,
						direction.upstream.ending[len(direction.upstream.ending)-1])
				case "responses":
					events = append(events, strings.NewReplacer(`"input_tokens":3`, `"input_tokens":13`, `"output_tokens":5`, `"output_tokens":17`, `"total_tokens":8`, `"total_tokens":30`).Replace(direction.upstream.ending[0]))
				}
				trips := countConversionRoundTrips(t)
				engine, group := compatRelay(t, direction.upstream.protocol, direction.client.format, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, compatSSE(events...))
				})
				writer := httptest.NewRecorder()
				serveWithTimeout(t, engine, writer, compatRequest(t, group, direction.client.format, true, nil))
				state := compatState(t, group)
				if state.Status != StatusSuccess || trips.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 {
					t.Fatalf("late source usage changed delivery or attempts: status=%s error=%s trips=%d", state.Status, state.LastError, trips.Load())
				}
				assertConvertedContent(t, direction, writer.Body.String(), empty)
				assertStartupStats(t, group.Items[0], 1, 0)
				channel, err := op.ChannelGet(group.Items[0].ChannelID)
				if err != nil {
					t.Fatal(err)
				}
				if channel.InputToken != 13 || channel.OutputToken != 17 {
					t.Fatalf("final source usage lost or counted twice: got %d/%d, want 13/17", channel.InputToken, channel.OutputToken)
				}
			})
		}
	}
}
