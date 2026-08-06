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

func TestResolveEffectiveConfigNilTenantKeepsGlobal(t *testing.T) {
	global := globalTestConfig()

	got, err := ResolveEffectiveConfig(nil, global)

	require.NoError(t, err)
	require.Equal(t, *global, *got, "existing tenants must behave exactly as before")
}

func TestResolveEffectiveConfigDoesNotMutateGlobal(t *testing.T) {
	global := globalTestConfig()
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "tenant-key"},
	}

	_, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, "global-key", global.E2BAPIKey,
		"resolution must not leak tenant values into the shared global config")
}

func TestResolveEffectiveConfigFieldLevelFallback(t *testing.T) {
	global := globalTestConfig()
	// Tenant overrides only the API key; everything else must be inherited.
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "e2b",
		E2B:         &types.E2BSandboxConfig{APIKey: "tenant-key"},
	}

	got, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, "tenant-key", got.E2BAPIKey)
	require.Equal(t, "https://global.e2b.dev", got.E2BAPIURL)
	require.Equal(t, "global-template", got.E2BTemplate)
	require.Equal(t, "global.domain", got.E2BSandboxDomain)
}

func TestResolveEffectiveConfigSwitchesProvider(t *testing.T) {
	global := globalTestConfig() // global is e2b
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType: "cube",
		Cube: &types.CubeSandboxConfig{
			APIKey:     "cube-key",
			APIURL:     "https://203.0.113.20",
			TemplateID: "cube-template",
		},
	}

	got, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, SandboxTypeCube, got.Type)
	require.Equal(t, "cube-key", got.CubeAPIKey)
	require.Equal(t, "https://203.0.113.20", got.CubeAPIURL)
	require.Equal(t, "cube-template", got.CubeTemplate)
}

func TestResolveEffectiveConfigAppliesTimeoutsAndTTL(t *testing.T) {
	global := globalTestConfig()
	tenantCfg := &types.TenantSandboxConfig{
		SandboxType:       "e2b",
		DefaultTimeoutSec: 90,
		E2B: &types.E2BSandboxConfig{
			HTTPTimeoutSec:       15,
			E2BSandboxTTLSeconds: 600,
		},
	}

	got, err := ResolveEffectiveConfig(tenantCfg, global)

	require.NoError(t, err)
	require.Equal(t, 90*time.Second, got.DefaultTimeout)
	require.Equal(t, 15*time.Second, got.E2BHTTPTimeout)
	require.Equal(t, 600*time.Second, got.E2BSandboxTTL)
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
