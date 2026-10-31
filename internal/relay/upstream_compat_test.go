package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/rhttp"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// These fixtures travel through Forward, the selected channel and a real HTTP
// server. Inspect both the wire response and the finished request/health state.
func compatRelay(t *testing.T, protocol model.Protocol, format llm.APIFormat, upstream http.HandlerFunc) (*gin.Engine, model.Group) {
	t.Helper()
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	config := testRelayConfig(1)
	config.ManualMaxAttempts = 1
	group := protocolHealthGroup(t, server.URL, protocol, config)
	engine := gin.New()
	engine.POST("/compat", Forward(format))
	return engine, group
}

func compatRequest(t *testing.T, group model.Group, format llm.APIFormat, streaming bool, extra map[string]any) *http.Request {
	t.Helper()
	payload := map[string]any{"model": group.Name, "stream": streaming}
	if format == llm.APIFormatOpenAIResponse {
		payload["input"] = "ping"
	} else {
		payload["messages"] = []any{map[string]any{"role": "user", "content": "ping"}}
		if format == llm.APIFormatAnthropicMessage {
			payload["max_tokens"] = 64
		}
	}
	for key, value := range extra {
		payload[key] = value
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/compat", strings.NewReader(string(body))).WithContext(t.Context())
	req.Header.Set("Content-Type", "application/json")
	return req
}

func compatState(t *testing.T, group model.Group) RequestState {
	t.Helper()
	states, updates := OpenRequestStream()
	CloseRequestStream(updates)
	for _, state := range states {
		if state.GroupID == group.ID {
			return state
		}
	}
	t.Fatal("Relay did not publish a request state")
	return RequestState{}
}

func compatEvents(t *testing.T, body string) []*httpclient.StreamEvent {
	t.Helper()
	decoder := httpclient.NewDefaultSSEDecoder(t.Context(), io.NopCloser(strings.NewReader(body)))
	defer decoder.Close()
	var events []*httpclient.StreamEvent
	for decoder.Next() {
		events = append(events, decoder.Current())
	}
	if err := decoder.Err(); err != nil {
		t.Fatalf("client SSE is invalid: %v", err)
	}
	return events
}

func compatJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("invalid JSON: %v: %.500s", err, body)
	}
	return value
}

func compatSSE(events ...string) string {
	var body strings.Builder
	for _, event := range events {
		var typed struct{ Type string }
		if json.Unmarshal([]byte(event), &typed) == nil && typed.Type != "" {
			fmt.Fprintf(&body, "event: %s\n", typed.Type)
		}
		fmt.Fprintf(&body, "data: %s\n\n", event)
	}
	return body.String()
}

