package sandbox

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func globalTestConfig() *Config {
	return &Config{
		Type:             SandboxTypeE2B,
		DefaultTimeout:   60 * time.Second,
		E2BAPIKey:        "global-key",
		E2BAPIURL:        "https://global.e2b.dev",
		E2BSandboxDomain: "global.domain",
		E2BTemplate:      "global-template",
		E2BSandboxTTL:    10 * time.Minute,
		E2BHTTPTimeout:   30 * time.Second,
	}
}

// completeE2BTenantConfig is the minimum a named E2B config must carry now that
// nothing is inherited.
func completeE2BTenantConfig() *types.TenantSandboxConfig {
	return &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B: &types.E2BSandboxConfig{
			APIKey:     "tenant-key",
			TemplateID: "tenant-template",
		},
	}
}

// A nil tenant config means "the deployment default backend", which is the one
// path where the baseline is used as-is.
func TestResolveEffectiveConfigNilTenantKeepsGlobal(t *testing.T) {
	global := globalTestConfig()

	got, err := ResolveEffectiveConfig(nil, global)

	require.NoError(t, err)
	require.Equal(t, *global, *got)
}

func TestResolveEffectiveConfigDoesNotMutateGlobal(t *testing.T) {
	global := globalTestConfig()

	_, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, "global-key", global.E2BAPIKey,
		"resolution must not leak tenant values into the shared global config")
}

// The point of the whole design: a named config never picks up the deployment's
// endpoint, domain or key, so what it does not state it does not get.
func TestResolveEffectiveConfigDoesNotInheritProviderFields(t *testing.T) {
	global := globalTestConfig()

	got, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, "tenant-key", got.E2BAPIKey)
	require.Equal(t, "tenant-template", got.E2BTemplate)
	require.Empty(t, got.E2BAPIURL, "go-e2b resolves its own API base when unset")
	require.Empty(t, got.E2BSandboxDomain)
}

// A leftover sub-struct from the deployment's other provider must not survive
// either, or a cube config would silently answer with e2b coordinates.
func TestResolveEffectiveConfigClearsInactiveProviderBaseline(t *testing.T) {
	global := globalTestConfig() // global is e2b, with e2b credentials set

	got, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIKey: "cube-key", APIURL: "https://203.0.113.20",
			ProxyURL: "https://203.0.113.21", SandboxDomain: "cube.example",
			TemplateID: "cube-template",
		},
	}, global)

	require.NoError(t, err)
	require.Equal(t, SandboxTypeCube, got.Type)
	require.Equal(t, "https://203.0.113.20", got.CubeAPIURL)
	require.Empty(t, got.E2BAPIKey, "the baseline's e2b credentials must not ride along")
	require.Empty(t, got.E2BTemplate)
}

func TestResolveEffectiveConfigRejectsIncompleteCube(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube:        &types.CubeSandboxConfig{APIURL: "https://203.0.113.20"},
	}, globalTestConfig())

	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
	require.Contains(t, err.Error(), "proxy_url")
	require.Contains(t, err.Error(), "sandbox_domain")
	require.Contains(t, err.Error(), "template_id")
}

func TestResolveEffectiveConfigRejectsIncompleteE2B(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{TemplateID: "t1"},
	}, globalTestConfig())

	require.ErrorIs(t, err, ErrSandboxConfigIncomplete)
	require.Contains(t, err.Error(), "api_key")
}

func TestResolveEffectiveConfigAppliesTimeoutsAndTTL(t *testing.T) {
	global := globalTestConfig()
	tenantCfg := completeE2BTenantConfig()
	tenantCfg.DefaultTimeoutSec = 90
	tenantCfg.E2B.HTTPTimeoutSec = 15
	tenantCfg.E2B.E2BSandboxTTLSeconds = 600

	got, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, 90*time.Second, got.DefaultTimeout)
	require.Equal(t, 15*time.Second, got.E2BHTTPTimeout)
	require.Equal(t, 600*time.Second, got.E2BSandboxTTL)
}

