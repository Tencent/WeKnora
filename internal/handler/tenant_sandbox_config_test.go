package handler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestSanitizeSandboxConfigForUpdatePreservesRedactedSecret(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	existing := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "stored-key", APIURL: "https://203.0.113.10"},
	}
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey: types.RedactedSecretPlaceholder,
			APIURL: "https://203.0.113.10",
		},
	}

	out, err := SanitizeSandboxConfigForUpdate(incoming, existing)

	require.NoError(t, err)
	require.Equal(t, "stored-key", out.E2B.APIKey)
}

func TestSanitizeSandboxConfigForUpdateRejectsUnsafeURL(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIURL: "http://169.254.169.254"},
	}

	_, err := SanitizeSandboxConfigForUpdate(incoming, nil)

	require.ErrorIs(t, err, sandbox.ErrUnsafeOutboundURL)
}

func TestSanitizeSandboxConfigForUpdateRejectsUnknownType(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	incoming := &types.TenantSandboxConfig{SandboxType: "quantum"}

	_, err := SanitizeSandboxConfigForUpdate(incoming, nil)

	require.Error(t, err)
}

// Without an AES key the Value() hook would write these secrets as plaintext,
// so saving must be refused rather than silently downgrading storage security.
func TestSanitizeSandboxConfigForUpdateRefusesSecretsWithoutAESKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", "")
	incoming := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "plaintext-risk"},
	}

	_, err := SanitizeSandboxConfigForUpdate(incoming, nil)

	require.Error(t, err)

	// A config without secrets is still allowed in that deployment.
	_, err = SanitizeSandboxConfigForUpdate(
		&types.TenantSandboxConfig{SandboxType: "local"}, nil)
	require.NoError(t, err)
}
