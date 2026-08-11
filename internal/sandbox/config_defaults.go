// Package sandbox: description of the process-wide defaults a tenant inherits.
//
// A tenant that has not overridden a field runs on the deployment's global
// WEKNORA_SANDBOX_* configuration. The settings UI has to show what is actually
// in effect, otherwise an empty override set looks indistinguishable from
// "disabled" — and saving that impression would silently disable the sandbox.
//
// Secrets are deliberately never included here; only whether one is present.
package sandbox

// ProviderDefaults is the inheritable, non-secret configuration of one backend.
type ProviderDefaults struct {
	APIURL           string `json:"api_url,omitempty"`
	ProxyURL         string `json:"proxy_url,omitempty"`
	SandboxDomain    string `json:"sandbox_domain,omitempty"`
	TemplateID       string `json:"template_id,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	HTTPTimeoutSec   int    `json:"http_timeout_sec,omitempty"`
	SandboxTTLSec    int    `json:"sandbox_ttl_seconds,omitempty"`
}

// ConfigDefaults mirrors types.TenantSandboxConfig's shape so the UI can render
// inherited values as placeholders next to the tenant's own overrides.
type ConfigDefaults struct {
	// SandboxType is the backend a tenant gets when it sets no override.
	SandboxType       string            `json:"sandbox_type"`
	DefaultTimeoutSec int               `json:"default_timeout_sec,omitempty"`
	DockerImage       string            `json:"docker_image,omitempty"`
	Cube              *ProviderDefaults `json:"cube,omitempty"`
	E2B               *ProviderDefaults `json:"e2b,omitempty"`
}

// DescribeDefaults projects a resolved global Config into its inheritable,
// secret-free description.
func DescribeDefaults(cfg *Config) *ConfigDefaults {
	if cfg == nil {
		return &ConfigDefaults{SandboxType: string(SandboxTypeDisabled)}
	}
	defaults := &ConfigDefaults{
		SandboxType:       string(cfg.Type),
		DefaultTimeoutSec: int(cfg.DefaultTimeout.Seconds()),
		DockerImage:       cfg.DockerImage,
	}
	// Describe both remote backends regardless of the active mode: an admin
	// switching a workspace to the other one still needs to see what it would
	// inherit.
	defaults.Cube = &ProviderDefaults{
		APIURL:           cfg.CubeAPIURL,
		ProxyURL:         cfg.CubeProxyURL,
		SandboxDomain:    cfg.CubeSandboxDomain,
		TemplateID:       cfg.CubeTemplate,
		APIKeyConfigured: cfg.CubeAPIKey != "",
		HTTPTimeoutSec:   int(cfg.CubeHTTPTimeout.Seconds()),
		SandboxTTLSec:    int(cfg.CubeSandboxTTL.Seconds()),
	}
	defaults.E2B = &ProviderDefaults{
		APIURL:           cfg.E2BAPIURL,
		SandboxDomain:    cfg.E2BSandboxDomain,
		TemplateID:       cfg.E2BTemplate,
		APIKeyConfigured: cfg.E2BAPIKey != "",
		HTTPTimeoutSec:   int(cfg.E2BHTTPTimeout.Seconds()),
		SandboxTTLSec:    int(cfg.E2BSandboxTTL.Seconds()),
	}
	return defaults
}