// Tuning fields fall back to the built-in constants, never to the deployment's:
// "inherits nothing" would be a much weaker rule with an exception here.
func TestResolveEffectiveConfigTuningFallsBackToBuiltIns(t *testing.T) {
	global := globalTestConfig()
	global.E2BSandboxTTL = 10 * time.Minute
	global.E2BHTTPTimeout = 90 * time.Second

	got, err := ResolveEffectiveConfig(completeE2BTenantConfig(), global)

	require.NoError(t, err)
	require.Equal(t, DefaultE2BSandboxTTL, got.E2BSandboxTTL)
	require.Equal(t, DefaultE2BHTTPTimeout, got.E2BHTTPTimeout)
	require.Equal(t, global.DefaultTimeout, got.DefaultTimeout,
		"the execution timeout is deployment policy and does carry over")
}

func TestResolveEffectiveConfigDisabled(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{SandboxType: "disabled"}

	got, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.NoError(t, err)
	require.Equal(t, SandboxTypeDisabled, got.Type)
}

func TestResolveEffectiveConfigRejectsUnknownType(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{SandboxType: "quantum"}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.Error(t, err)
}

func TestResolveEffectiveConfigRejectsUnsafeURL(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIURL: "http://169.254.169.254"},
	}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.ErrorIs(t, err, ErrUnsafeOutboundURL)
}

func TestResolveEffectiveConfigRejectsUnsafeCubeProxyURL(t *testing.T) {
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIURL:   "https://203.0.113.10",
			ProxyURL: "http://127.0.0.1:80",
		},
	}

	_, err := ResolveEffectiveConfig(tenantCfg, globalTestConfig())

	require.ErrorIs(t, err, ErrUnsafeOutboundURL)
}

func TestResolveEffectiveConfigRejectsDockerHostNetwork(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker: &types.DockerSandboxConfig{
			Image:       "weknora:test",
			NetworkMode: "host",
		},
	}, DefaultConfig())
	require.Error(t, err)
}

// A blank host must come out of config resolution already pointing at the
// daemon the Docker CLI would use, because that resolved value is what the
// connectivity check dials. Leaving it empty here is what made the check fail
// on a Colima or Docker Desktop host, where /var/run/docker.sock is absent.
func TestResolveEffectiveConfigDetectsLocalDockerHostWhenBlank(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///tmp/from-env.sock")

	effective, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType: "docker",
		Docker:      &types.DockerSandboxConfig{Image: "weknora:test"},
	}, DefaultConfig())

	require.NoError(t, err)
	require.Equal(t, "unix:///tmp/from-env.sock", effective.DockerHost)
}

func TestResolveEffectiveConfigRejectsPlaintextDockerTCP(t *testing.T) {
	_, err := ResolveEffectiveConfig(&types.TenantSandboxConfig{
		SandboxType:           "docker",
		AllowPrivateEndpoints: true,
		Docker: &types.DockerSandboxConfig{
			Image: "weknora:test",
			Host:  "tcp://10.0.0.5:2376",
		},
	}, DefaultConfig())
	require.Error(t, err)
}

func TestEffectiveTemplateIDPerProvider(t *testing.T) {
	require.Equal(t, "e2b-tpl", EffectiveTemplateID(&Config{
		Type: SandboxTypeE2B, E2BTemplate: "e2b-tpl", CubeTemplate: "cube-tpl",
	}))
	require.Equal(t, "cube-tpl", EffectiveTemplateID(&Config{
		Type: SandboxTypeCube, E2BTemplate: "e2b-tpl", CubeTemplate: "cube-tpl",
	}))
	require.Empty(t, EffectiveTemplateID(&Config{Type: SandboxTypeLocal}))
	require.Empty(t, EffectiveTemplateID(nil))
}
