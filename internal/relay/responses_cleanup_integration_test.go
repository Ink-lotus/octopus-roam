package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
)

// Reuse the package's real SQLite/op fixtures. Every channel points to a local
// synthetic upstream; no configured application or external account is used.
func responsesHistoryChannel(t *testing.T, upstream string, enabled bool, override string) int {
	t.Helper()
	grant := newTestChannel(t, testName(t, "history-channel"), upstream, "upstream-model")
	detail, err := op.ChannelDetailGet(channelIDOfGrant(t, grant))
	if err != nil {
		t.Fatal(err)
	}
	detail.Grants[0].Protocols = model.ProtocolOpenAIResponse
	detail.StripResponsesHistory = enabled
	detail.ParamOverride = override
	saved, err := op.ChannelUpdateWithOptions(&detail, model.ChannelSaveOptions{StripResponsesHistoryPresent: true}, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if saved.StripResponsesHistory != enabled {
		t.Fatal("fixture did not persist the requested cleanup setting")
	}
	return grant
}

func responsesHistoryRelayServer(t *testing.T) *httptest.Server {
	t.Helper()
	engine := gin.New()
	engine.POST("/v1/responses", Forward(llm.APIFormatOpenAIResponse))
	engine.POST("/v1/chat/completions", Forward(llm.APIFormatOpenAIChatCompletion))
	engine.POST("/v1/messages", Forward(llm.APIFormatAnthropicMessage))
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	server.Client().Timeout = 15 * time.Second
	return server
}

func responsesHistoryMarshal(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Reject malformed/lost fields before sending success, rather than hiding a
// bad request behind an unconditional 200 from the fixture.
func responsesHistoryCheckUpstream(r *http.Request, input json.RawMessage, streaming bool, previous string) error {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
		return fmt.Errorf("unexpected upstream endpoint: %s %s", r.Method, r.URL.Path)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	if string(body["model"]) != `"upstream-model"` || string(body["stream"]) != fmt.Sprint(streaming) {
		return fmt.Errorf("upstream model or stream flag changed")
	}
	if previous == "" {
		if _, exists := body["previous_response_id"]; exists {
			return fmt.Errorf("remote response reference was retained")
		}
	} else if string(body["previous_response_id"]) != fmt.Sprintf("%q", previous) {
		return fmt.Errorf("disabled cleanup lost the original response reference")
	}
	if _, exists := body["messages"]; exists {
		return fmt.Errorf("Responses upstream received unconverted messages")
	}
	if err := responsesHistoryJSONEqual(body["input"], input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if err := responsesHistoryJSONEqual(body["include"], []byte(`["reasoning.encrypted_content","message.output_text.logprobs"]`)); err != nil {
		return fmt.Errorf("include: %w", err)
	}
	return nil
}

func responsesHistoryWriteResponse(w http.ResponseWriter, streaming bool, round int, output []json.RawMessage) {
	response := map[string]any{
		"id": fmt.Sprintf("resp_history_%d", round), "object": "response", "created_at": 1,
		"model": "upstream-model", "status": "completed", "output": output,
		"usage": map[string]int{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
	}
	if !streaming {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	sequence := 0
	writeEvent := func(event map[string]any) {
		event["sequence_number"] = sequence
		sequence++
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	writeEvent(map[string]any{"type": "response.created", "response": map[string]any{
		"id": response["id"], "object": "response", "created_at": 1,
		"model": "upstream-model", "status": "in_progress", "output": []any{},
	}})
	for i, item := range output {
		var message struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		_ = json.Unmarshal(item, &message)
		var added map[string]json.RawMessage
		_ = json.Unmarshal(item, &added)
		if _, hasStatus := added["status"]; hasStatus {
			added["status"] = json.RawMessage(`"in_progress"`)
		}
		switch message.Type {
		case "message":
			added["content"] = json.RawMessage(`[]`)
		case "function_call":
			added["arguments"] = json.RawMessage(`""`)
		}
		writeEvent(map[string]any{"type": "response.output_item.added", "output_index": i, "item": added})
		if message.Type == "message" {
			for j, part := range message.Content {
				if part.Type == "output_text" {
					writeEvent(map[string]any{"type": "response.content_part.added", "item_id": message.ID, "output_index": i, "content_index": j, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
					writeEvent(map[string]any{"type": "response.output_text.delta", "item_id": message.ID, "output_index": i, "content_index": j, "delta": part.Text})
					writeEvent(map[string]any{"type": "response.output_text.done", "item_id": message.ID, "output_index": i, "content_index": j, "text": part.Text})
					writeEvent(map[string]any{"type": "response.content_part.done", "item_id": message.ID, "output_index": i, "content_index": j, "part": map[string]any{"type": "output_text", "text": part.Text, "annotations": []any{}}})
				}
			}
		} else if message.Type == "function_call" {
			writeEvent(map[string]any{"type": "response.function_call_arguments.delta", "item_id": message.ID, "output_index": i, "delta": message.Arguments})
			writeEvent(map[string]any{"type": "response.function_call_arguments.done", "item_id": message.ID, "output_index": i, "arguments": message.Arguments})
		}
		writeEvent(map[string]any{"type": "response.output_item.done", "output_index": i, "item": item})
	}
	writeEvent(map[string]any{"type": "response.completed", "response": response})
}

func responsesHistoryPost(t *testing.T, server *httptest.Server, path string, body []byte, streaming bool) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Relay status %d: %.1500s", response.StatusCode, raw)
	}
	wantType := "application/json"
	if streaming {
		wantType = "text/event-stream"
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), wantType) {
		t.Fatalf("Relay content type = %q, want %s", response.Header.Get("Content-Type"), wantType)
	}
	return raw
}

type responsesHistoryReplayStep struct {
	append []json.RawMessage
	input  []json.RawMessage
	output []json.RawMessage
}

func responsesHistoryReplaySteps(t *testing.T, scenario string) []responsesHistoryReplayStep {
	t.Helper()
	var history []json.RawMessage
	steps := make([]responsesHistoryReplayStep, 0, 4)
	for round := 0; round < 4; round++ {
		addition := []json.RawMessage{responsesHistoryMarshal(t, map[string]string{"role": "user", "content": fmt.Sprintf("continue %d", round)})}
		output := []json.RawMessage{json.RawMessage(strings.ReplaceAll(responsesHistoryOutputItem, "msg_1", fmt.Sprintf("msg_round_%d", round)))}
		switch scenario {
		case "agent":
			if round == 0 {
				output = append([]json.RawMessage{json.RawMessage(responsesHistoryAgentItem)}, output...)
			}
		case "tools":
			if round < 2 {
				suffix := fmt.Sprintf("_%d", round+1)
				output = []json.RawMessage{
					json.RawMessage(strings.ReplaceAll(responsesHistoryReasoningItem, "_1", suffix)),
					json.RawMessage(strings.ReplaceAll(responsesHistoryCallItem, "_1", suffix)),
				}
			}
			if round == 1 || round == 2 {
				addition = []json.RawMessage{json.RawMessage(strings.ReplaceAll(responsesHistoryToolOutputItem, "_1", fmt.Sprintf("_%d", round)))}
			}
		case "compaction":
			if round == 0 {
				addition = append([]json.RawMessage{json.RawMessage(responsesHistoryCompactionItem)}, addition...)
			}
		default:
			t.Fatalf("unknown replay scenario %q", scenario)
		}
		history = append(history, addition...)
		steps = append(steps, responsesHistoryReplayStep{append: addition, input: slices.Clone(history), output: output})
		history = append(history, output...)
	}
	return steps
}

func TestResponsesHistoryHTTPReplay(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, scenario := range []string{"agent", "tools", "compaction"} {
			t.Run(fmt.Sprintf("stream_%t/%s", streaming, scenario), func(t *testing.T) {
				steps := responsesHistoryReplaySteps(t, scenario)
				inputs := make([]json.RawMessage, len(steps))
				for i, step := range steps {
					inputs[i] = responsesHistoryMarshal(t, step.input)
				}
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					i := int(calls.Add(1)) - 1
					if i >= len(steps) {
						t.Error("unexpected retry or extra replay request")
						http.Error(w, "unexpected replay", http.StatusBadRequest)
						return
					}
					if err := responsesHistoryCheckUpstream(r, inputs[i], streaming, ""); err != nil {
						t.Errorf("upstream round %d: %v", i, err)
						http.Error(w, "history contract failed", http.StatusBadRequest)
						return
					}
					responsesHistoryWriteResponse(w, streaming, i, steps[i].output)
				}))
				t.Cleanup(upstream.Close)
				grant := responsesHistoryChannel(t, upstream.URL, true, "")
				config := testRelayConfig(1)
				config.ManualMaxAttempts = 1
				group := newTestGroup(t, model.GroupModeManual, config, grant)
				server := responsesHistoryRelayServer(t)
				var history []json.RawMessage
				for i, step := range steps {
					history = append(history, step.append...)
					input := append([]json.RawMessage{json.RawMessage(`{"type":"item_reference","id":"remote_item"}`)}, history...)
					body := responsesHistoryMarshal(t, map[string]any{
						"model": group, "stream": streaming, "input": input, "store": false,
						"previous_response_id": "remote_response",
						"include":              []string{"reasoning.encrypted_content", "message.output_text.logprobs"},
					})
					raw := responsesHistoryPost(t, server, "/v1/responses", body, streaming)
					if streaming {
						var completed json.RawMessage
						var doneItems []json.RawMessage
						for _, event := range compatEvents(t, string(raw)) {
							var value struct {
								Type     string          `json:"type"`
								Response json.RawMessage `json:"response"`
								Item     json.RawMessage `json:"item"`
							}
							if err := json.Unmarshal([]byte(event.Data), &value); err != nil {
								t.Fatal(err)
							}
							switch value.Type {
							case "response.completed":
								if completed != nil {
									t.Fatal("duplicate completion event")
								}
								completed = value.Response
							case "response.output_item.done":
								doneItems = append(doneItems, value.Item)
							}
						}
						if err := responsesHistoryJSONEqual(responsesHistoryMarshal(t, doneItems), responsesHistoryMarshal(t, step.output)); err != nil {
							t.Fatalf("completed SSE items changed: %v", err)
						}
						raw = completed
					}
					var response struct {
						ID     string            `json:"id"`
						Status string            `json:"status"`
						Output []json.RawMessage `json:"output"`
					}
					if err := json.Unmarshal(raw, &response); err != nil {
						t.Fatal(err)
					}
					if response.ID != fmt.Sprintf("resp_history_%d", i) || response.Status != "completed" {
						t.Fatalf("unexpected response identity/status: %s", raw)
					}
					if err := responsesHistoryJSONEqual(responsesHistoryMarshal(t, response.Output), responsesHistoryMarshal(t, step.output)); err != nil {
						t.Fatal(err)
					}
					// Replay what the client actually received, not a fresh copy of
					// the expected output. This detects losses across later rounds.
					history = append(history, response.Output...)
				}
				if calls.Load() != int32(len(steps)) {
					t.Fatalf("received %d upstream requests, want %d", calls.Load(), len(steps))
				}
			})
		}
	}
}

func TestResponsesHistoryHTTPConversionAfterOverrides(t *testing.T) {
	input := json.RawMessage(`[` + responsesHistoryReasoningItem + `,` + responsesHistoryAgentItem + `,` + responsesHistoryCompactionItem + `,` + responsesHistoryOutputItem + `]`)
	override := `{"previous_response_id":"override_remote","input":[{"type":"item_reference","id":"override_ref"},` + string(input[1:len(input)-1]) + `],"include":["reasoning.encrypted_content","message.output_text.logprobs"]}`
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream_%t", format, streaming), func(t *testing.T) {
				var calls atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if err := responsesHistoryCheckUpstream(r, input, streaming, ""); err != nil {
						t.Error(err)
						http.Error(w, "converted history contract failed", http.StatusBadRequest)
						return
					}
					responsesHistoryWriteResponse(w, streaming, 0, []json.RawMessage{json.RawMessage(responsesHistoryOutputItem)})
				}))
				t.Cleanup(upstream.Close)
				grant := responsesHistoryChannel(t, upstream.URL, true, override)
				config := testRelayConfig(1)
				config.ManualMaxAttempts = 1
				group := newTestGroup(t, model.GroupModeManual, config, grant)
				server := responsesHistoryRelayServer(t)
				body := responsesHistoryMarshal(t, map[string]any{
					"model": group, "stream": streaming, "max_tokens": 64,
					"messages": []map[string]string{{"role": "user", "content": "ping"}},
				})
				path := "/v1/chat/completions"
				if format == llm.APIFormatAnthropicMessage {
					path = "/v1/messages"
				}
				raw := responsesHistoryPost(t, server, path, body, streaming)
				if !strings.Contains(string(raw), "record found") || calls.Load() != 1 {
					t.Fatalf("converted response/retry mismatch: calls=%d body=%.1500s", calls.Load(), raw)
				}
				if streaming {
					terminal := "data: [DONE]"
					if format == llm.APIFormatAnthropicMessage {
						terminal = "message_stop"
					}
					if !strings.Contains(string(raw), terminal) {
						t.Fatalf("converted stream lacks %q", terminal)
					}
				}
			})
		}
	}
}

