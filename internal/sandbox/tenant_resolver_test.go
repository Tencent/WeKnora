package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type stubTenantConfigLoader struct {
	result ResolvedTenantSandboxConfig
	hits   int
}

func (s *stubTenantConfigLoader) Load(
	context.Context, uint64, string,
) (ResolvedTenantSandboxConfig, error) {
	s.hits++
	return s.result, nil
}

func newTestResolver(t *testing.T, loader TenantSandboxConfigLoader) (TenantSandboxResolver, Manager) {
	t.Helper()
	fallback := NewDisabledManager()
	resolver, err := NewTenantSandboxResolver(TenantSandboxResolverDeps{
		GlobalConfig: &Config{
			Type:           SandboxTypeE2B,
			DefaultTimeout: 60 * time.Second,
			E2BAPIKey:      "global-key",
			E2BAPIURL:      "https://203.0.113.30",
			E2BTemplate:    "global-template",
			E2BSandboxTTL:  time.Minute,
			E2BHTTPTimeout: 30 * time.Second,
		},
		DefaultManager: fallback,
		Loader:         loader,
		Store:          NewMemorySessionSandboxBindingStore(),
		Checker:        PermissiveSessionExistenceChecker{},
	})
	require.NoError(t, err)
	return resolver, fallback
}

func TestResolveEmptyConfigIDIsDisabled(t *testing.T) {
	resolver, _ := newTestResolver(t, &stubTenantConfigLoader{})

	mgr, err := resolver.Resolve(context.Background(), 42, "")

	require.NoError(t, err)
	require.Equal(t, SandboxTypeDisabled, mgr.GetType())
}

// A cordoned config must not produce a manager: the whole point of the cordon
// is that no new sandbox may be created on credentials about to be replaced.
func TestResolveRefusesCordonedConfig(t *testing.T) {
	resolver, _ := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{Found: true, Cordoned: true},
	})

	mgr, err := resolver.Resolve(context.Background(), 7, "cfg-a")

	require.Nil(t, mgr)
	require.ErrorIs(t, err, ErrSandboxConfigCordoned)
}

// A missing config must fail loudly. Falling back to the global default would
// silently run the agent on a DIFFERENT backend than the one it was pointed at.
func TestResolveRefusesMissingConfig(t *testing.T) {
	resolver, _ := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{Found: false},
	})

	mgr, err := resolver.Resolve(context.Background(), 7, "cfg-gone")

	require.Nil(t, mgr)
	require.ErrorIs(t, err, ErrSandboxConfigNotFound)
}

// Construction must not perform a Health round-trip (it happens per request),
// and skipping it must also skip the silent fallback to LocalSandbox: a tenant
// that chose E2B should never have scripts quietly run as a local process.
func TestResolveBuildsRemoteManagerWithoutHealthProbe(t *testing.T) {
	resolver, fallback := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{
			Config: &types.TenantSandboxConfig{
				SandboxType: "e2b",
				E2B: &types.E2BSandboxConfig{
					APIKey: "tenant-key", TemplateID: "tenant-template",
				},
			},
			Found: true,
		},
	})

	mgr, err := resolver.Resolve(context.Background(), 42, "cfg-a")

	require.NoError(t, err)
	require.NotSame(t, fallback, mgr)
	require.Equal(t, SandboxTypeE2B, mgr.GetType())
}

// Docker is a session-scoped backend like the MicroVM ones: it must resolve to
// a SessionBoundManager that advertises shell_exec and the session file store,
// not to the stateless DefaultManager it used to get.
func TestResolveBuildsSessionBoundManagerForDocker(t *testing.T) {
	// Pinned so the resolved daemon endpoint does not depend on whatever the
	// machine running the tests points its docker CLI at: a developer on a
	// tcp:// daemon would otherwise trip the TLS requirement.
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	resolver, fallback := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{
			Config: &types.TenantSandboxConfig{
				SandboxType: "docker",
				Docker: &types.DockerSandboxConfig{
					Image: "wechatopenai/weknora-sandbox:test",
				},
			},
			Found: true,
		},
	})

	mgr, err := resolver.Resolve(context.Background(), 42, "cfg-docker")

	require.NoError(t, err)
	require.NotSame(t, fallback, mgr)
	require.Equal(t, SandboxTypeDocker, mgr.GetType())

	capabilities, ok := mgr.(SessionCapabilityProvider)
	require.True(t, ok, "docker must expose session-scoped capabilities")
	require.NotNil(t, capabilities.SessionShellExecutor())
	require.NotNil(t, capabilities.SessionFileStore())
}

// The image is this backend's template. Without it there is nothing to create a
// sandbox from, so the config must be refused up front rather than at the first
// execution.
func TestResolveRefusesDockerConfigWithoutImage(t *testing.T) {
	resolver, _ := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{
			Config: &types.TenantSandboxConfig{SandboxType: "docker"},
			Found:  true,
		},
	})

	mgr, err := resolver.Resolve(context.Background(), 42, "cfg-docker")

	require.Nil(t, mgr)
	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
}

func TestResolveBuildsLocalManagerFromWorkspaceConfig(t *testing.T) {
	resolver, fallback := newTestResolver(t, &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{
			Config: &types.TenantSandboxConfig{
				SandboxType:       "local",
				DefaultTimeoutSec: 17,
				EnvVars:           map[string]string{"WORKSPACE_FLAG": "enabled"},
			},
			Found: true,
		},
	})

	mgr, err := resolver.Resolve(context.Background(), 42, "cfg-local")

	require.NoError(t, err)
	require.NotSame(t, fallback, mgr)
	require.Equal(t, SandboxTypeLocal, mgr.GetType())
}

// No caching: the loader is consulted on every Resolve, which is what makes a
// config change take effect on the next request with no invalidation step.
func TestResolveBuildsFreshManagerEveryCall(t *testing.T) {
	loader := &stubTenantConfigLoader{
		result: ResolvedTenantSandboxConfig{
			Config: &types.TenantSandboxConfig{
				SandboxType: "e2b",
				E2B: &types.E2BSandboxConfig{
					APIKey: "tenant-key", TemplateID: "tenant-template",
				},
			},
			Found: true,
		},
	}
	resolver, _ := newTestResolver(t, loader)

	first, err := resolver.Resolve(context.Background(), 42, "cfg-a")
	require.NoError(t, err)
	second, err := resolver.Resolve(context.Background(), 42, "cfg-a")
	require.NoError(t, err)

	require.NotSame(t, first, second)
	require.Equal(t, 2, loader.hits)
}
