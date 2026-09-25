package im

import (
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/plugin/configschema"
)

// ParseCredentials parses the JSONB credentials field into a map.
func ParseCredentials(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var creds map[string]any
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	return creds, nil
}

// GetString safely extracts a string value from a credentials map.
func GetString(creds map[string]any, key string) string {
	if v, ok := creds[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetBool reads a boolean from JSON credentials (bool, string "true"/"1", or non-zero number).
func GetBool(creds map[string]any, key string) bool {
	v, ok := creds[key]
	if !ok {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		s := strings.TrimSpace(strings.ToLower(x))
		return s == "true" || s == "1" || s == "yes"
	case float64:
		return x != 0
	case int:
		return x != 0
	default:
		return false
	}
}

// secretKeys returns the credential keys a platform's schema marks secret.
func secretKeys(platform string) map[string]bool {
	out := map[string]bool{}
	if s := LookupPlatformInfo(platform).ConfigSchema; s != nil {
		for key, prop := range s.Properties {
			if prop.Secret {
				out[key] = true
			}
		}
	}
	return out
}

// RedactCredentials returns a channel's credentials for the editor: plain
// fields as stored, set secrets replaced by configschema.RedactedPlaceholder.
func RedactCredentials(platform string, raw []byte) (map[string]any, error) {
	creds, err := ParseCredentials(raw)
	if err != nil {
		return nil, err
	}
	secrets := secretKeys(platform)
	for key, v := range creds {
		if s, ok := v.(string); ok && s != "" && secrets[key] {
			creds[key] = configschema.RedactedPlaceholder
		}
	}
	return creds, nil
}

// MergeCredentials applies credentials sent by the editor to the stored ones.
// Keys the update leaves out keep their stored value, and so does a secret
// sent empty or as the redaction placeholder, since the editor never saw it.
// A plain field sent empty is cleared. An empty update changes nothing, which
// keeps an edit that never loaded the credentials from wiping them.
func MergeCredentials(platform string, stored, incoming []byte) ([]byte, error) {
	update, err := ParseCredentials(incoming)
	if err != nil {
		return nil, err
	}
	if len(update) == 0 {
		return stored, nil
	}
	out, err := ParseCredentials(stored)
	if err != nil {
		// Unreadable stored credentials cannot be merged into; take the update.
		out = map[string]any{}
	}
	secrets := secretKeys(platform)
	for key, v := range update {
		if secrets[key] {
			if s, ok := v.(string); v == nil || (ok && (s == "" || s == configschema.RedactedPlaceholder)) {
				continue
			}
		}
		out[key] = v
	}
	return json.Marshal(out)
}
