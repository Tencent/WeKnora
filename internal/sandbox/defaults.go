// Package sandbox: configuration defaults and deployment baseline.
//
// DeploymentConfig reads WEKNORA_SANDBOX_* into the process-wide baseline. That
// baseline backs agents which selected no named config; it is NOT merged into
// named configs, so the built-in endpoint constants below stay on this path only
// (see tenant_config.go for why).
//
// The provider defaults come in two flavours. Deployment defaults fill endpoints
// and templates and exist so a developer can run `WEKNORA_SANDBOX_MODE=cube` with
// an empty .env. Runtime defaults fill TTLs and HTTP timeouts and are safe
// anywhere, because those have meaningful built-in values while an endpoint does
// not.
package sandbox

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// DeploymentConfig returns the process-wide *Config built from WEKNORA_SANDBOX_*.
func DeploymentConfig() *Config {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("WEKNORA_SANDBOX_MODE")))
	switch mode {
	case "cube":
		return cubeConfigFromEnv()
	case "e2b":
		return e2bConfigFromEnv()
	case "docker":
		cfg := DefaultConfig()
		cfg.Type = SandboxTypeDocker
		if v := os.Getenv("WEKNORA_SANDBOX_DOCKER_IMAGE"); v != "" {
			cfg.DockerImage = v
		}
		return cfg
	case "local":
		cfg := DefaultConfig()
		cfg.Type = SandboxTypeLocal
		return cfg
	default:
		cfg := DefaultConfig()
		cfg.Type = SandboxTypeDisabled
		return cfg
	}
}

// CubeConfigFromEnv assembles a Cube *Config from WEKNORA_SANDBOX_CUBE_* env vars.
func CubeConfigFromEnv() *Config {
	return cubeConfigFromEnv()
}

// E2BConfigFromEnv assembles an E2B *Config from WEKNORA_SANDBOX_E2B_* env vars.
func E2BConfigFromEnv() *Config {
	return e2bConfigFromEnv()
}

func cubeConfigFromEnv() *Config {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeCube
	cfg.FallbackEnabled = false

	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_API_URL"); v != "" {
		cfg.CubeAPIURL = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_PROXY_URL"); v != "" {
		cfg.CubeProxyURL = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_SANDBOX_DOMAIN"); v != "" {
		cfg.CubeSandboxDomain = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_API_KEY"); v != "" {
		cfg.CubeAPIKey = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_TEMPLATE"); v != "" {
		cfg.CubeTemplate = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_SANDBOX_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CubeSandboxTTL = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_HTTP_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CubeHTTPTimeout = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("WEKNORA_SANDBOX_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DefaultTimeout = time.Duration(n) * time.Second
		}
	}
	// After the environment so an explicit value always wins.
	applyCubeDeploymentDefaults(cfg)
	applyCubeRuntimeDefaults(cfg)
	return cfg
}

func e2bConfigFromEnv() *Config {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.FallbackEnabled = false

	if v := os.Getenv("WEKNORA_SANDBOX_E2B_API_KEY"); v != "" {
		cfg.E2BAPIKey = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_E2B_API_URL"); v != "" {
		cfg.E2BAPIURL = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_E2B_SANDBOX_DOMAIN"); v != "" {
		cfg.E2BSandboxDomain = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_E2B_TEMPLATE"); v != "" {
		cfg.E2BTemplate = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_E2B_SANDBOX_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.E2BSandboxTTL = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("WEKNORA_SANDBOX_E2B_HTTP_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.E2BHTTPTimeout = time.Duration(n) * time.Second
		}
	}
	if v := os.Getenv("WEKNORA_SANDBOX_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.DefaultTimeout = time.Duration(n) * time.Second
		}
	}
	applyE2BRuntimeDefaults(cfg)
	return cfg
}

// applyCubeDeploymentDefaults fills the Cube endpoint, domain and template with
// the built-in single-node values so `WEKNORA_SANDBOX_MODE=cube` works out of the
// box on a developer machine.
//
// It is reachable from the deployment baseline only. Named configs must not see
// these constants: inheriting them is how a workspace config ends up dialling
// 127.0.0.1 instead of being told which field it forgot.
func applyCubeDeploymentDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.CubeAPIURL == "" {
		cfg.CubeAPIURL = DefaultCubeAPIURL
	}
	if cfg.CubeProxyURL == "" {
		cfg.CubeProxyURL = DefaultCubeProxyURL
	}
	if cfg.CubeSandboxDomain == "" {
		cfg.CubeSandboxDomain = DefaultCubeSandboxDomain
	}
	if cfg.CubeTemplate == "" {
		cfg.CubeTemplate = DefaultCubeTemplate
	}
}

// applyCubeRuntimeDefaults fills the Cube tuning fields downstream code relies
// on being non-zero. Unlike the endpoint defaults these are safe everywhere:
// a TTL or an HTTP timeout has a sane built-in value, an endpoint does not.
// Safe to call multiple times.
func applyCubeRuntimeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.CubeSandboxTTL <= 0 {
		cfg.CubeSandboxTTL = DefaultCubeSandboxTTL
	}
	if cfg.CubeHTTPTimeout <= 0 {
		cfg.CubeHTTPTimeout = DefaultCubeHTTPTimeout
	}
}

// applyE2BRuntimeDefaults is applyCubeRuntimeDefaults for E2B. There is no
// applyE2BDeploymentDefaults: go-e2b resolves its own API base URL and sandbox
// domain, and it ships no template ID that would work for anyone.
func applyE2BRuntimeDefaults(cfg *Config) {
	if cfg == nil {
		return
	}
	if cfg.E2BSandboxTTL <= 0 {
		cfg.E2BSandboxTTL = DefaultE2BSandboxTTL
	}
	if cfg.E2BHTTPTimeout <= 0 {
		cfg.E2BHTTPTimeout = DefaultE2BHTTPTimeout
	}
}