func TestResponsesHistoryHTTPFailoverPreservesOriginal(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", streaming), func(t *testing.T) {
			kept := json.RawMessage(`[` + responsesHistoryAgentItem + `,` + responsesHistoryCompactionItem + `,` + responsesHistoryReasoningItem + `]`)
			original := json.RawMessage(`[{"type":"item_reference","id":"remote_item"},` + string(kept[1:len(kept)-1]) + `]`)
			log := &upstreamCallLog{}
			makeUpstream := func(name string, input json.RawMessage, previous string, fail bool) *httptest.Server {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					log.add(name)
					if err := responsesHistoryCheckUpstream(r, input, streaming, previous); err != nil {
						t.Errorf("%s: %v", name, err)
						http.Error(w, "failover history contract failed", http.StatusBadRequest)
						return
					}
					if fail {
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(w, `{"error":{"type":"server_error","message":"synthetic first channel failure"}}`)
						return
					}
					responsesHistoryWriteResponse(w, streaming, 0, []json.RawMessage{json.RawMessage(responsesHistoryOutputItem)})
				}))
				t.Cleanup(server.Close)
				return server
			}
			first := makeUpstream("enabled", kept, "", true)
			second := makeUpstream("disabled", original, "remote_response", false)
			firstGrant := responsesHistoryChannel(t, first.URL, true, "")
			secondGrant := responsesHistoryChannel(t, second.URL, false, "")
			group := newTestGroup(t, model.GroupModeFailover, testRelayConfig(1), firstGrant, secondGrant)
			server := responsesHistoryRelayServer(t)
			body := responsesHistoryMarshal(t, map[string]any{
				"model": group, "stream": streaming, "input": original,
				"previous_response_id": "remote_response",
				"include":              []string{"reasoning.encrypted_content", "message.output_text.logprobs"},
			})
			responsesHistoryPost(t, server, "/v1/responses", body, streaming)
			if !slices.Equal(log.snapshot(), []string{"enabled", "disabled"}) {
				t.Fatalf("unexpected channel attempts: %v", log.snapshot())
			}
		})
	}
}
