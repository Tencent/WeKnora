package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// Identity comes from the stored row alone. Nothing about the deployment can
// change it, which is what makes "did this edit strand a sandbox" answerable
// without reaching for WEKNORA_SANDBOX_*.
func TestIdentityOfReadsStoredFieldsOnly(t *testing.T) {
	identity := IdentityOf(&types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIURL: "https://api.e2b.app", SandboxDomain: "e2b.app", APIKey: "key-a",
		},
	})

	require.Equal(t, SandboxIdentity{
		Provider:      "e2b",
		APIURL:        "https://api.e2b.app",
		APIKey:        "key-a",
		SandboxDomain: "e2b.app",
	}, identity)
}

// A blank field is blank, not "whatever the deployment runs". Two configs that
// differ only in what they spell out are therefore different identities.
func TestIdentityOfDoesNotInherit(t *testing.T) {
	blank := IdentityOf(&types.TenantSandboxConfig{SandboxType: "cube"})
	spelledOut := IdentityOf(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: DefaultCubeAPIURL, ProxyURL: DefaultCubeProxyURL,
			SandboxDomain: DefaultCubeSandboxDomain,
		},
	})

	require.Equal(t, SandboxIdentity{Provider: "cube"}, blank)
	require.NotEqual(t, blank, spelledOut)
	require.Equal(t, DefaultCubeProxyURL, spelledOut.ProxyURL)
}

// Only the active provider describes where the sandboxes are; the other
// sub-struct is leftover state from an earlier switch.
func TestIdentityOfReadsOnlyActiveProvider(t *testing.T) {
	cfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "e2b-key"},
		Cube:        &types.CubeSandboxConfig{APIKey: "stale-cube-key"},
	}

	identity := IdentityOf(cfg)

	require.Equal(t, "e2b-key", identity.APIKey)
	require.Empty(t, identity.ProxyURL, "Cube's data plane is irrelevant while E2B is active")
}

// Backends that hold no remote resources carry no credentials, but switching
// between them still has to register as a change.
func TestIdentityOfLocalBackendsCarryProviderOnly(t *testing.T) {
	docker := IdentityOf(&types.TenantSandboxConfig{SandboxType: "docker"})
	local := IdentityOf(&types.TenantSandboxConfig{SandboxType: "local"})

	require.Equal(t, SandboxIdentity{Provider: "docker"}, docker)
	require.NotEqual(t, docker, local)
}

// A nil config must not panic: Update compares against it when creating.
func TestIdentityOfToleratesNilConfig(t *testing.T) {
	require.Equal(t, SandboxIdentity{}, IdentityOf(nil))
}
