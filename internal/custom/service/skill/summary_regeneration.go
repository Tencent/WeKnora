package skill

import (
	"encoding/json"
	"strings"
)

const explicitSummaryRegenerationKey = "explicit_summary_regeneration"

func MarkExplicitSummaryRegeneration(inputPayload string) (string, error) {
	payload := make(map[string]json.RawMessage)
	if strings.TrimSpace(inputPayload) != "" {
		if err := json.Unmarshal([]byte(inputPayload), &payload); err != nil {
			return "", err
		}
	}
	payload[explicitSummaryRegenerationKey] = json.RawMessage("true")
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func IsExplicitSummaryRegeneration(inputPayload string) bool {
	var payload map[string]json.RawMessage
	if strings.TrimSpace(inputPayload) == "" || json.Unmarshal([]byte(inputPayload), &payload) != nil {
		return false
	}
	var enabled bool
	return json.Unmarshal(payload[explicitSummaryRegenerationKey], &enabled) == nil && enabled
}