const compatChatFinish = `{"id":"chatcmpl-test","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
const compatResponseCreated = `{"type":"response.created","response":{"id":"resp-compat","object":"response","created_at":1,"model":"upstream-model","status":"in_progress","output":[]}}`
const compatResponseCompleted = `{"type":"response.completed","response":{"id":"resp-compat","object":"response","created_at":1,"model":"upstream-model","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}}`

func TestUpstreamCompatEOF1AndEOF2(t *testing.T) {
	chatPrefix := strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n") + compatSSE(compatChatFinish)
	responsePrefix := compatSSE(compatResponseCreated, `{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"pong"}`)
	anthropicPrefix := compatSSE(
		`{"type":"message_start","message":{"id":"msg-compat","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
	)
	for _, route := range []struct {
		name     string
		protocol model.Protocol
		format   llm.APIFormat
		prefix   string
		terminal string
	}{
		{"chat", model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIChatCompletion, chatPrefix, "[DONE]"},
		{"chat-to-responses", model.ProtocolOpenAIChatCompletion, llm.APIFormatOpenAIResponse, chatPrefix, "[DONE]"},
		{"responses", model.ProtocolOpenAIResponse, llm.APIFormatOpenAIResponse, responsePrefix, compatResponseCompleted},
		{"responses-to-chat", model.ProtocolOpenAIResponse, llm.APIFormatOpenAIChatCompletion, responsePrefix, compatResponseCompleted},
		{"anthropic", model.ProtocolAnthropicMessage, llm.APIFormatAnthropicMessage, anthropicPrefix, `{"type":"message_stop"}`},
		{"anthropic-to-chat", model.ProtocolAnthropicMessage, llm.APIFormatOpenAIChatCompletion, anthropicPrefix, `{"type":"message_stop"}`},
	} {
		for _, ending := range []string{"unterminated-line", "lf", "crlf", "empty", "missing-terminal", "partial-json", "read-error"} {
			t.Run(route.name+"/"+ending, func(t *testing.T) {
				body := route.prefix + "data: " + route.terminal
				success := true
				switch ending {
				case "lf":
					body += "\n\n"
				case "crlf":
					body = strings.ReplaceAll(body, "\n", "\r\n") + "\r\n\r\n"
				case "empty":
					body, success = "", false
				case "missing-terminal", "read-error":
					body, success = route.prefix, false
				case "partial-json":
					body, success = route.prefix+`data: {"type":`, false
				}
				var calls atomic.Int32
				engine, group := compatRelay(t, route.protocol, route.format, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var request struct {
						Model  string `json:"model"`
						Stream bool   `json:"stream"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "upstream-model" || !request.Stream {
						t.Errorf("unexpected upstream request: %+v, %v", request, err)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if ending == "read-error" {
						// Truncated HTTP body gives a real transport read error, not a
						// synthetic EOF returned by a test stream.
						w.Header().Set("Content-Length", fmt.Sprint(len(body)+100))
					}
					_, _ = io.WriteString(w, body)
				})
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, route.format, true, nil))
				state := compatState(t, group)
				if calls.Load() != 1 || state.Attempt != 1 || state.RetryCount != 0 || state.TargetProtocol != route.protocol {
					t.Fatalf("wrong route/retry count: calls=%d state=%+v", calls.Load(), state)
				}
				wantFailures := 1
				if success {
					wantFailures = 0
					if state.Status != StatusSuccess || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "pong") {
						t.Fatalf("complete stream failed: state=%+v body=%s", state, recorder.Body)
					}
					terminals := 0
					for _, event := range compatEvents(t, recorder.Body.String()) {
						last, err := inspectStreamEvent(route.format, event)
						if err != nil {
							t.Fatal(err)
						}
						if last {
							terminals++
						}
					}
					if terminals != 1 {
						t.Fatalf("client received %d terminal events", terminals)
					}
				} else if state.Status != StatusFailed || state.Error == "" || state.ErrorCount != 1 {
					t.Fatalf("incomplete stream reported success: %+v", state)
				}
				if got := healthOf(t, group.ID).Members[group.ActiveItemID].FailureStreak; got != wantFailures {
					t.Fatalf("health failure count=%d, want %d", got, wantFailures)
				}
			})
		}
	}
}

type compatRoundTripper func(*http.Request) (*http.Response, error)

func (f compatRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type compatChunkBody struct {
	io.ReadCloser
	data        []byte
	togetherEOF bool
	closed      *atomic.Int32
}

func (r *compatChunkBody) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data[:min(len(r.data), 7)])
	r.data = r.data[n:]
	if len(r.data) == 0 && r.togetherEOF {
		return n, io.EOF
	}
	return n, nil
}

func (r *compatChunkBody) Close() error {
	r.closed.Add(1)
	return r.ReadCloser.Close()
}

