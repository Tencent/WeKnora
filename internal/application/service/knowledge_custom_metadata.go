package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseCustomMetadata shares the existing property-update validation with manual
// creation. It validates decoded values without rewriting the original JSON.
func parseCustomMetadata(raw []byte) (map[string]interface{}, error) {
	var custom map[string]interface{}
	if err := json.Unmarshal(raw, &custom); err != nil {
		return nil, fmt.Errorf("custom_metadata must be a JSON object: %w", err)
	}
	if len(custom) > 20 {
		return nil, fmt.Errorf("custom_metadata supports at most 20 fields")
	}
	for key, value := range custom {
		if len(strings.TrimSpace(key)) == 0 || len(key) > 64 || len(fmt.Sprint(value)) > 1000 {
			return nil, fmt.Errorf("invalid custom_metadata field %q", key)
		}
		switch value.(type) {
		case string, float64, bool, nil:
		default:
			return nil, fmt.Errorf("custom_metadata field %q must be a string, number, boolean, or null", key)
		}
	}
	return custom, nil
}
