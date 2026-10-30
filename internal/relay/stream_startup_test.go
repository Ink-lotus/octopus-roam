package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

type startupProtocol struct {
	name              string
	protocol          model.Protocol
	format            llm.APIFormat
	prelude, ending   []string
	content, failure  string
	tool, thinking    string
	refusal, extended string
}

func startupProtocols() []startupProtocol {
	return []startupProtocol{
		{
			name: "chat", protocol: model.ProtocolOpenAIChatCompletion, format: llm.APIFormatOpenAIChatCompletion,
			prelude: []string{
				`{"id":"chat-start","object":"chat.completion.chunk","created":1,"model":"upstream-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
				`{"id":"chat-start","choices":[{"index":0,"delta":{"content":"","tool_calls":[]},"finish_reason":null}]}`,
			},
			content: `{"id":"chat-start","object":"chat.completion.chunk","created":1,"model":"upstream-model","choices":[{"index":0,"delta":{"content":"pong"},"finish_reason":null}]}`,
			ending: []string{
				`{"id":"chat-start","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
				`[DONE]`,
			},
			failure:  `{"error":{"message":"synthetic startup failure","type":"server_error","code":"synthetic_code"}}`,
			tool:     `{"id":"chat-start","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-start","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`,
			thinking: `{"id":"chat-start","choices":[{"index":0,"delta":{"reasoning_content":"thinking"}}]}`,
			refusal:  `{"id":"chat-start","choices":[{"index":0,"delta":{"refusal":"refused"}}]}`,
			extended: `{"id":"chat-start","choices":[{"index":0,"delta":{"role":"assistant","vendor_output":{"text":"extension"}}}]}`,
		},
		{
			name: "responses", protocol: model.ProtocolOpenAIResponse, format: llm.APIFormatOpenAIResponse,
			prelude: []string{
				compatResponseCreated,
				`{"type":"response.queued","sequence_number":1,"response":{"status":"queued","output":[]}}`,
				`{"type":"response.in_progress","sequence_number":2,"response":{"status":"in_progress","output":[]}}`,
				`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg-start","role":"assistant","status":"in_progress","content":[]}}`,
				`{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
			},
			content:  `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"pong"}`,
			ending:   []string{compatResponseCompleted},
			failure:  `{"type":"response.failed","response":{"status":"failed","error":{"message":"synthetic startup failure","type":"server_error","code":"synthetic_code"}}}`,
			tool:     `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item-start","call_id":"call-start","name":"lookup","arguments":""}}`,
			thinking: `{"type":"response.reasoning_text.delta","output_index":0,"content_index":0,"delta":"thinking"}`,
			refusal:  `{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"refused"}`,
			extended: `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","vendor_output":"extension"}}`,
		},
		{
			name: "anthropic", protocol: model.ProtocolAnthropicMessage, format: llm.APIFormatAnthropicMessage,
			prelude: []string{
				`{"type":"message_start","message":{"id":"msg-start","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
				`{"type":"ping"}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			},
			content: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
			ending: []string{
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
				`{"type":"message_stop"}`,
			},
			failure:  `{"type":"error","error":{"type":"overloaded_error","message":"synthetic startup failure"},"request_id":"synthetic-request"}`,
			tool:     `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-start","name":"lookup","input":{}}}`,
			thinking: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"thinking"}}`,
			refusal:  `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`,
			extended: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","vendor_output":"extension"}}`,
		},
	}
}

func startupWire(tag string, events ...string) string {
	var body strings.Builder
	for i, event := range events {
		fmt.Fprintf(&body, "id: %s-%d\n%s", tag, i, compatSSE(event))
	}
	return body.String()
}

func startupGroup(t *testing.T, protocol startupProtocol, mode model.GroupMode, config model.GroupRelayConfig, urls ...string) model.Group {
	t.Helper()
	var grants []int
	for i, url := range urls {
		grant := newTestChannel(t, testName(t, fmt.Sprintf("startup-%d", i)), url, "upstream-model")
		detail, err := op.ChannelDetailGet(channelIDOfGrant(t, grant))
		if err != nil {
			t.Fatal(err)
		}
		detail.Grants[0].Protocols = protocol.protocol
		if _, err := op.ChannelUpdate(&detail, t.Context()); err != nil {
			t.Fatal(err)
		}
		grants = append(grants, grant)
	}
	name := newTestGroup(t, mode, config, grants...)
	group, err := op.GroupGetByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return group
}

func startupEngine(protocol startupProtocol) *gin.Engine {
	engine := gin.New()
	engine.POST("/compat", Forward(protocol.format))
	return engine
}

