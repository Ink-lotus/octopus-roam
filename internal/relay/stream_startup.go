package relay

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/tidwall/gjson"
)

const (
	maxStreamStartupEvents = 1024
	maxStreamStartupBytes  = 8 << 20
)

var errStreamStartupLimit = errors.New("upstream stream startup buffer limit exceeded")

// readStreamStartup owns events until it returns a response. On failure it
// closes the source; on success the handler owns that same source, including
// its optional delivery/finalization capabilities. No adapter hides them.
func readStreamStartup(ctx context.Context, format llm.APIFormat, events streams.Stream[*httpclient.StreamEvent]) (*upstreamResponse, error) {
	var buffered []*httpclient.StreamEvent
	bufferedBytes := 0
	fail := func(err error) (*upstreamResponse, error) {
		events.Close()
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if !events.Next() {
			err := events.Err()
			if err == nil {
				if len(buffered) == 0 {
					err = errors.New("upstream stream ended before first event")
				} else {
					err = llm.ErrStreamIncomplete
				}
			}
			return fail(err)
		}
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			return fail(fmt.Errorf("%w: %s", err, event.Data))
		}
		if !last && isStreamStartupEvent(format, event) {
			size := len(event.Data) + len(event.Type) + len(event.LastEventID)
			if len(buffered) >= maxStreamStartupEvents || size > maxStreamStartupBytes-bufferedBytes {
				return fail(fmt.Errorf("%w (%d events, %d bytes)", errStreamStartupLimit, maxStreamStartupEvents, maxStreamStartupBytes))
			}
			bufferedBytes += size
			buffered = append(buffered, cloneStartupEvent(event))
			continue
		}
		// An output event is subject to the decoder's single-event limit, not
		// the startup-only budget. Own its bytes before advancing the source.
		buffered = append(buffered, cloneStartupEvent(event))
		first := buffered[0]
		buffered[0] = nil
		return &upstreamResponse{
			events: events, first: first, last: last && len(buffered) == 1,
			buffered: buffered[1:], sourceComplete: last,
		}, nil
	}
}

func cloneStartupEvent(event *httpclient.StreamEvent) *httpclient.StreamEvent {
	owned := *event
	owned.Data = slices.Clone(event.Data)
	owned.Type = strings.Clone(event.Type)
	owned.LastEventID = strings.Clone(event.LastEventID)
	owned.Headers = event.Headers.Clone()
	return &owned
}

// The classification follows Octopus fd8b3b8, as present in fixed 8aa58b0.
// Inspect errors/terminators first. Only known, empty startup information can
// be held: unrecognized output fields must retain their passthrough behavior.
func isStreamStartupEvent(format llm.APIFormat, event *httpclient.StreamEvent) bool {
	parsed := gjson.ParseBytes(event.Data)
	if !parsed.IsObject() {
		return false
	}
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		if event.Type != "" && event.Type != "message" && event.Type != "chat.completion.chunk" {
			return false
		}
		return isChatStartup(parsed)
	case llm.APIFormatOpenAIResponse:
		if event.Type != "" && event.Type != parsed.Get("type").String() {
			return false
		}
		return isResponsesStartup(parsed)
	case llm.APIFormatAnthropicMessage:
		return isAnthropicStartup(parsed)
	default:
		return false
	}
}

func emptyStartupValue(value gjson.Result) bool {
	return value.Type == gjson.Null || (value.Type == gjson.String && value.Str == "") ||
		(value.IsArray() && len(value.Array()) == 0)
}

// Known metadata is allowed to be nonempty. Everything else must be visibly
// empty, including fields unknown to the dependency's typed protocol structs.
func onlyStartupFields(object gjson.Result, metadata ...string) bool {
	if !object.IsObject() {
		return emptyStartupValue(object)
	}
	empty := true
	object.ForEach(func(key, value gjson.Result) bool {
		empty = slices.Contains(metadata, key.Str) || emptyStartupValue(value)
		return empty
	})
	return empty
}

func isChatStartup(parsed gjson.Result) bool {
	choices := parsed.Get("choices")
	if !choices.IsArray() || !onlyStartupFields(parsed,
		"id", "object", "created", "model", "choices", "usage", "system_fingerprint", "service_tier") {
		return false
	}
	for _, choice := range choices.Array() {
		if !choice.IsObject() || choice.Get("finish_reason").String() != "" || choice.Get("message").Type != gjson.Null ||
			!onlyStartupFields(choice, "index", "delta", "logprobs") ||
			!onlyStartupFields(choice.Get("logprobs")) || !onlyStartupFields(choice.Get("delta"), "role") {
			return false
		}
	}
	return true
}

func isResponsesStartup(parsed gjson.Result) bool {
	switch parsed.Get("type").String() {
	case "response.created", "response.in_progress", "response.queued":
		return onlyStartupFields(parsed, "type", "sequence_number", "response") &&
			onlyStartupFields(parsed.Get("response"),
				"id", "object", "created_at", "model", "status", "instructions", "metadata", "parallel_tool_calls",
				"temperature", "tool_choice", "tools", "top_p", "background", "conversation", "max_output_tokens",
				"max_tool_calls", "previous_response_id", "prompt", "prompt_cache_key", "prompt_cache_retention",
				"reasoning", "safety_identifier", "service_tier", "text", "top_logprobs", "truncation", "usage", "user", "store")
	case "response.output_item.added":
		item := parsed.Get("item")
		kind := item.Get("type").String()
		// Tool identity/name is output even when its arguments are still empty.
		return (kind == "message" || kind == "reasoning") &&
			onlyStartupFields(parsed, "type", "sequence_number", "output_index", "item") &&
			item.Get("reasoning_content").Type == gjson.Null &&
			onlyStartupFields(item, "id", "type", "role", "status")
	case "response.content_part.added", "response.reasoning_summary_part.added":
		part := parsed.Get("part")
		kind := part.Get("type").String()
		known := kind == "output_text" || kind == "reasoning_text" || kind == "refusal" ||
			(parsed.Get("type").String() == "response.reasoning_summary_part.added" && (kind == "summary_text" || kind == ""))
		return part.IsObject() && known && onlyStartupFields(part, "type") &&
			onlyStartupFields(parsed, "type", "sequence_number", "item_id", "output_index", "content_index", "summary_index", "part")
	case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta", "response.refusal.delta":
		return onlyStartupFields(parsed, "type", "sequence_number", "item_id", "output_index", "content_index", "summary_index")
	default:
		return false
	}
}

func isAnthropicStartup(parsed gjson.Result) bool {
	switch parsed.Get("type").String() {
	case "message_start":
		return onlyStartupFields(parsed, "type", "message") &&
			onlyStartupFields(parsed.Get("message"), "id", "type", "role", "model", "usage")
	case "ping", "content_block_stop":
		return onlyStartupFields(parsed, "type", "index")
	case "content_block_start":
		block := parsed.Get("content_block")
		kind := block.Get("type").String()
		return (kind == "text" || kind == "thinking") && onlyStartupFields(block, "type") &&
			onlyStartupFields(parsed, "type", "index", "content_block")
	case "content_block_delta":
		delta := parsed.Get("delta")
		switch delta.Get("type").String() {
		case "text_delta", "thinking_delta", "signature_delta", "input_json_delta":
			return onlyStartupFields(delta, "type") && onlyStartupFields(parsed, "type", "index", "delta")
		default:
			return false
		}
	case "message_delta":
		return onlyStartupFields(parsed, "type", "delta", "usage") && onlyStartupFields(parsed.Get("delta"))
	default:
		return false
	}
}
