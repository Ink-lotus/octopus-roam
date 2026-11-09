package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestStripResponsesHistoryBody(t *testing.T) {
	body := []byte(`{
        "model":"gpt-5",
        "previous_response_id":"resp_old",
        "include":["reasoning.encrypted_content","other","reasoning.encrypted_content_extra"],
        "input":[
            {"type":"message","id":"msg_old","content":[{"type":"input_text","id":"text_old","text":"hello"}]},
            {"type":"item_reference","id":"ref_old","item_id":"item_old"},
            {"type":"function_call","id":"call_old","call_id":"call_1","arguments":"{}"},
            {"type":"function_call_output","call_id":"call_1","encrypted_content":"old_encrypted","output":"ok"}
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
	if want := []string{"other", "reasoning.encrypted_content_extra"}; !reflect.DeepEqual(include, want) {
		t.Fatalf("include = %#v, want %#v", include, want)
	}
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(root["input"], &input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("input length = %d, want 3", len(input))
	}
	if _, ok := input[0]["id"]; ok {
		t.Fatal("message id was retained")
	}
	if _, ok := input[0]["content"]; !ok {
		t.Fatal("message content was removed")
	}
	if _, ok := input[1]["id"]; ok {
		t.Fatal("function call id was retained")
	}
	if string(input[1]["call_id"]) != `"call_1"` {
		t.Fatal("function call call_id was removed")
	}
	if _, ok := input[2]["encrypted_content"]; ok {
		t.Fatal("encrypted_content was retained")
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

func TestStripResponsesHistoryBodyRemovesEmptyInclude(t *testing.T) {
	cleaned, err := stripResponsesHistoryBody([]byte(`{"include":["reasoning.encrypted_content"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(cleaned, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["include"]; ok {
		t.Fatal("empty include was retained")
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

func TestBuildPassthroughRequestCleansResponsesBodyAfterChannelOverrides(t *testing.T) {
	outbound, err := responses.NewOutboundTransformer("https://upstream.example.com", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	raw := &httpclient.Request{
		Method:  http.MethodPost,
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"gpt-5","previous_response_id":"resp_old","input":[{"id":"item_old"}]}`),
	}
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		StripResponsesHistory: true,
		ParamOverride:         `{"previous_response_id":"override_should_be_removed"}`,
	}}
	request, err := buildPassthroughRequest(llm.APIFormatOpenAIResponse, raw, channel, outbound, "gpt-5")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["previous_response_id"]; ok {
		t.Fatal("passthrough request retained previous_response_id")
	}
}

func TestConvertedRequestCleansResponsesBodyAfterChannelOverrides(t *testing.T) {
	channel := model.Channel{ChannelConfig: model.ChannelConfig{
		StripResponsesHistory: true,
		ParamOverride:         `{"previous_response_id":"override_should_be_removed"}`,
	}}
	middleware := &conversionMiddleware{
		channel: channel,
		format:  llm.APIFormatOpenAIResponse,
		failure: newUpstreamFailureCapture(context.Background(), channel.ID, llm.APIFormatOpenAIResponse, "gpt-5"),
	}
	request := &httpclient.Request{Body: []byte(`{"model":"gpt-5","previous_response_id":"resp_old","input":[{"id":"item_old"}]}`)}
	if _, err := middleware.OnOutboundRawRequest(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["previous_response_id"]; ok {
		t.Fatal("converted request retained previous_response_id")
	}
}
