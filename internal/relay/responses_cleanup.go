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

// sanitizeResponsesRequestBody removes explicit remote history references,
// preserving the history supplied by the client, including encrypted content.
// It runs on the final outbound body after channel parameter overrides so an
// override cannot add the removed references back.
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
	if raw, ok := object["input"]; ok {
		cleaned, err := stripResponsesInput(raw)
		if err != nil {
			return nil, err
		}
		object["input"] = cleaned
	}
	return json.Marshal(object)
}

// stripResponsesInput only filters references in the first-level input array.
// All other items remain opaque RawMessages: IDs, ciphertext, tool data and
// unknown structures must survive without recursive edits or number coercion.
func stripResponsesInput(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return slices.Clone(raw), nil
	}

	var values []json.RawMessage
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("invalid OpenAI Responses input array: %w", err)
	}
	cleaned := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		if !responsesItemReference(value) {
			cleaned = append(cleaned, value)
		}
	}
	return json.Marshal(cleaned)
}

func responsesItemReference(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return false
	}
	var itemType string
	return json.Unmarshal(object["type"], &itemType) == nil && itemType == "item_reference"
}