func TestUpstreamCompatEOF3(t *testing.T) {
	// Tests in this package are serial. Restore the shared client's transport
	// only after Forward returns; the upstream still receives a real HTTP call.
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse} {
		for _, together := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/data-and-eof=%t", format, together), func(t *testing.T) {
				body := strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n") + compatSSE(compatChatFinish) + "data: [DONE]"
				var calls, closed atomic.Int32
				engine, group := compatRelay(t, model.ProtocolOpenAIChatCompletion, format, func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, body)
				})
				client, err := rhttp.Direct()
				if err != nil {
					t.Fatal(err)
				}
				original := client.Transport
				client.Transport = compatRoundTripper(func(r *http.Request) (*http.Response, error) {
					response, err := original.RoundTrip(r)
					if err != nil {
						return nil, err
					}
					data, err := io.ReadAll(response.Body)
					if err != nil {
						response.Body.Close()
						return nil, err
					}
					response.Body = &compatChunkBody{ReadCloser: response.Body, data: data, togetherEOF: together, closed: &closed}
					return response, nil
				})
				t.Cleanup(func() { client.Transport = original })
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, format, true, nil))
				state := compatState(t, group)
				if state.Status != StatusSuccess || !strings.Contains(recorder.Body.String(), "pong") || calls.Load() != 1 || closed.Load() != 1 {
					t.Fatalf("chunked EOF failed: state=%+v calls=%d closes=%d body=%s", state, calls.Load(), closed.Load(), recorder.Body)
				}
			})
		}
	}
}