func assertStartupWire(t *testing.T, actual, expected string) {
	t.Helper()
	got, want := compatEvents(t, actual), compatEvents(t, expected)
	if len(got) != len(want) {
		t.Fatalf("client events = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Data, want[i].Data) || got[i].Type != want[i].Type || got[i].LastEventID != want[i].LastEventID {
			t.Fatalf("event %d changed data/type/id: got %.250s, want %.250s", i, got[i].Data, want[i].Data)
		}
	}
}

func assertStartupStats(t *testing.T, item model.GroupItem, success, failed int64) {
	t.Helper()
	channel, err := op.ChannelGet(item.ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := op.ChannelGrantGet(item.ChannelGrantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, stats := range []model.StatsMetrics{channel.StatsMetrics, grant.ChannelKey.StatsMetrics, grant.ChannelModel.StatsMetrics} {
		if stats.RequestSuccess != success || stats.RequestFailed != failed {
			t.Fatalf("channel/key/model result counted incorrectly: %+v, want %d/%d", stats, success, failed)
		}
	}
	awaitCapacity(t, item.ChannelID, 0, 0)
}

func TestStreamStartupPassthrough(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, mode := range []string{"content", "retry-error", "retry-eof", "retry-overflow", "startup-eof", "startup-read-error", "malformed", "empty", "empty-success"} {
			t.Run(protocol.name+"/"+mode, func(t *testing.T) {
				accepted := slices.Clone(protocol.prelude)
				if mode != "empty-success" {
					accepted = append(accepted, protocol.content)
				}
				accepted = append(accepted, protocol.ending...)
				wantWire := startupWire("accepted", accepted...)
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					n := calls.Add(1)
					body := wantWire
					switch mode {
					case "retry-error":
						if n == 1 {
							body = startupWire("failed-round", append(slices.Clone(protocol.prelude), protocol.failure)...)
						}
					case "retry-eof":
						if n == 1 {
							body = startupWire("failed-round", protocol.prelude...)
						}
					case "retry-overflow":
						if n == 1 {
							body = strings.Repeat(startupWire("failed-round", protocol.prelude[0]), maxStreamStartupEvents+1)
						}
					case "startup-eof", "startup-read-error":
						body = startupWire("failed-round", protocol.prelude...)
					case "malformed":
						body = startupWire("failed-round", protocol.prelude...) + "data: {\"broken\":\n\n"
					case "empty":
						body = ""
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if mode == "startup-read-error" {
						w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
					}
					_, _ = io.WriteString(w, body)
				}))
				defer upstream.Close()
				config := testRelayConfig(2)
				config.ManualMaxAttempts = 1
				wantCalls := 1
				if strings.HasPrefix(mode, "retry-") {
					config.ManualMaxAttempts, wantCalls = 2, 2
				}
				group := startupGroup(t, protocol, model.GroupModeManual, config, upstream.URL)
				setConcurrency(t, group.Items[0].ChannelID, 1)
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, startupEngine(protocol), recorder, compatRequest(t, group, protocol.format, true, nil))
				state := compatState(t, group)
				if int(calls.Load()) != wantCalls || state.Round != wantCalls || state.Attempt != wantCalls || state.RetryCount != wantCalls-1 {
					t.Fatalf("actual sends/attempts: calls=%d state=%+v", calls.Load(), state)
				}
				success := mode == "content" || strings.HasPrefix(mode, "retry-") || mode == "empty-success"
				wantSuccess, wantErrors := int64(0), 1
				if success {
					wantSuccess, wantErrors = 1, wantCalls-1
					if recorder.Code != http.StatusOK || state.Status != StatusSuccess {
						t.Fatalf("valid stream failed: status=%d state=%+v", recorder.Code, state)
					}
					assertStartupWire(t, recorder.Body.String(), wantWire)
					// A localhost round can complete within one Windows clock tick;
					// the controlled barrier test below verifies the positive wait.
					if state.OutputChars != len(accepted) || state.FirstTokenDuration < 0 || state.FirstTokenDuration > state.Duration {
						t.Fatalf("output/timing includes a failed round: %+v", state)
					}
				} else if recorder.Code != http.StatusServiceUnavailable || recorder.Flushed || state.Status != StatusFailed || state.OutputChars != 0 || state.FirstTokenDuration != 0 {
					t.Fatalf("startup failure committed response data: status=%d flushed=%t state=%+v", recorder.Code, recorder.Flushed, state)
				}
				if strings.Contains(recorder.Body.String(), "failed-round") || state.ErrorCount != wantErrors {
					t.Fatalf("failed startup leaked or failure duplicated: %+v body=%.200s", state, recorder.Body.String())
				}
				health := healthOf(t, group.ID).Members[group.Items[0].ID]
				wantStreak := 1 - int(wantSuccess)
				if health.FailureStreak != wantStreak || (strings.HasPrefix(mode, "retry-") && health.Epoch != 1) {
					t.Fatalf("health changed at the startup frame: %+v", health)
				}
				assertStartupStats(t, group.Items[0], wantSuccess, int64(wantCalls)-wantSuccess)
			})
		}
	}
}

