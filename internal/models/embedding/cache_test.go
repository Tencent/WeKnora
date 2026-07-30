package embedding

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestModelFingerprintIsStableAndExcludesSecrets(t *testing.T) {
	base := Config{
		Source:                    types.ModelSourceRemote,
		BaseURL:                   "https://example.com/v1",
		ModelName:                 "embed-v1",
		ModelID:                   "model-id",
		Provider:                  "generic",
		Dimensions:                1024,
		TruncatePromptTokens:      512,
		SupportsDimensionOverride: true,
		APIKey:                    "secret-a",
		AppSecret:                 "app-secret-a",
		MaxConcurrency:            4,
		ExtraConfig:               map[string]string{"b": "2", "a": "1"},
	}
	rotated := base
	rotated.APIKey = "secret-b"
	rotated.AppSecret = "app-secret-b"
	rotated.MaxConcurrency = 99
	rotated.ExtraConfig = map[string]string{"a": "1", "b": "2"}

	require.Equal(t, ModelFingerprint(base), ModelFingerprint(rotated),
		"credential rotation, concurrency tuning and map iteration order must preserve cache hits")
}

func TestModelFingerprintChangesWithEmbeddingOutputConfiguration(t *testing.T) {
	base := Config{
		Source:               types.ModelSourceRemote,
		BaseURL:              "https://example.com/v1",
		ModelName:            "embed-v1",
		ModelID:              "model-id",
		Provider:             "generic",
		Dimensions:           1024,
		TruncatePromptTokens: 512,
	}
	baseFingerprint := ModelFingerprint(base)

	cases := map[string]func(*Config){
		"base URL":      func(c *Config) { c.BaseURL = "https://other.example/v1" },
		"model name":    func(c *Config) { c.ModelName = "embed-v2" },
		"model ID":      func(c *Config) { c.ModelID = "other-id" },
		"provider":      func(c *Config) { c.Provider = "openai" },
		"dimensions":    func(c *Config) { c.Dimensions = 1536 },
		"truncation":    func(c *Config) { c.TruncatePromptTokens = 256 },
		"extra config":  func(c *Config) { c.ExtraConfig = map[string]string{"mode": "new"} },
		"custom header": func(c *Config) { c.CustomHeaders = map[string]string{"X-Model-Route": "blue"} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			require.NotEqual(t, baseFingerprint, ModelFingerprint(changed))
		})
	}
}