func TestUpstreamCompatEOF3LimitAndCancellation(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatOpenAIResponse} {
		t.Run(format.String()+"/event-limit", func(t *testing.T) {
			engine, group := compatRelay(t, model.ProtocolOpenAIChatCompletion, format, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"padding":"`+strings.Repeat("x", 32*1024*1024)+`"}`+"\n\n")
			})
			recorder := httptest.NewRecorder()
			serveWithTimeout(t, engine, recorder, compatRequest(t, group, format, true, nil))
			state := compatState(t, group)
			if state.Status != StatusFailed || !strings.Contains(state.Error, httpclient.ErrStreamEventTooLarge.Error()) || recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("oversized event was accepted: %+v", state)
			}
		})
		t.Run(format.String()+"/cancel-read", func(t *testing.T) {
			arrived, closed := make(chan struct{}), make(chan struct{})
			engine, group := compatRelay(t, model.ProtocolOpenAIChatCompletion, format, func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, strings.TrimSuffix(chatCompletionStreamBody, "data: [DONE]\n\n"))
				w.(http.Flusher).Flush()
				close(arrived)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				engine.ServeHTTP(httptest.NewRecorder(), compatRequest(t, group, format, true, nil).WithContext(ctx))
			}()
			select {
			case <-arrived:
			case <-ctx.Done():
				t.Fatal("upstream did not receive request")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("canceled Relay remained blocked")
			}
			select {
			case <-closed:
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not release upstream connection")
			}
			if state := compatState(t, group); state.Status != StatusCanceled || state.ErrorCount != 0 {
				t.Fatalf("cancellation was counted as upstream failure: %+v", state)
			}
		})
	}
}

type compatTool struct {
	ID, Type, Name, Arguments string
}

// Ordinary Chat clients concatenate every delta string field by tool index.
// Using that rule catches repeated IDs/names as well as repeated arguments.
func compatChatTools(t *testing.T, body string) map[int]compatTool {
	t.Helper()
	tools := make(map[int]compatTool)
	for _, event := range compatEvents(t, body) {
		if string(event.Data) == "[DONE]" {
			continue
		}
		var chunk llm.Response
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			t.Fatal(err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta == nil {
				continue
			}
			for _, delta := range choice.Delta.ToolCalls {
				tool := tools[delta.Index]
				tool.ID += delta.ID
				tool.Type += delta.Type
				tool.Name += delta.Function.Name
				tool.Arguments += delta.Function.Arguments
				tools[delta.Index] = tool
			}
		}
	}
	return tools
}

func compatToolList(t *testing.T, format llm.APIFormat, body string) []compatTool {
	t.Helper()
	var result []compatTool
	parsed := compatJSON(t, []byte(body))
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		var response llm.Response
		if err := json.Unmarshal([]byte(body), &response); err != nil {
			t.Fatal(err)
		}
		for _, choice := range response.Choices {
			if choice.Message == nil {
				continue
			}
			for _, tool := range choice.Message.ToolCalls {
				result = append(result, compatTool{tool.ID, tool.Type, tool.Function.Name, tool.Function.Arguments})
			}
		}
	case llm.APIFormatOpenAIResponse:
		items, _ := parsed["output"].([]any)
		for _, value := range items {
			item := value.(map[string]any)
			if item["type"] == "function_call" {
				result = append(result, compatTool{item["call_id"].(string), "function", item["name"].(string), item["arguments"].(string)})
			}
		}
	case llm.APIFormatAnthropicMessage:
		items, _ := parsed["content"].([]any)
		for _, value := range items {
			item := value.(map[string]any)
			if item["type"] == "tool_use" {
				arguments, err := json.Marshal(item["input"])
				if err != nil {
					t.Fatal(err)
				}
				result = append(result, compatTool{item["id"].(string), "function", item["name"].(string), string(arguments)})
			}
		}
	}
	return result
}

func assertCompatTools(t *testing.T, got []compatTool, want []compatTool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tool count=%d, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Type != want[i].Type || got[i].Name != want[i].Name ||
			!reflect.DeepEqual(compatJSON(t, []byte(got[i].Arguments)), compatJSON(t, []byte(want[i].Arguments))) {
			t.Fatalf("tool[%d]=%+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestUpstreamCompatTOOL1AndTOOL2(t *testing.T) {
	for _, mode := range []string{"done-only", "remaining-in-done", "interleaved-complete-deltas"} {
		t.Run(mode, func(t *testing.T) {
			events := []string{compatResponseCreated,
				`{"type":"response.output_item.added","output_index":0,"item":{"id":"fc-a","type":"function_call","call_id":"call-a","name":"lookup","namespace":"weather","arguments":""}}`,
			}
			want := []compatTool{{"call-a", "function", "weather__lookup", `{"city":"Paris"}`}}
			switch mode {
			case "remaining-in-done":
				events = append(events, `{"type":"response.function_call_arguments.delta","item_id":"fc-a","output_index":0,"delta":"{\"city\":"}`)
			case "interleaved-complete-deltas":
				events = append(events,
					`{"type":"response.output_item.added","output_index":1,"item":{"id":"fc-b","type":"function_call","call_id":"call-b","name":"forecast","namespace":"weather","arguments":""}}`,
					`{"type":"response.function_call_arguments.delta","item_id":"fc-a","output_index":0,"delta":"{\"city\":"}`,
					`{"type":"response.function_call_arguments.delta","item_id":"fc-b","output_index":1,"delta":"{\"days\":"}`,
					`{"type":"response.function_call_arguments.delta","item_id":"fc-a","output_index":0,"delta":"\"Paris\"}"}`,
					`{"type":"response.function_call_arguments.delta","item_id":"fc-b","output_index":1,"delta":"2}"}`,
				)
				want = append(want, compatTool{"call-b", "function", "weather__forecast", `{"days":2}`})
			}
			events = append(events, `{"type":"response.function_call_arguments.done","item_id":"fc-a","output_index":0,"name":"lookup","namespace":"weather","arguments":"{\"city\":\"Paris\"}"}`)
			if mode == "interleaved-complete-deltas" {
				events = append(events, `{"type":"response.function_call_arguments.done","item_id":"fc-b","output_index":1,"name":"forecast","namespace":"weather","arguments":"{\"days\":2}"}`)
			}
			events = append(events, compatResponseCompleted)
			var calls atomic.Int32
			engine, group := compatRelay(t, model.ProtocolOpenAIResponse, llm.APIFormatOpenAIChatCompletion, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/responses" {
					t.Errorf("wrong endpoint: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatSSE(events...))
			})
			recorder := httptest.NewRecorder()
			serveWithTimeout(t, engine, recorder, compatRequest(t, group, llm.APIFormatOpenAIChatCompletion, true, nil))
			state := compatState(t, group)
			if state.Status != StatusSuccess || calls.Load() != 1 || state.Usage.PromptTokens != 3 || state.Usage.CompletionTokens != 5 {
				t.Fatalf("tool stream failed or usage duplicated: %+v, calls=%d", state, calls.Load())
			}
			joined := compatChatTools(t, recorder.Body.String())
			var got []compatTool
			for i := 0; i < len(joined); i++ {
				got = append(got, joined[i])
			}
			assertCompatTools(t, got, want)
			assertCompatTools(t, compatToolList(t, llm.APIFormatOpenAIChatCompletion, state.responseBody), want)
		})
	}
}

func TestUpstreamCompatAnthropicChatToolIdentity(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiple=%t", multiple), func(t *testing.T) {
			events := []string{
				`{"type":"message_start","message":{"id":"msg-tool-identity","type":"message","role":"assistant","model":"upstream-model","content":[],"usage":{"input_tokens":3,"output_tokens":0}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call-a","name":"lookup","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`,
				`{"type":"content_block_stop","index":0}`,
			}
			want := []compatTool{{"call-a", "function", "lookup", `{"city":"Paris"}`}}
			if multiple {
				events = append(events,
					`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-b","name":"forecast","input":{}}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"days\":"}}`,
					`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"2}"}}`,
					`{"type":"content_block_stop","index":1}`,
				)
				want = append(want, compatTool{"call-b", "function", "forecast", `{"days":2}`})
			}
			events = append(events,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`,
				`{"type":"message_stop"}`,
			)
			var calls atomic.Int32
			engine, group := compatRelay(t, model.ProtocolAnthropicMessage, llm.APIFormatOpenAIChatCompletion, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1/messages" {
					t.Errorf("wrong endpoint: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, compatSSE(events...))
			})
			recorder := httptest.NewRecorder()
			serveWithTimeout(t, engine, recorder, compatRequest(t, group, llm.APIFormatOpenAIChatCompletion, true, nil))
			state := compatState(t, group)
			if state.Status != StatusSuccess || calls.Load() != 1 || state.Usage.PromptTokens != 3 || state.Usage.CompletionTokens != 5 {
				t.Fatalf("tool stream failed or usage changed: %+v, calls=%d", state, calls.Load())
			}
			joined := compatChatTools(t, recorder.Body.String())
			got := make([]compatTool, len(joined))
			for index := range got {
				got[index] = joined[index]
			}
			assertCompatTools(t, got, want)
			assertCompatTools(t, compatToolList(t, llm.APIFormatOpenAIChatCompletion, state.responseBody), want)
		})
	}
}

