// Package sandbox: configuration defaults and deployment baseline.
//
// applyCubeDefaults / applyE2BDefaults fill provider fields that adapters rely
// on being non-zero. DeploymentConfig reads WEKNORA_SANDBOX_* for the
// process-wide baseline tenant overrides merge onto.
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
	return cfg
}

func e2bConfigFromEnv() *Config {
	cfg := DefaultConfig()
	cfg.Type = SandboxTypeE2B
	cfg.FallbackEnabled = false
	cfg.E2BSandboxTTL = DefaultE2BSandboxTTL
	cfg.E2BHTTPTimeout = DefaultE2BHTTPTimeout

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
	return cfg
}

// applyCubeDefaults mutates cfg in-place so downstream code can rely on the
// Cube-specific fields being non-zero. Safe to call multiple times.
func applyCubeDefaults(cfg *Config) {
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
	if cfg.CubeSandboxTTL <= 0 {
		cfg.CubeSandboxTTL = DefaultCubeSandboxTTL
	}
	if cfg.CubeHTTPTimeout <= 0 {
		cfg.CubeHTTPTimeout = DefaultCubeHTTPTimeout
	}
}

// applyE2BDefaults mutates cfg in-place so downstream code can rely on the
// E2B-specific timeout fields being non-zero.
func applyE2BDefaults(cfg *Config) {
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
