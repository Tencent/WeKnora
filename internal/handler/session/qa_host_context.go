package session

import (
	"encoding/json"
	"sort"
	"strings"
)

// buildQueryWithHostContext adds embed metadata after suggestion attribution is validated.
func buildQueryWithHostContext(query string, hostContext map[string]any) (string, error) {
	keys := make([]string, 0, len(hostContext))
	for key := range hostContext {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		value := hostContext[key]
		if value == nil || value == "" {
			continue
		}
		text, ok := value.(string)
		if !ok {
			encoded, err := json.Marshal(value)
			if err != nil {
				return "", err
			}
			text = string(encoded)
		}
		lines = append(lines, key+": "+text)
	}
	if len(lines) == 0 {
		return query, nil
	}
	return "[Host context]\n" + strings.Join(lines, "\n") + "\n\n" + query, nil
}
