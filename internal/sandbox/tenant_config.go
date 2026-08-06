// Package sandbox: tenant sandbox configuration resolution.
//
// ResolveEffectiveConfig merges a tenant's stored overrides onto the
// process-wide defaults built from WEKNORA_SANDBOX_* environment variables.
//
// Merging is field-level on purpose: a tenant that only supplies an API key
// still inherits the global endpoint, domain and template. A nil tenant config
// yields the global config unchanged, which is what keeps existing
// deployments byte-for-byte identical after this feature lands.
package sandbox

import (
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ResolveEffectiveConfig returns the Config to build a tenant's sandbox
// manager from.
func ResolveEffectiveConfig(
	tenantCfg *types.TenantSandboxConfig,
	global *Config,
) (*Config, error) {
	if global == nil {
		return nil, fmt.Errorf("sandbox: global config is required")
	}
	effective := *global
	if tenantCfg == nil {
		return &effective, nil
	}

	if tenantCfg.SandboxType != "" {
		resolved, err := ParseSandboxType(tenantCfg.SandboxType)
		if err != nil {
			return nil, err
		}
		effective.Type = resolved
	}
	overrideSeconds(&effective.DefaultTimeout, tenantCfg.DefaultTimeoutSec)

	if cube := tenantCfg.Cube; cube != nil {
		if err := overrideURL(&effective.CubeAPIURL, cube.APIURL); err != nil {
			return nil, err
		}
		if err := overrideURL(&effective.CubeProxyURL, cube.ProxyURL); err != nil {
			return nil, err
		}
		overrideString(&effective.CubeSandboxDomain, cube.SandboxDomain)
		overrideString(&effective.CubeAPIKey, cube.APIKey)
		overrideString(&effective.CubeTemplate, cube.TemplateID)
		overrideSeconds(&effective.CubeHTTPTimeout, cube.HTTPTimeoutSec)
		overrideSeconds(&effective.CubeSandboxTTL, cube.CubeSandboxTTLSeconds)
	}

	if e2bCfg := tenantCfg.E2B; e2bCfg != nil {
		if err := overrideURL(&effective.E2BAPIURL, e2bCfg.APIURL); err != nil {
			return nil, err
		}
		overrideString(&effective.E2BSandboxDomain, e2bCfg.SandboxDomain)
		overrideString(&effective.E2BAPIKey, e2bCfg.APIKey)
		overrideString(&effective.E2BTemplate, e2bCfg.TemplateID)
		overrideSeconds(&effective.E2BHTTPTimeout, e2bCfg.HTTPTimeoutSec)
		overrideSeconds(&effective.E2BSandboxTTL, e2bCfg.E2BSandboxTTLSeconds)
	}

	if docker := tenantCfg.Docker; docker != nil {
		overrideString(&effective.DockerImage, docker.Image)
	}

	switch effective.Type {
	case SandboxTypeCube:
		applyCubeDefaults(&effective)
	case SandboxTypeE2B:
		applyE2BDefaults(&effective)
	}
	return &effective, nil
}

// ErrUnsupportedSandboxType marks a sandbox type string we cannot honour. It is
// a sentinel so callers can classify it as bad input without matching on the
// message text.
var ErrUnsupportedSandboxType = errors.New("sandbox: unsupported sandbox type")

// ParseSandboxType maps a stored string onto a SandboxType. Unknown values are
// rejected so a typo surfaces when the admin saves the config, instead of
// silently disabling that tenant's sandbox at first use.
func ParseSandboxType(raw string) (SandboxType, error) {
	switch SandboxType(raw) {
	case SandboxTypeCube:
		return SandboxTypeCube, nil
	case SandboxTypeE2B:
		return SandboxTypeE2B, nil
	case SandboxTypeDocker:
		return SandboxTypeDocker, nil
	case SandboxTypeLocal:
		return SandboxTypeLocal, nil
	case SandboxTypeDisabled:
		return SandboxTypeDisabled, nil
	default:
		return "", fmt.Errorf("%w %q", ErrUnsupportedSandboxType, raw)
	}
}

// EffectiveTemplateID returns the template the given provider will use.
func EffectiveTemplateID(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	switch cfg.Type {
	case SandboxTypeCube:
		return cfg.CubeTemplate
	case SandboxTypeE2B:
		return cfg.E2BTemplate
	default:
		return ""
	}
}

func overrideString(dst *string, value string) {
	if value != "" {
		*dst = value
	}
}

// overrideURL is overrideString for endpoint fields: a tenant-supplied URL
// must pass the SSRF guard before it is accepted into the effective config.
func overrideURL(dst *string, value string) error {
	if value == "" {
		return nil
	}
	if err := ValidateOutboundURL(value); err != nil {
		return err
	}
	*dst = value
	return nil
}

func overrideSeconds(dst *time.Duration, seconds int) {
	if seconds > 0 {
		*dst = time.Duration(seconds) * time.Second
	}
}
