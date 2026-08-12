package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSandboxConfigForResponseMasksSecrets(t *testing.T) {
	cfg := &TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &E2BSandboxConfig{APIKey: "e2b-secret", APIURL: "https://api.e2b.dev"},
		Cube:        &CubeSandboxConfig{APIKey: "cube-secret", APIURL: "http://cube"},
		EnvVars:     map[string]string{"HF_TOKEN": "hf-secret"},
	}

	out := SandboxConfigForResponse(cfg, true)

	require.Equal(t, RedactedSecretPlaceholder, out.E2B.APIKey)
	require.Equal(t, RedactedSecretPlaceholder, out.Cube.APIKey)
	require.Equal(t, RedactedSecretPlaceholder, out.EnvVars["HF_TOKEN"])
	// Non-secret fields stay visible.
	require.Equal(t, "https://api.e2b.dev", out.E2B.APIURL)
	require.Equal(t, "e2b", out.SandboxType)
	// Original must be untouched.
	require.Equal(t, "e2b-secret", cfg.E2B.APIKey)
	require.Equal(t, "hf-secret", cfg.EnvVars["HF_TOKEN"])
}

func TestSandboxConfigForResponseSkipsMaskingWhenDisabled(t *testing.T) {
	cfg := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: "e2b-secret"}}

	out := SandboxConfigForResponse(cfg, false)

	require.Equal(t, "e2b-secret", out.E2B.APIKey)
}

func TestSandboxConfigForResponseEmptySecretStaysEmpty(t *testing.T) {
	cfg := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: ""}}

	out := SandboxConfigForResponse(cfg, true)

	require.Empty(t, out.E2B.APIKey, "an unset secret must not become a placeholder")
}

func TestSandboxConfigForResponseNil(t *testing.T) {
	require.Nil(t, SandboxConfigForResponse(nil, true))
}

func TestMergeSandboxConfigForUpdatePreservesRedactedSecrets(t *testing.T) {
	existing := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: "old-e2b"},
		Cube:    &CubeSandboxConfig{APIKey: "old-cube"},
		EnvVars: map[string]string{"HF_TOKEN": "old-hf", "GONE": "old-gone"},
	}
	incoming := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder}, // untouched by user
		Cube:    &CubeSandboxConfig{APIKey: "new-cube"},               // user typed a new key
		EnvVars: map[string]string{"HF_TOKEN": RedactedSecretPlaceholder},
	}

	out := MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, "old-e2b", out.E2B.APIKey, "placeholder must resolve to the stored secret")
	require.Equal(t, "new-cube", out.Cube.APIKey, "an explicitly typed secret must win")
	require.Equal(t, "old-hf", out.EnvVars["HF_TOKEN"])
	require.NotContains(t, out.EnvVars, "GONE", "env vars removed by the user must not resurrect")
}

func TestMergeSandboxConfigForUpdateHandlesNilExisting(t *testing.T) {
	incoming := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder}}

	out := MergeSandboxConfigForUpdate(incoming, nil)

	require.Empty(t, out.E2B.APIKey, "placeholder with no stored value resolves to empty")
}

func TestMergeSandboxConfigForUpdateNilIncoming(t *testing.T) {
	require.Nil(t, MergeSandboxConfigForUpdate(nil, &TenantSandboxConfig{}))
}

func TestMergeSandboxConfigForUpdateDoesNotMutateInputs(t *testing.T) {
	existing := &TenantSandboxConfig{E2B: &E2BSandboxConfig{APIKey: "old-e2b"}}
	incoming := &TenantSandboxConfig{
		E2B:     &E2BSandboxConfig{APIKey: RedactedSecretPlaceholder},
		EnvVars: map[string]string{"HF_TOKEN": RedactedSecretPlaceholder},
	}

	_ = MergeSandboxConfigForUpdate(incoming, existing)

	require.Equal(t, RedactedSecretPlaceholder, incoming.E2B.APIKey,
		"merge must not mutate the incoming payload")
	require.Equal(t, "old-e2b", existing.E2B.APIKey,
		"merge must not mutate the stored config")
}