func TestUpstreamCompatINDEX1AndINDEX2(t *testing.T) {
	for _, format := range []llm.APIFormat{llm.APIFormatAnthropicMessage, llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIChatCompletion} {
		for _, mixed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/mixed-index=%t", format, mixed), func(t *testing.T) {
				firstIndex := ""
				if mixed {
					firstIndex = `"index":4,`
				}
				toolDeltas := []string{
					`{` + firstIndex + `"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{\"city\":"}}`,
					`{"function":{"arguments":"\"Paris\"}"}}`,
					`{"id":"call-b","type":"function","function":{"name":"forecast","arguments":"{\"days\":"}}`,
					`{"function":{"arguments":"2}"}}`,
				}
				if mixed {
					// Explicitly revisit index 4 after the indexless second call.
					toolDeltas = append(toolDeltas, `{"index":4,"function":{"arguments":" "}}`)
				}
				var events []string
				for _, tool := range toolDeltas {
					events = append(events, `{"id":"chat-compat","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{"tool_calls":[`+tool+`]},"finish_reason":null}]}`)
				}
				events = append(events,
					`{"id":"chat-compat","object":"chat.completion.chunk","model":"upstream-model","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`, "[DONE]")
				var calls atomic.Int32
				engine, group := compatRelay(t, model.ProtocolOpenAIChatCompletion, format, func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, compatSSE(events...))
				})
				recorder := httptest.NewRecorder()
				serveWithTimeout(t, engine, recorder, compatRequest(t, group, format, true, nil))
				state := compatState(t, group)
				if state.Status != StatusSuccess || calls.Load() != 1 || state.Usage.PromptTokens != 3 || state.Usage.CompletionTokens != 5 {
					t.Fatalf("indexless calls failed: state=%+v calls=%d body=%s", state, calls.Load(), recorder.Body)
				}
				want := []compatTool{{"call-a", "function", "lookup", `{"city":"Paris"}`}, {"call-b", "function", "forecast", `{"days":2}`}}
				assertCompatTools(t, compatToolList(t, format, state.responseBody), want)
				if format != llm.APIFormatOpenAIChatCompletion {
					// Reconstruct from the actual client's terminal response or
					// Anthropic tool blocks, separately from the log aggregator.
					assertCompatTools(t, compatWireTools(t, format, recorder.Body.String()), want)
				}
			})
		}
	}
}

