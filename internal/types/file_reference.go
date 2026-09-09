package types

import (
	"encoding/json"
	"regexp"
	"strings"
)

// StorageReferencePattern is shared by response rewriting and persisted
// reference checks. Matching an entire token prevents path/handle prefix grants.
var StorageReferencePattern = regexp.MustCompile(
	`\b(?:resource://[0-9A-Za-z_-]+|(?:storage://[0-9A-Za-z_-]+/)?` +
		`(?:local|minio|s3|cos|tos|oss|obs|ks3)://[^\s)\]>"]+)`,
)

// ContainsStorageReference matches an exact token in text or nested JSON.
func ContainsStorageReference(text, reference string) bool {
	if reference == "" {
		return false
	}
	// Image metadata and tool outputs may contain nested JSON strings. Decode
	// before tokenizing so escaping neither hides a path nor grants its prefix.
	trimmed := strings.TrimSpace(text)
	if len(trimmed) > 0 && strings.ContainsRune("[{\"", rune(trimmed[0])) {
		var value interface{}
		if json.Unmarshal([]byte(trimmed), &value) == nil {
			return containsStorageReferenceValue(value, reference)
		}
	}
	for _, candidate := range StorageReferencePattern.FindAllString(text, -1) {
		if candidate == reference {
			return true
		}
	}
	return false
}

func containsStorageReferenceValue(value interface{}, reference string) bool {
	switch value := value.(type) {
	case string:
		return ContainsStorageReference(value, reference)
	case []interface{}:
		for _, item := range value {
			if containsStorageReferenceValue(item, reference) {
				return true
			}
		}
	case map[string]interface{}:
		for _, item := range value {
			if containsStorageReferenceValue(item, reference) {
				return true
			}
		}
	}
	return false
}