type startupKind struct{ name, event string }

func startupOutputKinds(protocol startupProtocol) []startupKind {
	kinds := []startupKind{
		{"content", protocol.content}, {"tool", protocol.tool}, {"thinking", protocol.thinking},
		{"refusal", protocol.refusal}, {"extension", protocol.extended},
		{"unknown-event", `{"type":"vendor.output","payload":"extension"}`},
	}
	switch protocol.name {
	case "chat":
		kinds = append(kinds,
			startupKind{"audio-start", `{"choices":[{"delta":{"audio":{"id":"audio-start","data":""}}}]}`},
			startupKind{"citations", `{"choices":[],"citations":["synthetic citation"]}`},
			startupKind{"logprobs", `{"choices":[{"delta":{},"logprobs":{"refusal":[{"token":"no","logprob":0}]}}]}`},
			startupKind{"message", `{"choices":[{"message":{"role":"assistant","content":""}}]}`},
			startupKind{"root-extension", `{"choices":[],"vendor_output":"extension"}`},
			startupKind{"finish", compatChatFinish},
		)
	case "responses":
		kinds = append(kinds,
			startupKind{"created-output", `{"type":"response.created","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ready"}]}]}}`},
			startupKind{"encrypted", `{"type":"response.output_item.added","item":{"type":"reasoning","encrypted_content":"encrypted"}}`},
			startupKind{"reasoning-item", `{"type":"response.output_item.added","item":{"type":"reasoning","reasoning_content":"thinking"}}`},
			startupKind{"summary", `{"type":"response.reasoning_summary_part.added","part":{"type":"summary_text","text":"summary"}}`},
			startupKind{"annotations", `{"type":"response.content_part.added","part":{"type":"output_text","text":"","annotations":[{"type":"url_citation","url":"https://example.invalid","title":"synthetic"}]}}`},
			startupKind{"root-extension", `{"type":"response.in_progress","vendor_output":"extension"}`},
			startupKind{"response-extension", `{"type":"response.created","response":{"output":[],"vendor_output":"extension"}}`},
		)
	case "anthropic":
		kinds = append(kinds,
			startupKind{"signature", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signature"}}`},
			startupKind{"block-signature", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"signature"}}`},
			startupKind{"citations", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","citations":[{"type":"char_location","cited_text":"quote","document_index":0,"start_char_index":0,"end_char_index":5}]}}`},
			startupKind{"partial-json", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`},
			startupKind{"root-extension", `{"type":"ping","vendor_output":"extension"}`},
			startupKind{"message-extension", `{"type":"message_start","message":{"content":[],"vendor_output":"extension"}}`},
		)
	}
	return kinds
}

func TestStreamStartupOutputStopsRetry(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, kind := range startupOutputKinds(protocol) {
			t.Run(protocol.name+"/"+kind.name, func(t *testing.T) {
				wire := startupWire("committed", append(append(slices.Clone(protocol.prelude), kind.event), protocol.failure)...)
				var firstCalls, spareCalls atomic.Int32
				first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					firstCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				}))
				defer first.Close()
				spareWire := startupWire("spare", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
				spare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					spareCalls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, spareWire)
				}))
				defer spare.Close()
				group := startupGroup(t, protocol, model.GroupModeFailover, testRelayConfig(1), first.URL, spare.URL)
				engine := startupEngine(protocol)
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, protocol.format, true, nil))
				state := compatState(t, group)
				if firstCalls.Load() != 1 || spareCalls.Load() != 0 || recorder.Code != http.StatusOK || !recorder.Flushed || state.Status != StatusFailed || state.Attempt != 1 || state.RetryCount != 0 || state.ErrorCount != 1 {
					t.Fatalf("output was held/replayed after commitment: first=%d spare=%d state=%+v", firstCalls.Load(), spareCalls.Load(), state)
				}
				assertStartupWire(t, recorder.Body.String(), wire)
				if health := healthOf(t, group.ID).Members[group.Items[0].ID]; health.FailureStreak != 1 {
					t.Fatalf("late failure missing from shared health: %+v", health)
				}
				assertStartupStats(t, group.Items[0], 0, 1)
				// A separate request must respect the failed member's cooldown.
				next := httptest.NewRecorder()
				serveWithTimeout(t, engine, next, compatRequest(t, group, protocol.format, true, nil))
				if firstCalls.Load() != 1 || spareCalls.Load() != 1 || compatState(t, group).Status != StatusSuccess {
					t.Fatal("next request bypassed the established shared cooldown")
				}
				assertStartupWire(t, next.Body.String(), spareWire)
				assertStartupStats(t, group.Items[1], 1, 0)
			})
		}
	}
}

type startupReusingStream struct {
	events  []*httpclient.StreamEvent
	current httpclient.StreamEvent
	index   int
	closed  int
}

func (s *startupReusingStream) Next() bool {
	if s.index >= len(s.events) {
		return false
	}
	event := s.events[s.index]
	s.index++
	s.current.Data = append(s.current.Data[:0], event.Data...)
	s.current.Type, s.current.LastEventID = event.Type, event.LastEventID
	if s.current.Headers == nil {
		s.current.Headers = make(http.Header)
	}
	s.current.Headers.Set("X-Synthetic", event.Headers.Get("X-Synthetic"))
	return true
}

func (s *startupReusingStream) Current() *httpclient.StreamEvent { return &s.current }
func (s *startupReusingStream) Err() error                       { return nil }
func (s *startupReusingStream) Close() error                     { s.closed++; return nil }

func TestStreamStartupOwnershipAndCapabilities(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			events := compatEvents(t, startupWire("owned", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...))
			for i, event := range events {
				event.Headers = http.Header{"X-Synthetic": []string{fmt.Sprint(i)}}
			}
			source := &startupReusingStream{events: events}
			probe := &deliveryProbe{Stream: source, format: protocol.format}
			result, err := readStreamStartup(t.Context(), protocol.format, probe)
			if err != nil {
				t.Fatal(err)
			}
			if result.events != probe || source.index != len(protocol.prelude)+1 || probe.acks != 0 || probe.closes != 0 {
				t.Fatal("pre-read changed capabilities, advanced past output, or acknowledged delivery")
			}
			owned := append([]*httpclient.StreamEvent{result.first}, result.buffered...)
			// Advance the source to overwrite its one reusable object and byte array.
			if !source.Next() {
				t.Fatal("missing trailing source event")
			}
			for i, event := range owned {
				if !reflect.DeepEqual(event, events[i]) {
					t.Fatalf("buffered event %d aliases the source", i)
				}
			}
			result.events.Close()
			waitForStreamFinalization(result.events)
			if probe.closes != 1 || probe.waits != 1 || source.closed != 1 {
				t.Fatal("source ownership or finalization was lost")
			}
		})
	}
}

func TestStreamStartupPreservesErrorIdentity(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			source := &startupReusingStream{events: compatEvents(t, startupWire("failure", append(slices.Clone(protocol.prelude), protocol.failure)...))}
			result, err := readStreamStartup(t.Context(), protocol.format, source)
			var failure *llm.ResponseError
			if result != nil || !errors.As(err, &failure) || source.closed != 1 {
				t.Fatalf("typed upstream error or close was lost: result=%v err=%v closes=%d", result, err, source.closed)
			}
			if failure.Detail.Message != "synthetic startup failure" || (protocol.name != "anthropic" && failure.Detail.Code != "synthetic_code") ||
				(protocol.name == "anthropic" && (failure.Detail.Type != "overloaded_error" || failure.Detail.RequestID != "synthetic-request")) {
				t.Fatalf("upstream error identity changed: %+v", failure.Detail)
			}
		})
	}
}

type startupWriter struct {
	*httptest.ResponseRecorder
	writes, flushes atomic.Int32
	wrote           chan struct{}
}

func (w *startupWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(data)
	w.writes.Add(1)
	if w.wrote != nil {
		select {
		case w.wrote <- struct{}{}:
		default:
		}
	}
	return n, err
}

func (w *startupWriter) Flush() {
	w.ResponseRecorder.Flush()
	w.flushes.Add(1)
}

func awaitStartupSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("startup test barrier timed out")
	}
}

func startupState(t *testing.T, id uint64) RequestState {
	t.Helper()
	states, updates := OpenRequestStream()
	CloseRequestStream(updates)
	for _, state := range states {
		if state.ID == id {
			return state
		}
	}
	t.Fatal("missing startup request state")
	return RequestState{}
}

func TestStreamStartupWaitsForOutput(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			started, output, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
			prefix := startupWire("startup", protocol.prelude...)
			content := startupWire("output", protocol.content)
			ending := startupWire("finish", protocol.ending...)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, prefix)
				w.(http.Flusher).Flush()
				close(started)
				select {
				case <-output:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, content)
				w.(http.Flusher).Flush()
				select {
				case <-finish:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, ending)
			}))
			defer upstream.Close()
			config := testRelayConfig(1)
			config.ManualMaxAttempts = 1
			group := startupGroup(t, protocol, model.GroupModeManual, config, upstream.URL)
			setConcurrency(t, group.Items[0].ChannelID, 1)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			writer := &startupWriter{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{}, 32)}
			done := make(chan struct{})
			go func() {
				defer close(done)
				startupEngine(protocol).ServeHTTP(writer, compatRequest(t, group, protocol.format, true, nil).WithContext(ctx))
			}()
			awaitStartupSignal(t, started)
			select {
			case <-writer.wrote:
				t.Fatal("startup frame reached the client before output")
			case <-time.After(50 * time.Millisecond):
			}
			state := compatState(t, group)
			if !state.Sending || state.FirstTokenDuration != 0 || state.OutputChars != 0 || writer.flushes.Load() != 0 {
				t.Fatalf("startup prematurely ended response timing: %+v", state)
			}
			awaitCapacity(t, group.Items[0].ChannelID, 1, 0)
			released := time.Now()
			close(output)
			awaitStartupSignal(t, writer.wrote)
			select {
			case <-done:
				t.Fatal("stream finished before provider termination")
			case <-time.After(20 * time.Millisecond):
			}
			close(finish)
			awaitStartupSignal(t, done)
			state = compatState(t, group)
			if state.Status != StatusSuccess || state.FirstTokenDuration < released.Sub(state.RoundStartedAt) || state.StreamDuration < 20*time.Millisecond {
				t.Fatalf("first-output or response-phase duration is wrong: %+v", state)
			}
			assertStartupWire(t, writer.Body.String(), prefix+content+ending)
			assertStartupStats(t, group.Items[0], 1, 0)
		})
	}
}

func TestStreamStartupTimeout(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			closed := make(chan struct{})
			var frames, calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				defer close(closed)
				w.Header().Set("Content-Type", "text/event-stream")
				ticker := time.NewTicker(40 * time.Millisecond)
				defer ticker.Stop()
				for {
					_, _ = io.WriteString(w, ": heartbeat without data\n\n")
					_, _ = io.WriteString(w, startupWire("never-commit", protocol.prelude[0]))
					w.(http.Flusher).Flush()
					frames.Add(1)
					select {
					case <-ticker.C:
					case <-r.Context().Done():
						return
					}
				}
			}))
			defer upstream.Close()
			config := testRelayConfig(1)
			config.ManualMaxAttempts, config.MemberStreamFirstEventTimeoutSeconds = 1, 1
			group := startupGroup(t, protocol, model.GroupModeManual, config, upstream.URL)
			recorder := httptest.NewRecorder()
			began := time.Now()
			serveWithTimeout(t, startupEngine(protocol), recorder, compatRequest(t, group, protocol.format, true, nil))
			elapsed := time.Since(began)
			awaitStartupSignal(t, closed)
			state := compatState(t, group)
			if elapsed < 800*time.Millisecond || elapsed > 2500*time.Millisecond || calls.Load() != 1 || frames.Load() < 3 {
				t.Fatalf("heartbeats changed timeout: duration=%s calls=%d frames=%d", elapsed, calls.Load(), frames.Load())
			}
			if recorder.Code != http.StatusServiceUnavailable || recorder.Flushed || state.Status != StatusFailed || state.Attempt != 1 || state.ErrorCount != 1 || state.RetryCount != 0 || state.FirstTokenDuration != 0 || state.OutputChars != 0 || !strings.Contains(state.LastError, "first event timeout") {
				t.Fatalf("timeout committed or changed accounting: status=%d state=%+v", recorder.Code, state)
			}
			assertStartupStats(t, group.Items[0], 0, 1)
		})
	}
}

func TestStreamStartupCancellationAndQueue(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
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
			engine := startupEngine(protocol)
			start := func(ctx context.Context) (*startupWriter, <-chan struct{}) {
				writer := &startupWriter{ResponseRecorder: httptest.NewRecorder()}
				done := make(chan struct{})
				request := compatRequest(t, group, protocol.format, true, nil).WithContext(ctx)
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

func TestStreamStartupBufferLimits(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, boundary := range []string{"events-exact", "events-over", "bytes-exact", "bytes-over", "large-output"} {
			t.Run(protocol.name+"/"+boundary, func(t *testing.T) {
				prelude := compatEvents(t, startupWire("boundary", protocol.prelude[0]))[0]
				output := compatEvents(t, compatSSE(protocol.content))[0]
				count := 1
				switch boundary {
				case "events-exact", "large-output":
					count = maxStreamStartupEvents
				case "events-over":
					count = maxStreamStartupEvents + 1
				case "bytes-exact", "bytes-over":
					size := maxStreamStartupBytes - len(prelude.Type) - len(prelude.LastEventID)
					if boundary == "bytes-over" {
						size++
					}
					prelude.Data = append(prelude.Data, bytes.Repeat([]byte(" "), size-len(prelude.Data))...)
				}
				if boundary == "large-output" {
					output.Data = []byte(strings.Replace(string(output.Data), "pong", strings.Repeat("x", maxStreamStartupBytes+1), 1))
				}
				events := make([]*httpclient.StreamEvent, count, count+1)
				for i := range events {
					events[i] = prelude
				}
				events = append(events, output)
				probe := &deliveryProbe{Stream: streams.SliceStream(events), format: protocol.format}
				result, err := readStreamStartup(t.Context(), protocol.format, probe)
				over := strings.HasSuffix(boundary, "-over")
				if over {
					if result != nil || !errors.Is(err, errStreamStartupLimit) || probe.closes != 1 || probe.acks != 0 {
						t.Fatalf("overflow was committed or leaked: err=%v closes=%d", err, probe.closes)
					}
					return
				}
				if err != nil || result == nil || len(result.buffered) != count || result.events != probe {
					t.Fatalf("boundary rejected valid output: err=%v", err)
				}
				if !bytes.Equal(result.first.Data, prelude.Data) || !bytes.Equal(result.buffered[len(result.buffered)-1].Data, output.Data) {
					t.Fatal("boundary changed buffered or large output bytes")
				}
				result.events.Close()
				if probe.closes != 1 || probe.acks != 0 {
					t.Fatal("pre-read changed delivery or close ownership")
				}
			})
		}
	}
}

func TestStreamStartupLimitsThroughRelay(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, boundary := range []string{"events-exact", "events-over", "bytes-exact", "bytes-over", "large-output"} {
			t.Run(protocol.name+"/"+boundary, func(t *testing.T) {
				prelude := protocol.prelude[0]
				content := protocol.content
				count := 1
				switch boundary {
				case "events-exact", "large-output":
					count = maxStreamStartupEvents
				case "events-over":
					count = maxStreamStartupEvents + 1
				case "bytes-exact", "bytes-over":
					event := compatEvents(t, startupWire("limit", prelude))[0]
					size := maxStreamStartupBytes - len(event.Type) - len(event.LastEventID)
					if boundary == "bytes-over" {
						size++
					}
					prelude += strings.Repeat(" ", size-len(prelude))
				}
				if boundary == "large-output" {
					content = strings.Replace(content, "pong", strings.Repeat("x", maxStreamStartupBytes+1), 1)
				}
				events := make([]string, count, count+1+len(protocol.ending))
				for i := range events {
					events[i] = prelude
				}
				events = append(events, content)
				events = append(events, protocol.ending...)
				wire := startupWire("limit", events...)
				var calls atomic.Int32
				engine, group := compatRelay(t, protocol.protocol, protocol.format, func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, wire)
				})
				setConcurrency(t, group.Items[0].ChannelID, 1)
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, protocol.format, true, nil))
				state := compatState(t, group)
				if calls.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 {
					t.Fatalf("limit check changed actual attempts: calls=%d state=%+v", calls.Load(), state)
				}
				if strings.HasSuffix(boundary, "-over") {
					if state.Status != StatusFailed || recorder.Code != http.StatusServiceUnavailable || recorder.Flushed || state.OutputChars != 0 || !strings.Contains(state.LastError, errStreamStartupLimit.Error()) {
						t.Fatalf("overflow committed startup data: code=%d state=%+v", recorder.Code, state)
					}
					assertStartupStats(t, group.Items[0], 0, 1)
				} else {
					if state.Status != StatusSuccess || recorder.Code != http.StatusOK || state.OutputChars != len(events) {
						t.Fatalf("valid large/boundary output was rejected: code=%d state=%+v", recorder.Code, state)
					}
					assertStartupWire(t, recorder.Body.String(), wire)
					assertStartupStats(t, group.Items[0], 1, 0)
				}
			})
		}
	}
}

type startupDeliveryWriter struct {
	*httptest.ResponseRecorder
	mode, terminal                   string
	cancel                           context.CancelFunc
	writes                           int
	terminalWritten, terminalFlushed bool
}

func (w *startupDeliveryWriter) Write(data []byte) (int, error) {
	w.writes++
	terminal := strings.Contains(string(data), w.terminal)
	if (terminal && w.mode == "short-terminal") || (w.writes == 1 && w.mode == "short-first-empty") {
		return w.ResponseRecorder.Write(data[:len(data)-1])
	}
	if (terminal && w.mode == "write-error") || (w.writes == 1 && w.mode == "write-first-empty") {
		return 0, io.ErrClosedPipe
	}
	n, err := w.ResponseRecorder.Write(data)
	w.terminalWritten = terminal && err == nil && n == len(data)
	if w.mode == "cancel-before-terminal" && strings.Contains(string(data), "pong") {
		w.cancel()
	}
	return n, err
}

func (w *startupDeliveryWriter) Flush() {
	w.ResponseRecorder.Flush()
	if w.terminalWritten {
		w.terminalFlushed = true
		if w.mode == "cancel-after-terminal" || w.mode == "cancel-after-empty-terminal" {
			w.cancel()
		}
	}
}

func TestStreamStartupDeliveryAndConnections(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, mode := range []string{"success", "short-terminal", "write-error", "cancel-before-terminal", "cancel-after-terminal", "short-first-empty", "write-first-empty", "cancel-after-empty-terminal", "startup-error", "startup-overflow"} {
			t.Run(protocol.name+"/"+mode, func(t *testing.T) {
				events := append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)
				if strings.Contains(mode, "empty") {
					events = append(slices.Clone(protocol.prelude), protocol.ending[len(protocol.ending)-1])
				} else if mode == "startup-error" {
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
				writer := &startupDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), mode: mode, terminal: protocol.ending[len(protocol.ending)-1], cancel: cancel}
				startupEngine(protocol).ServeHTTP(writer, compatRequest(t, group, protocol.format, true, nil).WithContext(ctx))
				awaitStartupSignal(t, closed)
				if connections.Load() != 1 || closes.Load() != 1 || calls.Load() != 1 {
					t.Fatalf("connection/send ownership: opened=%d closed=%d sent=%d", connections.Load(), closes.Load(), calls.Load())
				}
				state := compatState(t, group)
				wantStatus, wantSuccess, wantFailures := StatusFailed, int64(0), 0
				switch mode {
				case "success", "cancel-after-terminal", "cancel-after-empty-terminal":
					wantStatus, wantSuccess = StatusSuccess, 1
					if !writer.terminalFlushed {
						t.Fatal("successful terminal was not flushed")
					}
					assertStartupWire(t, writer.Body.String(), wire)
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
				if mode != "cancel-before-terminal" && mode != "startup-error" && mode != "startup-overflow" {
					input, output := int64(3), int64(5)
					if strings.Contains(mode, "empty") && protocol.name != "responses" {
						output = 0
						if protocol.name == "chat" {
							input = 0
						}
					}
					channel, _ := op.ChannelGet(group.Items[0].ChannelID)
					if channel.InputToken != input || channel.OutputToken != output {
						t.Fatalf("observed source usage lost or duplicated: %+v, want %d/%d", channel.StatsMetrics, input, output)
					}
				}
			})
		}
	}
}

func TestStreamStartupExclusiveRecovery(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			arrived := make(chan int32, 16)
			var calls atomic.Int32
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
			engine := startupEngine(protocol)
			engine.ServeHTTP(httptest.NewRecorder(), compatRequest(t, group, protocol.format, true, nil))
			<-arrived
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			writers := make([]*startupWriter, 5)
			for i := range writers {
				writers[i] = &startupWriter{ResponseRecorder: httptest.NewRecorder()}
				request := compatRequest(t, group, protocol.format, true, nil).WithContext(ctx)
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
			serveWithTimeout(t, engine, response, compatRequest(t, group, protocol.format, true, nil))
			if calls.Load() != 3 || compatState(t, group).Status != StatusSuccess || healthOf(t, group.ID).Members[group.Items[0].ID].FailureStreak != 0 {
				t.Fatal("canceled probe did not release recovery ownership")
			}
			assertStartupStats(t, group.Items[0], 1, 1)
		})
	}
}

func TestStreamStartupRouteChanges(t *testing.T) {
	for _, protocol := range startupProtocols() {
		for _, change := range []string{"select", "interrupt", "config", "disable", "delete"} {
			t.Run(protocol.name+"/"+change, func(t *testing.T) {
				arrived, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var oldCalls, newCalls atomic.Int32
				oldWire := startupWire("old", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
				newWire := startupWire("new", append(append(slices.Clone(protocol.prelude), protocol.content), protocol.ending...)...)
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
				engine := startupEngine(protocol)
				start := func() (*startupWriter, <-chan struct{}) {
					writer := &startupWriter{ResponseRecorder: httptest.NewRecorder()}
					done := make(chan struct{})
					request := compatRequest(t, group, protocol.format, true, nil).WithContext(ctx)
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
				assertStartupWire(t, queuedWriter.Body.String(), newWire)
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
				assertStartupWire(t, firstWriter.Body.String(), wantWire)
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

func TestStreamStartupEmptyKinds(t *testing.T) {
	for _, protocol := range startupProtocols() {
		var events []string
		switch protocol.name {
		case "chat":
			events = []string{
				`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0}}`,
				`{"choices":[{"index":0,"delta":{},"logprobs":{"content":[],"refusal":[]}}],"citations":[]}`,
				`{"choices":[{"index":0,"delta":{"role":"assistant","content":null,"reasoning_content":"","tool_calls":[],"vendor_output":null}}]}`,
			}
		case "responses":
			events = []string{
				`{"type":"response.created"}`, `{"type":"response.in_progress","response":null}`,
				`{"type":"response.output_item.added","item":{"type":"reasoning","content":[],"summary":[],"reasoning_content":null,"encrypted_content":""}}`,
				`{"type":"response.content_part.added","part":{"type":"reasoning_text","text":""}}`,
				`{"type":"response.content_part.added","part":{"type":"refusal","refusal":""}}`,
				`{"type":"response.reasoning_summary_part.added","summary_index":0,"part":{"type":"summary_text","text":""}}`,
				`{"type":"response.reasoning_summary_part.added","part":{"text":""}}`,
				`{"type":"response.output_text.delta","delta":""}`, `{"type":"response.reasoning_summary_text.delta","delta":""}`,
				`{"type":"response.reasoning_text.delta","delta":""}`, `{"type":"response.refusal.delta","delta":""}`,
			}
		case "anthropic":
			events = []string{
				`{"type":"message_start"}`, `{"type":"content_block_stop","index":0}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","text":"","thinking":"","signature":"","citations":[]}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`,
				`{"type":"message_delta","delta":{"stop_reason":null,"stop_sequence":""},"usage":{"output_tokens":0}}`,
			}
		}
		for i, event := range events {
			t.Run(fmt.Sprintf("%s/%02d", protocol.name, i), func(t *testing.T) {
				var calls atomic.Int32
				engine, group := compatRelay(t, protocol.protocol, protocol.format, func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, startupWire("must-not-commit", event, protocol.failure))
				})
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, protocol.format, true, nil))
				state := compatState(t, group)
				if calls.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 || state.Status != StatusFailed || state.ErrorCount != 1 || state.OutputChars != 0 || recorder.Code != http.StatusServiceUnavailable || recorder.Flushed || strings.Contains(recorder.Body.String(), "must-not-commit") {
					t.Fatalf("empty startup event committed instead of failing before output: %+v, body=%.250s", state, recorder.Body.String())
				}
				assertStartupStats(t, group.Items[0], 0, 1)
			})
		}
	}
}