func compatWireTools(t *testing.T, format llm.APIFormat, body string) []compatTool {
	t.Helper()
	var tools []compatTool
	byIndex := make(map[int]int)
	for _, event := range compatEvents(t, body) {
		value := compatJSON(t, event.Data)
		if format == llm.APIFormatOpenAIResponse {
			if value["type"] == "response.completed" {
				response, err := json.Marshal(value["response"])
				if err != nil {
					t.Fatal(err)
				}
				return compatToolList(t, format, string(response))
			}
			continue
		}
		switch value["type"] {
		case "content_block_start":
			block := value["content_block"].(map[string]any)
			if block["type"] == "tool_use" {
				byIndex[int(value["index"].(float64))] = len(tools)
				tools = append(tools, compatTool{ID: block["id"].(string), Type: "function", Name: block["name"].(string)})
			}
		case "content_block_delta":
			delta := value["delta"].(map[string]any)
			if delta["type"] == "input_json_delta" {
				index, ok := byIndex[int(value["index"].(float64))]
				if !ok {
					t.Fatal("tool argument delta has no matching start")
				}
				tools[index].Arguments += delta["partial_json"].(string)
			}
		}
	}
	return tools
}

const compatResponseBody = `{"id":"resp-compat","object":"response","created_at":1,"model":"upstream-model","status":"completed","output":[{"id":"msg-compat","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"pong","annotations":[]}]}],"usage":{"input_tokens":3,"output_tokens":5,"total_tokens":8}}`

