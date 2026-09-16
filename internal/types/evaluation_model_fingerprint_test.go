package types

import (
	"strings"
	"testing"
	"time"
)

func evaluationModelFingerprintFixture() *Model {
	return &Model{
		ID:        "chat-model-1",
		Name:      "gpt-fixture",
		Type:      ModelTypeKnowledgeQA,
		Source:    ModelSourceOpenAI,
		UpdatedAt: time.Date(2026, 8, 28, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)),
		Parameters: ModelParameters{
			BaseURL:       "https://user:secret@api.example.test/v1",
			APIKey:        "sk-live-secret",
			InterfaceType: "openai",
			Provider:      "openai",
			CustomHeaders: map[string]string{
				"X-Tenant-Auth": "bearer abc",
				"X-Request-ID":  "request-1",
				"X-Model-Route": "blue",
			},
			ExtraConfig: map[string]string{
				"organization":      "org-1",
				"behavior_revision": "route-v1",
				"session_token":     "drop-me",
				"openaiApiKey":      "drop-me-too",
				"tokenizer":         "cl100k_base",
				"max_tokens":        "2048",
			},
			SupportsVision: true,
			MaxConcurrency: 4,
		},
	}
}

func TestEvaluationModelConfigSHA256ExcludesCredentialsAndHeaders(t *testing.T) {
	model := evaluationModelFingerprintFixture()
	baseline := EvaluationModelConfigSHA256(model)

	// Rotating credentials must not change the behavior fingerprint.
	rotated := evaluationModelFingerprintFixture()
	rotated.Parameters.APIKey = "sk-rotated"
	rotated.Parameters.AppSecret = "new-secret"
	rotated.Parameters.ExtraConfig["session_token"] = "rotated-session"
	rotated.Parameters.ExtraConfig["openaiApiKey"] = "rotated-api-key"
	rotated.Parameters.CustomHeaders = map[string]string{
		"X-Tenant-Auth": "bearer other",
		"X-Request-ID":  "request-2",
		"X-Model-Route": "green",
	}
	if got := EvaluationModelConfigSHA256(rotated); got != baseline {
		t.Fatalf("fingerprint changed after credential or request-header rotation: %s != %s", got, baseline)
	}

	// The fingerprint is stable regardless of the ExtraConfig assembly order.
	reordered := evaluationModelFingerprintFixture()
	reordered.Parameters.ExtraConfig = map[string]string{
		"max_tokens": "2048", "tokenizer": "cl100k_base",
		"behavior_revision": "route-v1", "session_token": "drop-me",
		"openaiApiKey": "drop-me-too", "organization": "org-1",
	}
	if got := EvaluationModelConfigSHA256(reordered); got != baseline {
		t.Fatal("fingerprint must not depend on map assembly order")
	}
}

func TestEvaluationModelConfigSHA256CoversBehavior(t *testing.T) {
	baseline := EvaluationModelConfigSHA256(evaluationModelFingerprintFixture())
	cases := []struct {
		name   string
		mutate func(*Model)
	}{
		{"base url host", func(m *Model) { m.Parameters.BaseURL = "https://other.example.test/v1" }},
		{"interface type", func(m *Model) { m.Parameters.InterfaceType = "anthropic" }},
		{"provider", func(m *Model) { m.Parameters.Provider = "generic" }},
		{"vision", func(m *Model) { m.Parameters.SupportsVision = false }},
		{"concurrency", func(m *Model) { m.Parameters.MaxConcurrency = 8 }},
		{"extra config", func(m *Model) { m.Parameters.ExtraConfig["organization"] = "org-2" }},
		{"tokenizer config", func(m *Model) { m.Parameters.ExtraConfig["tokenizer"] = "o200k_base" }},
		{"max tokens config", func(m *Model) { m.Parameters.ExtraConfig["max_tokens"] = "4096" }},
		{"behavior revision", func(m *Model) { m.Parameters.ExtraConfig["behavior_revision"] = "route-v2" }},
		{"embedding dimension", func(m *Model) { m.Parameters.EmbeddingParameters.Dimension = 1024 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := evaluationModelFingerprintFixture()
			tc.mutate(model)
			if got := EvaluationModelConfigSHA256(model); got == baseline {
				t.Fatalf("fingerprint did not change after %s mutation", tc.name)
			}
		})
	}
}

func TestEvaluationModelSanitizeBaseURLStripsUserinfo(t *testing.T) {
	rawURL := "https://user:secret@api.example.test/v1?api_key=secret&api-version=2026-08-01#client-only"
	if got := EvaluationModelSanitizeBaseURL(rawURL); got != "https://api.example.test/v1?api-version=2026-08-01" {
		t.Fatalf("SanitizeBaseURL = %q, want credentials stripped", got)
	}
	if got := EvaluationModelSanitizeBaseURL(""); got != "" {
		t.Fatalf("SanitizeBaseURL(empty) = %q, want empty", got)
	}
}

func TestEvaluationModelConfigSHA256KeepsOnlyBehavioralBaseURLQuery(t *testing.T) {
	first := evaluationModelFingerprintFixture()
	first.Parameters.BaseURL = "https://api.example.test/v1?api-version=2026-08-01&access_token=first#one"
	rotatedSecret := evaluationModelFingerprintFixture()
	rotatedSecret.Parameters.BaseURL = "https://api.example.test/v1?access_token=second&api-version=2026-08-01#two"
	if EvaluationModelConfigSHA256(first) != EvaluationModelConfigSHA256(rotatedSecret) {
		t.Fatal("credential query rotation or fragment changed the model fingerprint")
	}

	changedVersion := evaluationModelFingerprintFixture()
	changedVersion.Parameters.BaseURL = "https://api.example.test/v1?api-version=2026-09-01&access_token=second"
	if EvaluationModelConfigSHA256(first) == EvaluationModelConfigSHA256(changedVersion) {
		t.Fatal("behavioral api-version query did not change the model fingerprint")
	}
}

func TestEvaluationModelSnapshotFromNeverCarriesSecretMaterial(t *testing.T) {
	model := evaluationModelFingerprintFixture()
	snapshot := EvaluationModelSnapshotFrom(model)
	if snapshot == nil {
		t.Fatal("snapshot is nil")
	}
	if snapshot.UpstreamName != "gpt-fixture" || snapshot.Provider != "openai" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.UpdatedAt.Location() != time.UTC {
		t.Fatal("snapshot updated_at must be UTC")
	}
	serialized := snapshot.ConfigSHA256 + snapshot.ID + snapshot.UpstreamName
	for _, secret := range []string{"sk-live-secret", "secret", "bearer abc", "drop-me"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("snapshot carries secret material %q", secret)
		}
	}
	if EvaluationModelSnapshotFrom(nil) != nil {
		t.Fatal("nil model must produce a nil snapshot")
	}
}
