package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// Protocol-shaped synthetic history. The opaque strings only test transport
// preservation; they are not ciphertext that a real upstream can decrypt.
const (
	responsesHistoryAgentItem      = `{"type":"agent_message","id":"agent_msg_1","author":"worker","recipient":"main","agent":{"agent_name":"worker"},"content":[{"type":"input_text","text":"worker result"},{"type":"encrypted_content","encrypted_content":"synthetic-agent-ciphertext"}]}`
	responsesHistoryCompactionItem = `{"type":"compaction","id":"cmp_1","encrypted_content":"synthetic-compaction-ciphertext","agent":{"agent_name":"main"}}`
	responsesHistoryReasoningItem  = `{"type":"reasoning","id":"rs_1","encrypted_content":"synthetic-reasoning-ciphertext","summary":[{"type":"summary_text","text":"look up the record"}],"content":[{"type":"reasoning_text","text":"synthetic reasoning"}],"status":"completed"}`
	responsesHistoryOutputItem     = `{"type":"message","id":"msg_1","role":"assistant","status":"completed","phase":"final_answer","content":[{"type":"output_text","text":"record found","annotations":[]}]}`
	responsesHistoryCallItem       = `{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup_record","arguments":"{\"id\":9007199254740993}","status":"completed"}`
	responsesHistoryToolOutputItem = `{"type":"function_call_output","id":"fco_1","call_id":"call_1","output":"{\"id\":9007199254740993,\"encrypted_content\":\"business-data\",\"type\":\"item_reference\"}","status":"completed"}`
)