func TestUpstreamCompatCHOICE1ToCHOICE3(t *testing.T) {
	chatTools := []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}}}
	responseTools := []any{map[string]any{"type": "function", "name": "lookup", "parameters": map[string]any{"type": "object"}}}
	chatChoice := map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}}
	responseChoice := map[string]any{"type": "function", "name": "lookup"}
	for _, tc := range []struct {
		name       string
		format     llm.APIFormat
		protocol   model.Protocol
		extra      map[string]any
		wantTools  bool
		wantChoice any
		override   string
	}{
		{"CHOICE1/no-tools-to-chat", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion, map[string]any{"tool_choice": "required"}, false, nil, ""},
		{"CHOICE1/no-tools-to-responses", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse, map[string]any{"tool_choice": "required"}, false, nil, ""},
		{"CHOICE1/filtered-tools-to-chat", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion, map[string]any{"tools": []any{map[string]any{"type": "image_generation"}}, "tool_choice": "required"}, false, nil, ""},
		{"CHOICE2/named-to-chat", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIChatCompletion, map[string]any{"tools": responseTools, "tool_choice": responseChoice}, true, chatChoice, ""},
		{"CHOICE2/named-to-responses", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse, map[string]any{"tools": chatTools, "tool_choice": chatChoice}, true, responseChoice, ""},
		{"CHOICE2/auto-with-tools", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse, map[string]any{"tools": chatTools, "tool_choice": "auto"}, true, "auto", ""},
		{"CHOICE3/passthrough-chat", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIChatCompletion, map[string]any{"tools": []any{}, "tool_choice": "required"}, false, "required", ""},
		{"CHOICE3/passthrough-responses", llm.APIFormatOpenAIResponse, model.ProtocolOpenAIResponse, map[string]any{"tool_choice": "required"}, false, "required", ""},
		{"CHOICE3/channel-override", llm.APIFormatOpenAIChatCompletion, model.ProtocolOpenAIResponse, nil, false, "required", `{"tools":[],"tool_choice":"required"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			received := make(chan map[string]any, 2)
			engine, group := compatRelay(t, tc.protocol, tc.format, func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				received <- request
				w.Header().Set("Content-Type", "application/json")
				if tc.protocol == model.ProtocolOpenAIResponse {
					_, _ = io.WriteString(w, compatResponseBody)
				} else {
					_, _ = io.WriteString(w, chatCompletionBody)
				}
			})
			if tc.override != "" {
				setChannelParamOverride(t, group.Items[0].ChannelGrantID, tc.override)
			}
			recorder := httptest.NewRecorder()
			serveWithTimeout(t, engine, recorder, compatRequest(t, group, tc.format, false, tc.extra))
			state := compatState(t, group)
			if state.Status != StatusSuccess || recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "pong") || len(received) != 1 {
				t.Fatalf("choice request failed: state=%+v body=%s calls=%d", state, recorder.Body, len(received))
			}
			request := <-received
			tools, _ := request["tools"].([]any)
			if (len(tools) > 0) != tc.wantTools || !reflect.DeepEqual(request["tool_choice"], tc.wantChoice) || request["model"] != "upstream-model" {
				t.Fatalf("wrong outgoing tools/choice: %v; want tools=%t choice=%v", request, tc.wantTools, tc.wantChoice)
			}
			if tc.wantChoice == nil {
				if _, exists := request["tool_choice"]; exists {
					t.Fatal("empty tools must omit tool_choice, not send null")
				}
			}
			if tc.wantTools {
				tool := tools[0].(map[string]any)
				if tc.protocol == model.ProtocolOpenAIChatCompletion {
					tool = tool["function"].(map[string]any)
				}
				if tool["name"] != "lookup" || tool["parameters"].(map[string]any)["type"] != "object" {
					t.Fatalf("tool definition lost: %v", tools)
				}
			}
		})
	}
}

func TestUpstreamCompatCHOICE2RawToolsRestored(t *testing.T) {
	// Forward intentionally bypasses conversion for the same protocol. Exercise
	// Relay's conversion sender explicitly to test the serializer's raw-tool
	// restoration without changing that production routing contract.
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, compatResponseBody)
	}))
	defer server.Close()
	tool := map[string]any{"type": "file_search", "vector_store_ids": []any{"vs-synthetic"}, "max_num_results": float64(2)}
	body, err := json.Marshal(map[string]any{"model": "upstream-model", "input": "ping", "tools": []any{tool}, "tool_choice": "required"})
	if err != nil {
		t.Fatal(err)
	}
	raw := &httpclient.Request{Method: http.MethodPost, URL: server.URL + "/v1/responses", Headers: http.Header{"Content-Type": {"application/json"}}, Body: body}
	unified, err := responses.NewInboundTransformer().TransformRequest(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(unified.Tools) != 0 || unified.ProviderExtensions == nil || unified.ProviderExtensions.OpenAIResponses == nil || len(unified.ProviderExtensions.OpenAIResponses.Request.RawTools) != 1 {
		t.Fatal("fixture does not exercise raw-only tool restoration")
	}
	outbound, err := responses.NewOutboundTransformer(server.URL, "synthetic-credential")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result, sent, err := sendConverted(ctx, llm.APIFormatOpenAIResponse, raw, model.Channel{}, outbound, false)
	if err != nil || !sent || result == nil || !strings.Contains(string(result.body), "pong") || len(received) != 1 {
		t.Fatalf("raw-tools conversion failed: sent=%t err=%v result=%+v", sent, err, result)
	}
	request := <-received
	if !reflect.DeepEqual(request["tools"], []any{tool}) || request["tool_choice"] != "required" {
		t.Fatalf("raw tools or valid choice lost: %v", request)
	}
}
