package model

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// UnmarshalJSON records whether optional channel fields were submitted so an
// update can preserve the stored value when an older caller omits a new field.
func (request *ChannelSaveRequest) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("request must be a JSON object")
		}
		return err
	}
	present := false
	if raw, ok := fields["cooldown_rules"]; ok {
		present = true
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%w: cooldown_rules must be an array", ErrInvalidCooldownRule)
		}
		if _, err := ValidateCooldownRulesJSON(raw); err != nil {
			return err
		}
	}
	stripResponsesHistoryPresent := false
	if _, ok := fields["strip_responses_history"]; ok {
		stripResponsesHistoryPresent = true
	}
	type plainChannelSaveRequest ChannelSaveRequest
	var decoded plainChannelSaveRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*request = ChannelSaveRequest(decoded)
	request.CooldownRulesPresent = present
	request.StripResponsesHistoryPresent = stripResponsesHistoryPresent
	return nil
}