// UseNumber makes the oracle sensitive to precision loss in unknown fields.
func responsesHistoryJSONEqual(got, want []byte) error {
	decode := func(raw []byte) (any, error) {
		if !json.Valid(raw) {
			return nil, fmt.Errorf("invalid JSON: %.500s", raw)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	actual, err := decode(got)
	if err != nil {
		return err
	}
	expected, err := decode(want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("JSON changed: got %.1200s, want %.1200s", got, want)
	}
	return nil
}

func TestResponsesHistoryRequiredItemsPreserved(t *testing.T) {
	for _, tc := range []struct {
		name string
		item string
	}{
		{"agent_message", responsesHistoryAgentItem},
		{"compaction", responsesHistoryCompactionItem},
		{"reasoning", responsesHistoryReasoningItem},
		{"output_message", responsesHistoryOutputItem},
		{"function_call", responsesHistoryCallItem},
		{"function_call_output", responsesHistoryToolOutputItem},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Compaction is the only prior history in its case: removing it or its
			// ciphertext cannot be hidden by redundant plaintext history.
			input := `[` + tc.item + `,{"role":"user","content":"continue"}]`
			body := []byte(`{"model":"gpt-5","input":` + input + `}`)
			cleaned, err := stripResponsesHistoryBody(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := responsesHistoryJSONEqual(cleaned, body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponsesHistoryAgentMessagePosition(t *testing.T) {
	for _, prefix := range []int{0, 1, 61, 97} {
		t.Run(fmt.Sprintf("prefix_%d", prefix), func(t *testing.T) {
			input := make([]json.RawMessage, 0, prefix+1)
			for i := 0; i < prefix; i++ {
				input = append(input, json.RawMessage(fmt.Sprintf(`{"role":"user","content":"history %d"}`, i)))
			}
			input = append(input, json.RawMessage(responsesHistoryAgentItem))
			body, err := json.Marshal(map[string]any{"input": input})
			if err != nil {
				t.Fatal(err)
			}
			cleaned, err := stripResponsesHistoryBody(body)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Input []json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(cleaned, &result); err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, item := range result.Input {
				var header struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(item, &header); err != nil {
					t.Fatal(err)
				}
				if header.Type != "agent_message" {
					continue
				}
				var agent struct {
					Content []struct {
						Type             string `json:"type"`
						EncryptedContent string `json:"encrypted_content"`
					} `json:"content"`
				}
				if err := json.Unmarshal(item, &agent); err != nil {
					t.Fatal(err)
				}
				found++
				if len(agent.Content) != 2 || agent.Content[1].Type != "encrypted_content" || agent.Content[1].EncryptedContent != "synthetic-agent-ciphertext" {
					t.Fatalf("agent_message lost required content: %s", item)
				}
			}
			if found != 1 {
				t.Fatalf("found %d agent messages, want 1", found)
			}
			if err := responsesHistoryJSONEqual(cleaned, body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStripResponsesHistoryBody(t *testing.T) {
	body := []byte(`{
        "model":"gpt-5",
        "previous_response_id":"resp_old",
        "include":["reasoning.encrypted_content","other","reasoning.encrypted_content_extra"],
        "input":[
            {"type":"message","id":"msg_old","role":"user","content":[{"type":"input_text","id":"text_old","text":"hello"}]},
            {"type":"item_reference","id":"ref_old","item_id":"item_old"},
            {"type":"function_call","id":"call_old","call_id":"call_1","name":"lookup_record","arguments":"{}"},
            {"type":"function_call_output","id":"output_old","call_id":"call_1","output":"ok"}
        ],
        "metadata":{"id":"metadata_id"}
    }`)

	cleaned, err := stripResponsesHistoryBody(body)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["previous_response_id"]; ok {
		t.Fatal("previous_response_id was retained")
	}
	var include []string
	if err := json.Unmarshal(root["include"], &include); err != nil {
		t.Fatal(err)
	}
	if want := []string{"reasoning.encrypted_content", "other", "reasoning.encrypted_content_extra"}; !reflect.DeepEqual(include, want) {
		t.Fatalf("include = %#v, want %#v", include, want)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(root["input"], &input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("input length = %d, want 3", len(input))
	}
	if string(input[0]["id"]) != `"msg_old"` {
		t.Fatal("message id was modified")
	}
	if _, ok := input[0]["content"]; !ok {
		t.Fatal("message content was removed")
	}
	if string(input[1]["id"]) != `"call_old"` {
		t.Fatal("function call id was modified")
	}
	if string(input[1]["call_id"]) != `"call_1"` {
		t.Fatal("function call call_id was removed")
	}
	if string(input[2]["id"]) != `"output_old"` || string(input[2]["output"]) != `"ok"` {
		t.Fatal("function output was modified")
	}
	if string(input[2]["call_id"]) != `"call_1"` {
		t.Fatal("function output call_id was removed")
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(root["metadata"], &metadata); err != nil {
		t.Fatal(err)
	}
	if string(metadata["id"]) != `"metadata_id"` {
		t.Fatal("top-level metadata was modified")
	}
}

func TestResponsesHistoryIncludePreserved(t *testing.T) {
	for _, include := range []string{
		`["reasoning.encrypted_content"]`,
		`["reasoning.encrypted_content","other","reasoning.encrypted_content_extra","reasoning.encrypted_content"]`,
		`[]`, `null`, `"reasoning.encrypted_content"`, `{"id":9007199254740993}`, `[null,42]`,
	} {
		t.Run(include, func(t *testing.T) {
			body := []byte(`{"include":` + include + `}`)
			cleaned, err := stripResponsesHistoryBody(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := responsesHistoryJSONEqual(cleaned, body); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSanitizeResponsesRequestBodyOnlyTargetsResponsesWhenEnabled(t *testing.T) {
	rawBody := []byte(`{"model":"gpt-5","previous_response_id":"resp_old","input":[{"id":"item_old"}]}`)
	for _, tc := range []struct {
		name    string
		enabled bool
		format  llm.APIFormat
		changed bool
	}{
		{name: "disabled", enabled: false, format: llm.APIFormatOpenAIResponse},
		{name: "chat", enabled: true, format: llm.APIFormatOpenAIChatCompletion},
		{name: "anthropic", enabled: true, format: llm.APIFormatAnthropicMessage},
		{name: "responses", enabled: true, format: llm.APIFormatOpenAIResponse, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := &httpclient.Request{Body: append([]byte(nil), rawBody...), JSONBody: []byte("cached")}
			channel := model.Channel{ChannelConfig: model.ChannelConfig{StripResponsesHistory: tc.enabled}}
			if err := sanitizeResponsesRequestBody(channel, tc.format, request); err != nil {
				t.Fatal(err)
			}
			if tc.changed {
				if string(request.Body) == string(rawBody) || string(request.JSONBody) != string(request.Body) {
					t.Fatal("enabled Responses cleanup did not update both request bodies")
				}
			} else if string(request.Body) != string(rawBody) || string(request.JSONBody) != "cached" {
				t.Fatal("disabled/non-Responses request was modified")
			}
		})
	}
}

func TestResponsesHistoryPassthroughAfterOverrides(t *testing.T) {
	outbound, err := responses.NewOutboundTransformer("https://upstream.example.com", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	raw := &httpclient.Request{
		Method:  http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"gpt-5","previous_response_id":"resp_old","input":[{"id":"item_old"}]}`),
	}
	raw.JSONBody = bytes.Clone(raw.Body)
	original := bytes.Clone(raw.Body)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		StripResponsesHistory: true,
		ParamOverride:         `{"previous_response_id":"override_should_be_removed","include":["reasoning.encrypted_content"],"input":[{"type":"item_reference","id":"override_ref"},` + responsesHistoryAgentItem + `]}`,
	}}
	request, err := buildPassthroughRequest(llm.APIFormatOpenAIResponse, raw, channel, outbound, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"model":"gpt-5","include":["reasoning.encrypted_content"],"input":[` + responsesHistoryAgentItem + `]}`)
	if err := responsesHistoryJSONEqual(request.Body, want); err != nil {
		t.Fatal(err)
	}
	if len(request.JSONBody) > 0 && !bytes.Equal(request.JSONBody, request.Body) {
		t.Fatal("passthrough JSON cache differs from the outbound body")
	}
	if !bytes.Equal(raw.Body, original) || !bytes.Equal(raw.JSONBody, original) {
		t.Fatal("cleanup changed the original inbound request")
	}
	// A later attempt with cleanup disabled must still receive the full input.
	next, err := buildPassthroughRequest(llm.APIFormatOpenAIResponse, raw, model.Channel{}, outbound, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	if err := responsesHistoryJSONEqual(next.Body, original); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesHistoryConvertedAfterOverrides(t *testing.T) {
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		StripResponsesHistory: true,
		ParamOverride:         `{"previous_response_id":"override_should_be_removed","include":["reasoning.encrypted_content"],"input":[{"type":"item_reference","id":"override_ref"},` + responsesHistoryReasoningItem + `]}`,
	}}
	middleware := &conversionMiddleware{
		channel: channel,
		format:  llm.APIFormatOpenAIResponse,
		failure: newUpstreamFailureCapture(context.Background(), channel.ID, llm.APIFormatOpenAIResponse, "gpt-5"),
	}
	request := &httpclient.Request{Body: []byte(`{"model":"gpt-5","previous_response_id":"resp_old","input":[{"id":"item_old"}]}`)}
	request.JSONBody = bytes.Clone(request.Body)
	if _, err := middleware.OnOutboundRawRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"model":"gpt-5","include":["reasoning.encrypted_content"],"input":[` + responsesHistoryReasoningItem + `]}`)
	if err := responsesHistoryJSONEqual(request.Body, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request.JSONBody, request.Body) {
		t.Fatal("converted JSON cache differs from the outbound body")
	}
}

func TestResponsesHistoryOnlyRemovesRemoteReferences(t *testing.T) {
	const retained = `
		{"type":"future_item","id":9007199254740993,"encrypted_content":null,"payload":{"previous_response_id":"business","id":18446744073709551615,"items":[{"type":"item_reference","id":"nested"},[{"id":"deep","encrypted_content":"business"}]]}},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"{\"id\":9007199254740993,\"type\":\"item_reference\"}"},{"type":"future_content","id":"content_id","encrypted_content":"content_ciphertext"}]},
		[{"type":"item_reference","id":"nested_array"}],
		null, true, 9007199254740993, "", [], {}, {"id":"no_type"},
		{"type":null,"id":"null_type"},{"type":7,"id":"numeric_type"},
		{"type":["item_reference"],"id":"array_type"},{"type":{"name":"item_reference"},"id":"object_type"},
		{"type":"item_reference_extra","id":"similar_type"},{"type":"ITEM_REFERENCE","id":"different_case"}`
	const other = `"include":["reasoning.encrypted_content","other"],
		"id":9007199254740993,"encrypted_content":"top_ciphertext","previous_response_id_extra":"keep",
		"metadata":{"id":"metadata_id","previous_response_id":"data"},
		"tools":[{"type":"function","name":"lookup_record","parameters":{"type":"object","properties":{"id":{"type":"integer"}}}}],
		"text":{"format":{"type":"json_schema","name":"record","schema":{"type":"object","properties":{"encrypted_content":{"type":"string"}}}}},
		"prompt_cache_key":"keep_cache_key","instructions":"keep instructions"`
	body := []byte(`{"previous_response_id":"remote",` + other + `,"input":[{"type":"item_reference","id":"remote_item"},` + retained + `,{"type":"item_reference","id":"last_remote_item"}]}`)
	original := bytes.Clone(body)
	want := []byte(`{` + other + `,"input":[` + retained + `]}`)
	for pass := 1; pass <= 2; pass++ {
		cleaned, err := stripResponsesHistoryBody(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := responsesHistoryJSONEqual(cleaned, want); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if pass == 1 && !bytes.Equal(body, original) {
			t.Fatal("cleanup mutated the caller's bytes")
		}
		body = cleaned
	}
}

func TestResponsesHistoryInputShapes(t *testing.T) {
	for _, input := range []string{
		`"hello"`, `null`, `true`, `9007199254740993`,
		`{"type":"item_reference","id":"non_array","encrypted_content":"opaque","items":[{"type":"item_reference","id":"nested"}]}`,
		`[]`,
	} {
		t.Run(input, func(t *testing.T) {
			body := []byte(`{"previous_response_id":"remote","input":` + input + `}`)
			cleaned, err := stripResponsesHistoryBody(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := responsesHistoryJSONEqual(cleaned, []byte(`{"input":`+input+`}`)); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct{ body, want string }{
		{`{"previous_response_id":"remote"}`, `{}`},
		{`{"input":[{"type":"item_reference","id":"one"},{"type":"item_reference","id":"two"}]}`, `{"input":[]}`},
	} {
		cleaned, err := stripResponsesHistoryBody([]byte(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if err := responsesHistoryJSONEqual(cleaned, []byte(tc.want)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestResponsesHistoryFailureIsAtomic(t *testing.T) {
	channel := model.Channel{ChannelConfig: model.ChannelConfig{StripResponsesHistory: true}}
	for _, raw := range []string{"", "null", "[]", `"text"`, "42", "false", `{"previous_response_id":"remote","input":[}`, `{} {}`} {
		t.Run(raw, func(t *testing.T) {
			request := &httpclient.Request{Body: []byte(raw), JSONBody: []byte(`{"cached":true}`)}
			beforeBody, beforeCache := bytes.Clone(request.Body), bytes.Clone(request.JSONBody)
			if err := sanitizeResponsesRequestBody(channel, llm.APIFormatOpenAIResponse, request); err == nil {
				t.Fatal("invalid request body was accepted")
			}
			if !bytes.Equal(request.Body, beforeBody) || !bytes.Equal(request.JSONBody, beforeCache) {
				t.Fatal("failure partially changed the request or its JSON cache")
			}
		})
	}
	if err := sanitizeResponsesRequestBody(channel, llm.APIFormatOpenAIResponse, nil); err == nil {
		t.Fatal("nil enabled Responses request was accepted")
	}
	if err := sanitizeResponsesRequestBody(model.Channel{}, llm.APIFormatOpenAIResponse, nil); err != nil {
		t.Fatal("disabled cleanup inspected the request")
	}
	if err := sanitizeResponsesRequestBody(channel, llm.APIFormatAnthropicMessage, nil); err != nil {
		t.Fatal("non-Responses cleanup inspected the request")
	}
}

func TestResponsesHistoryCacheAndOriginalBytes(t *testing.T) {
	for _, cache := range []string{"absent", "empty", "separate", "aliases_body"} {
		t.Run(cache, func(t *testing.T) {
			original := []byte(`{"previous_response_id":"remote","input":[` + responsesHistoryCompactionItem + `]}`)
			before := bytes.Clone(original)
			request := &httpclient.Request{Body: original}
			switch cache {
			case "empty":
				request.JSONBody = []byte{}
			case "separate":
				request.JSONBody = []byte(`{"stale":true}`)
			case "aliases_body":
				request.JSONBody = original
			}
			channel := model.Channel{ChannelConfig: model.ChannelConfig{StripResponsesHistory: true}}
			if err := sanitizeResponsesRequestBody(channel, llm.APIFormatOpenAIResponse, request); err != nil {
				t.Fatal(err)
			}
			if err := responsesHistoryJSONEqual(request.Body, []byte(`{"input":[`+responsesHistoryCompactionItem+`]}`)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, before) {
				t.Fatal("cleanup changed bytes owned by the inbound request")
			}
			switch cache {
			case "absent":
				if request.JSONBody != nil {
					t.Fatal("cleanup created a previously absent JSON cache")
				}
			case "empty":
				if request.JSONBody == nil || len(request.JSONBody) != 0 {
					t.Fatal("cleanup changed an empty JSON cache")
				}
			default:
				if !bytes.Equal(request.JSONBody, request.Body) {
					t.Fatal("JSON cache does not match the outbound body")
				}
				request.Body[0] = ' '
				if request.JSONBody[0] != '{' {
					t.Fatal("JSON cache aliases the mutable outbound body")
				}
			}
		})
	}
}
