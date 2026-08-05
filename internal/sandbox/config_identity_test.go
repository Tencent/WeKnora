package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

func identityGlobalConfig() *Config {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.E2BAPIURL = "https://api.e2b.app"
	cfg.E2BSandboxDomain = "e2b.app"
	cfg.E2BAPIKey = "global-key"
	cfg.CubeAPIURL = "https://cube.example.com"
	cfg.CubeProxyURL = "https://proxy.example.com"
	cfg.CubeSandboxDomain = "cube.app"
	cfg.CubeAPIKey = "global-cube-key"
	return cfg
}

// An empty field inherits the deployment value, so the two spellings describe
// the same provider account and must project to the same identity.
func TestIdentityOfInheritsDeploymentValues(t *testing.T) {
	global := identityGlobalConfig()

	inherited := IdentityOf(&types.TenantSandboxConfig{SandboxType: "e2b"}, global)
	spelledOut := IdentityOf(&types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIURL: "https://api.e2b.app", SandboxDomain: "e2b.app", APIKey: "global-key",
		},
	}, global)

	require.Equal(t, inherited, spelledOut)
	require.Equal(t, "https://api.e2b.app", inherited.APIURL)
	require.Equal(t, "e2b.app", inherited.SandboxDomain)
	require.Equal(t, "global-key", inherited.APIKey)
}

// Cube's built-in defaults are what the client is really constructed with, so
// typing one of them out is not a backend change either.
func TestIdentityOfAppliesCubeBuiltInDefaults(t *testing.T) {
	bare := &Config{Type: SandboxTypeCube}

	inherited := IdentityOf(&types.TenantSandboxConfig{SandboxType: "cube"}, bare)
	spelledOut := IdentityOf(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL: DefaultCubeAPIURL, ProxyURL: DefaultCubeProxyURL,
			SandboxDomain: DefaultCubeSandboxDomain,
		},
	}, bare)

	require.Equal(t, inherited, spelledOut)
	require.Equal(t, DefaultCubeProxyURL, inherited.ProxyURL)
}

// Only the active provider describes where the sandboxes are; the other
// sub-struct is leftover state from an earlier switch.
func TestIdentityOfReadsOnlyActiveProvider(t *testing.T) {
	global := identityGlobalConfig()
	cfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "e2b-key"},
		Cube:        &types.CubeSandboxConfig{APIKey: "stale-cube-key"},
	}

	identity := IdentityOf(cfg, global)

	require.Equal(t, "e2b-key", identity.APIKey)
	require.Empty(t, identity.ProxyURL, "Cube's data plane is irrelevant while E2B is active")
}

// Backends that hold no remote resources carry no credentials, but switching
// between them still has to register as a change.
func TestIdentityOfLocalBackendsCarryProviderOnly(t *testing.T) {
	global := identityGlobalConfig()

	docker := IdentityOf(&types.TenantSandboxConfig{SandboxType: "docker"}, global)
	local := IdentityOf(&types.TenantSandboxConfig{SandboxType: "local"}, global)

	require.Equal(t, SandboxIdentity{Provider: "docker"}, docker)
	require.NotEqual(t, docker, local)
}

// A nil deployment config must not panic; the config then inherits nothing.
func TestIdentityOfToleratesNilGlobal(t *testing.T) {
	identity := IdentityOf(&types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "key-a"},
	}, nil)

	require.Equal(t, SandboxIdentity{Provider: "e2b", APIKey: "key-a"}, identity)
}
