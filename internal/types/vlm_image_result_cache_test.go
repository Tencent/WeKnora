package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVLMFingerprintPayload_SanitizesSecrets(t *testing.T) {
	model := &Model{
		ID:     "vlm-1",
		Name:   "gpt-4o-mini",
		Source: ModelSourceRemote,
		Parameters: ModelParameters{
			BaseURL:       "https://example.com/v1",
			APIKey:        "secret-api-key",
			InterfaceType: "openai",
			Provider:      "generic",
			AppSecret:     "encrypted-secret",
			ExtraConfig: map[string]string{
				"remote_model_name": "gpt-4o-mini-2024",
				"temperature":       "0.1",
				"api_key":           "extra-secret",
			},
			CustomHeaders: map[string]string{
				"X-Route":       "vision",
				"Authorization": "Bearer token",
				"X-API-Key":     "header-secret",
			},
		},
	}

	payload := BuildVLMFingerprintPayloadFromModel(model)
	if payload.ExtraConfig["temperature"] != "0.1" {
		t.Fatalf("expected non-sensitive extra_config to remain: %+v", payload.ExtraConfig)
	}
	if payload.ExtraConfig["api_key"] != "" {
		t.Fatalf("api_key should be stripped from extra_config: %+v", payload.ExtraConfig)
	}
	if payload.CustomHeaders["X-Route"] != "vision" {
		t.Fatalf("expected non-sensitive custom header to remain: %+v", payload.CustomHeaders)
	}
	if _, ok := payload.CustomHeaders["Authorization"]; ok {
		t.Fatalf("Authorization should be stripped from custom_headers: %+v", payload.CustomHeaders)
	}
	if _, ok := payload.CustomHeaders["X-API-Key"]; ok {
		t.Fatalf("X-API-Key should be stripped from custom_headers: %+v", payload.CustomHeaders)
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	serialized := strings.ToLower(string(b))
	for _, forbidden := range []string{"secret-api-key", "encrypted-secret", "extra-secret", "bearer token", "header-secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("sanitized payload leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestVLMFingerprint_IgnoresSecretChanges(t *testing.T) {
	base := &Model{
		ID:     "vlm-1",
		Name:   "vision-model",
		Source: ModelSourceRemote,
		Parameters: ModelParameters{
			BaseURL:       "https://example.com/v1",
			APIKey:        "secret-a",
			InterfaceType: "openai",
			Provider:      "generic",
			AppSecret:     "app-secret-a",
			ExtraConfig:   map[string]string{"temperature": "0.1", "api_key": "extra-a"},
			CustomHeaders: map[string]string{"X-Route": "vision", "Authorization": "Bearer a"},
		},
	}
	changedSecrets := *base
	changedSecrets.Parameters = base.Parameters
	changedSecrets.Parameters.APIKey = "secret-b"
	changedSecrets.Parameters.AppSecret = "app-secret-b"
	changedSecrets.Parameters.ExtraConfig = map[string]string{"temperature": "0.1", "api_key": "extra-b"}
	changedSecrets.Parameters.CustomHeaders = map[string]string{"X-Route": "vision", "Authorization": "Bearer b"}

	fp1, err := BuildVLMFingerprintPayloadFromModel(base).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := BuildVLMFingerprintPayloadFromModel(&changedSecrets).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint should ignore secret-only changes: %s != %s", fp1, fp2)
	}
}

func TestVLMFingerprint_ChangesForRoutingFields(t *testing.T) {
	model := &Model{
		ID:     "vlm-1",
		Name:   "vision-model",
		Source: ModelSourceRemote,
		Parameters: ModelParameters{
			BaseURL:       "https://example.com/v1",
			InterfaceType: "openai",
			Provider:      "generic",
		},
	}
	fp1, err := BuildVLMFingerprintPayloadFromModel(model).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}

	changed := *model
	changed.Parameters = model.Parameters
	changed.Parameters.BaseURL = "https://other.example.com/v1"
	fp2, err := BuildVLMFingerprintPayloadFromModel(&changed).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp2 {
		t.Fatal("fingerprint should change when routing fields change")
	}
}

func TestVLMImageResultCacheKey_IsCanonicalAndVersioned(t *testing.T) {
	payload := VLMImageResultCacheKeyPayload{
		ImageHash:                  "image-hash",
		ModelFingerprint:           "model-fingerprint",
		ResultType:                 VLMImageResultTypeOCR,
		PromptVersion:              VLMOCRPromptVersion,
		PromptHash:                 "prompt-hash",
		ResultCanonicalizerVersion: VLMOCRCanonicalizerV1,
	}
	key1, err := BuildVLMImageResultCacheKey(payload)
	if err != nil {
		t.Fatal(err)
	}
	key2, err := BuildVLMImageResultCacheKey(payload)
	if err != nil {
		t.Fatal(err)
	}
	if key1 != key2 {
		t.Fatalf("cache key should be deterministic: %s != %s", key1, key2)
	}

	payload.ResultCanonicalizerVersion = "ocr_sanitize_v2"
	key3, err := BuildVLMImageResultCacheKey(payload)
	if err != nil {
		t.Fatal(err)
	}
	if key1 == key3 {
		t.Fatal("cache key should change when result canonicalizer version changes")
	}
}
