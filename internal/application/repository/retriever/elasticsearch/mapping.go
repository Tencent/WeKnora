package elasticsearch

import (
	"encoding/json"
	"fmt"
)

// FolderIDQueryField resolves and validates the exact-match query field for an
// existing folder_id mapping. Legacy dynamically mapped text fields remain
// queryable through their keyword multi-field.
func FolderIDQueryField(property any) (string, error) {
	const name = "folder_id"
	propertyJSON, err := json.Marshal(property)
	if err != nil {
		return "", fmt.Errorf("marshal mapping for %s: %w", name, err)
	}

	var mapping struct {
		Type   string                     `json:"type"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(propertyJSON, &mapping); err != nil {
		return "", fmt.Errorf("decode mapping for %s: %w", name, err)
	}

	switch mapping.Type {
	case "keyword":
		return name, nil
	case "text":
		keywordJSON, ok := mapping.Fields["keyword"]
		if !ok {
			return "", fmt.Errorf("text field %s has no keyword multi-field", name)
		}
		var keyword struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(keywordJSON, &keyword); err != nil {
			return "", fmt.Errorf("decode keyword multi-field for %s: %w", name, err)
		}
		if keyword.Type != "keyword" {
			return "", fmt.Errorf(
				"field %s.keyword has incompatible type %q; expected keyword",
				name,
				keyword.Type,
			)
		}
		return name + ".keyword", nil
	default:
		return "", fmt.Errorf(
			"field %s has incompatible type %q; expected keyword or text with a keyword multi-field",
			name,
			mapping.Type,
		)
	}
}
