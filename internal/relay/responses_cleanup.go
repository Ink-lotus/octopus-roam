package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

const responsesEncryptedReasoningInclude = "reasoning.encrypted_content"

// sanitizeResponsesRequestBody removes Responses conversation state that can
// be bound to a different upstream resource. It is deliberately applied to
// the final outbound body, after channel parameter overrides, so an override
// cannot add the removed state back.
func sanitizeResponsesRequestBody(channel model.Channel, format llm.APIFormat, request *httpclient.Request) error {
	if !channel.StripResponsesHistory || format != llm.APIFormatOpenAIResponse {
		return nil
	}
	if request == nil {
		return fmt.Errorf("cannot clean an empty upstream request")
	}

	body, err := stripResponsesHistoryBody(request.Body)
	if err != nil {
		return err
	}
	request.Body = body
	if len(request.JSONBody) > 0 {
		request.JSONBody = slices.Clone(body)
	}
	return nil
}

func stripResponsesHistoryBody(body []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, fmt.Errorf("invalid OpenAI Responses request body: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("invalid OpenAI Responses request body: expected an object")
	}

	delete(object, "previous_response_id")
	if raw, ok := object["include"]; ok {
		cleaned, err := stripResponsesInclude(raw)
		if err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(cleaned), []byte("[]")) {
			delete(object, "include")
		} else {
			object["include"] = cleaned
		}
	}
	if raw, ok := object["input"]; ok {
		cleaned, err := stripResponsesInput(raw)
		if err != nil {
			return nil, err
		}
		object["input"] = cleaned
	}
	return json.Marshal(object)
}

func stripResponsesInclude(raw json.RawMessage) (json.RawMessage, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		// Leave malformed or null include values for the upstream validator. The
		// compatibility layer only removes the exact known include entry.
		return slices.Clone(raw), nil
	}
	cleaned := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		var name string
		if err := json.Unmarshal(value, &name); err == nil && name == responsesEncryptedReasoningInclude {
			continue
		}
		cleaned = append(cleaned, slices.Clone(value))
	}
	return json.Marshal(cleaned)
}

// stripResponsesInput recursively removes item identity and encrypted
// reasoning fields from input objects. item_reference entries are removed
// only when they are array items, matching the upstream compatibility layer's
// boundary while preserving tool call IDs and call IDs in the surrounding
// request fields.
func stripResponsesInput(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return slices.Clone(raw), nil
	}

	switch trimmed[0] {
	case '{':
		var object map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &object); err != nil {
			return nil, fmt.Errorf("invalid OpenAI Responses input object: %w", err)
		}
		if object == nil {
			return slices.Clone(trimmed), nil
		}
		delete(object, "id")
		delete(object, "encrypted_content")
		for key, value := range object {
			cleaned, err := stripResponsesInput(value)
			if err != nil {
				return nil, err
			}
			object[key] = cleaned
		}
		return json.Marshal(object)

	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return nil, fmt.Errorf("invalid OpenAI Responses input array: %w", err)
		}
		cleaned := make([]json.RawMessage, 0, len(values))
		for _, value := range values {
			if responsesItemReference(value) {
				continue
			}
			next, err := stripResponsesInput(value)
			if err != nil {
				return nil, err
			}
			cleaned = append(cleaned, next)
		}
		return json.Marshal(cleaned)

	default:
		return slices.Clone(trimmed), nil
	}
}

func responsesItemReference(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return false
	}
	var itemType string
	return json.Unmarshal(object["type"], &itemType) == nil && itemType == "item_reference"
}
