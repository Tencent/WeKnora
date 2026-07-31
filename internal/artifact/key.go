package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// InputDigest names one direct input digest participating in an artifact key.
type InputDigest struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// ProcessorIdentity identifies the deterministic processor or provider request.
type ProcessorIdentity struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model,omitempty"`
}

// KeyMaterial is the canonical input for a content-addressed artifact key.
type KeyMaterial struct {
	KeyVersion           int               `json:"key_version"`
	Stage                string            `json:"stage"`
	DirectInputs         []InputDigest     `json:"direct_inputs,omitempty"`
	Processor            ProcessorIdentity `json:"processor"`
	RenderedRequest      any               `json:"rendered_request,omitempty"`
	EffectiveOptions     any               `json:"effective_options,omitempty"`
	CanonicalizerVersion string            `json:"canonicalizer_version,omitempty"`
	OutputSchema         string            `json:"output_schema"`
}

// CanonicalDigest returns a SHA-256 digest of v after stable JSON encoding and
// secret-field scrubbing.
func CanonicalDigest(v any) (string, error) {
	canonical, err := canonicalJSON(scrubSecrets(v))
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// BuildKey returns the canonical content-addressed key for one artifact.
func BuildKey(material KeyMaterial) (string, error) {
	body := map[string]any{
		"key_version":           material.KeyVersion,
		"stage":                 material.Stage,
		"direct_inputs":         material.DirectInputs,
		"processor":             material.Processor,
		"rendered_request":      scrubSecrets(material.RenderedRequest),
		"effective_options":     scrubSecrets(material.EffectiveOptions),
		"canonicalizer_version": material.CanonicalizerVersion,
		"output_schema":         material.OutputSchema,
	}
	return CanonicalDigest(body)
}

func canonicalJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

func scrubSecrets(v any) any {
	normalized, err := normalizeJSONValue(v)
	if err == nil {
		return scrubJSONSecrets(normalized)
	}
	return scrubJSONSecrets(v)
}

func normalizeJSONValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func scrubJSONSecrets(v any) any {
	switch value := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, child := range value {
			if isSecretKey(key) {
				continue
			}
			out[key] = scrubJSONSecrets(child)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, child := range value {
			out[i] = scrubJSONSecrets(child)
		}
		return out
	default:
		return value
	}
}

func isSecretKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "authorization", "cookie", "api_key", "apikey", "token", "access_token", "refresh_token", "credential", "credentials", "secret":
		return true
	default:
		return strings.HasSuffix(normalized, "_token") ||
			strings.HasSuffix(normalized, "_secret") ||
			strings.HasSuffix(normalized, "_api_key")
	}
}
