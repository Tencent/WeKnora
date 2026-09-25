package configschema

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/utils"
)

// RedactedPlaceholder replaces a set secret in responses. It equals
// types.RedactedSecretPlaceholder, which every other integration already
// sends, so the frontend treats both the same way.
const RedactedPlaceholder = "***"

// Seal returns a copy of value with every secret encrypted for storage. It
// uses the same AES-GCM format (enc:v1:) as the existing integration tables,
// so already-encrypted values pass through unchanged and values sealed here
// read back through utils.DecryptStoredSecret. Without SYSTEM_AES_KEY secrets
// stay in plaintext, as they do elsewhere.
func Seal(s *Schema, value map[string]any) (map[string]any, error) {
	key := utils.GetAESKey()
	return transformSecrets(s, value, func(path, secret string) (string, error) {
		sealed, err := utils.EncryptAESGCM(secret, key)
		if err != nil {
			return "", fmt.Errorf("encrypt %s: %w", path, err)
		}
		return sealed, nil
	})
}

// Open returns a copy of value with every secret decrypted. It fails when a
// secret cannot be decrypted (missing or rotated SYSTEM_AES_KEY), which is
// what a caller about to use the credential wants.
func Open(s *Schema, value map[string]any) (map[string]any, error) {
	return transformSecrets(s, value, func(path, secret string) (string, error) {
		plain, err := utils.DecryptStoredSecret(secret)
		if err != nil {
			return "", fmt.Errorf("decrypt %s: %w", path, err)
		}
		return plain, nil
	})
}

// OpenLenient decrypts like Open but blanks secrets that cannot be decrypted
// and reports their paths, so a listing still loads and the UI can ask for
// the credential again.
func OpenLenient(s *Schema, value map[string]any) (map[string]any, []string) {
	var failed []string
	out, _ := transformSecrets(s, value, func(path, secret string) (string, error) {
		plain, ok := utils.DecryptStoredSecretLenient(secret)
		if !ok {
			failed = append(failed, path)
		}
		return plain, nil
	})
	return out, failed
}

// Redact returns a copy of value with every set secret replaced by
// RedactedPlaceholder. Unset secrets stay empty so the UI can tell "not
// configured" from "configured".
func Redact(s *Schema, value map[string]any) map[string]any {
	out, _ := transformSecrets(s, value, func(_, _ string) (string, error) {
		return RedactedPlaceholder, nil
	})
	return out
}

// Merge applies an update to a stored configuration. Non-secret fields come
// from incoming as sent. A secret that incoming leaves out, empties or sends
// back as RedactedPlaceholder keeps its stored value, since the client never
// saw it; clearing a secret is an explicit operation elsewhere, as it is for
// the existing /credentials subresources.
func Merge(s *Schema, stored, incoming map[string]any) map[string]any {
	out := deepCopyMap(incoming)
	if out == nil {
		out = map[string]any{}
	}
	mergeSecrets(s, stored, out)
	return out
}

func mergeSecrets(s *Schema, stored, out map[string]any) {
	for key, prop := range s.Properties {
		switch {
		case prop.Secret:
			incoming, _ := out[key].(string)
			if incoming != "" && incoming != RedactedPlaceholder {
				continue
			}
			if old, ok := stored[key]; ok {
				out[key] = old
			} else {
				delete(out, key)
			}
		case prop.Type == TypeObject && prop.containsSecret():
			storedChild, _ := stored[key].(map[string]any)
			outChild, ok := out[key].(map[string]any)
			if !ok {
				if storedChild == nil {
					continue
				}
				outChild = map[string]any{}
				out[key] = outChild
			}
			mergeSecrets(prop, storedChild, outChild)
		}
	}
}

// transformSecrets copies value and rewrites every non-empty secret string
// through fn.
func transformSecrets(
	s *Schema, value map[string]any, fn func(path, secret string) (string, error),
) (map[string]any, error) {
	out := deepCopyMap(value)
	if out == nil {
		return nil, nil
	}
	if err := walkSecrets(s, out, "", fn); err != nil {
		return nil, err
	}
	return out, nil
}

func walkSecrets(s *Schema, obj map[string]any, path string, fn func(path, secret string) (string, error)) error {
	for key, prop := range s.Properties {
		v, ok := obj[key]
		if !ok {
			continue
		}
		fieldPath := joinPath(path, key)
		if prop.Secret {
			str, isStr := v.(string)
			if !isStr || str == "" {
				continue
			}
			rewritten, err := fn(fieldPath, str)
			if err != nil {
				return err
			}
			obj[key] = rewritten
			continue
		}
		if child, isObj := v.(map[string]any); isObj && prop.Type == TypeObject {
			if err := walkSecrets(prop, child, fieldPath, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return v
	}
}

// Update applies a client's edit to a stored (sealed) configuration: it keeps
// secrets the client sent back redacted, validates the result and seals it
// for storage. A validation failure is returned as FieldErrors.
func Update(s *Schema, stored, incoming map[string]any) (map[string]any, error) {
	plain, _ := OpenLenient(s, stored)
	merged := Merge(s, plain, incoming)
	if errs := Validate(s, merged); len(errs) > 0 {
		return nil, errs
	}
	return Seal(s, merged)
}
