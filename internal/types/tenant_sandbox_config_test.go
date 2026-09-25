package types

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// setAESKey installs a valid 32-byte SYSTEM_AES_KEY for the test.
func setAESKey(t *testing.T) {
	t.Helper()
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
}

func TestTenantSandboxConfigEncryptsSecretsAtRest(t *testing.T) {
	setAESKey(t)

	cfg := &TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &E2BSandboxConfig{APIKey: "e2b-secret", APIURL: "https://api.e2b.dev"},
		Cube:        &CubeSandboxConfig{APIKey: "cube-secret"},
		EnvVars:     map[string]string{"HF_TOKEN": "hf-secret", "PLAIN": "not-a-secret"},
	}

	raw, err := cfg.Value()
	require.NoError(t, err)

	serialized := string(raw.([]byte))
	require.NotContains(t, serialized, "e2b-secret", "E2B API key must not be stored in plaintext")
	require.NotContains(t, serialized, "cube-secret", "Cube API key must not be stored in plaintext")
	require.NotContains(t, serialized, "hf-secret", "EnvVars values must not be stored in plaintext")
	require.NotContains(t, serialized, "not-a-secret",
		"EnvVars values must be encrypted regardless of content")
	// Keys themselves stay readable so the config remains inspectable.
	require.Contains(t, serialized, "HF_TOKEN")

	var restored TenantSandboxConfig
	require.NoError(t, restored.Scan(raw.([]byte)))

	require.Equal(t, "e2b-secret", restored.E2B.APIKey)
	require.Equal(t, "cube-secret", restored.Cube.APIKey)
	require.Equal(t, "hf-secret", restored.EnvVars["HF_TOKEN"])
	require.Equal(t, "not-a-secret", restored.EnvVars["PLAIN"])
	require.Equal(t, "https://api.e2b.dev", restored.E2B.APIURL,
		"non-secret fields stay untouched")
}

func TestTenantSandboxConfigValueDoesNotMutateReceiver(t *testing.T) {
	setAESKey(t)

	cfg := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: "e2b-secret"},
		Cube:    &CubeSandboxConfig{APIKey: "cube-secret"},
		EnvVars: map[string]string{"HF_TOKEN": "hf-secret"},
	}

	_, err := cfg.Value()
	require.NoError(t, err)

	require.Equal(t, "e2b-secret", cfg.E2B.APIKey, "Value must not mutate the original struct")
	require.Equal(t, "cube-secret", cfg.Cube.APIKey, "Value must not mutate the original struct")
	require.Equal(t, "hf-secret", cfg.EnvVars["HF_TOKEN"], "Value must not mutate the original map")
}

func TestTenantSandboxConfigScanTolerantOfUndecryptableSecrets(t *testing.T) {
	// A row written under a different/rotated key must not break loading the
	// tenant; the secret is blanked and treated as unconfigured.
	setAESKey(t)

	payload, err := json.Marshal(map[string]any{
		"sandbox_type": "e2b",
		"e2b":          map[string]string{"api_key": "enc:v1:not-really-valid-base64!!"},
		"env_vars":     map[string]string{"HF_TOKEN": "enc:v1:also-invalid!!"},
	})
	require.NoError(t, err)

	var cfg TenantSandboxConfig
	require.NoError(t, cfg.Scan(payload), "Scan must not fail on undecryptable secrets")
	require.Empty(t, cfg.E2B.APIKey)
	require.Empty(t, cfg.EnvVars["HF_TOKEN"])
	require.Equal(t, "e2b", cfg.SandboxType, "non-secret fields still load")
}

func TestTenantSandboxConfigNilRoundTrip(t *testing.T) {
	var cfg *TenantSandboxConfig

	raw, err := cfg.Value()
	require.NoError(t, err)
	require.Nil(t, raw, "a nil config persists as SQL NULL")

	var restored TenantSandboxConfig
	require.NoError(t, restored.Scan(nil), "scanning NULL is a no-op")
}