func TestStreamStartupBackoffReleasesCapacity(t *testing.T) {
	for _, protocol := range startupProtocols() {
		t.Run(protocol.name, func(t *testing.T) {
			var calls atomic.Int32
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
			engine := startupEngine(protocol)
			first := httptest.NewRecorder()
			done := make(chan struct{})
			request := compatRequest(t, group, protocol.format, true, nil).WithContext(ctx)
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
			engine.ServeHTTP(second, compatRequest(t, other, protocol.format, true, nil).WithContext(ctx))
			if compatState(t, other).Status != StatusSuccess || calls.Load() != 2 || time.Now().UnixMilli() >= waiting.NextRetryAt {
				t.Fatal("backoff held the channel's capacity or allowed an early retry")
			}
			assertStartupWire(t, second.Body.String(), wire)
			awaitStartupSignal(t, done)
			state := compatState(t, group)
			if calls.Load() != 3 || state.Status != StatusSuccess || state.Attempt != 2 || state.RetryCount != 1 || state.ErrorCount != 1 {
				t.Fatalf("backoff retry was duplicated or miscounted: calls=%d state=%+v", calls.Load(), state)
			}
			assertStartupWire(t, first.Body.String(), wire)
			assertStartupStats(t, item, 2, 1)
		})
	}
}
