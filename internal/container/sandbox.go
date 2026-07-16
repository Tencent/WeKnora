// Package container - sandbox provider.
//
// This file wires up the sandbox.Manager singleton that gets injected into
// agent_service and session_service. It is intentionally isolated from
// container.go so the environment-variable parsing and CubeSandbox-specific
// configuration stays out of the main provider table.
package container

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// newSandboxManager builds a sandbox.Manager from environment variables.
// Recognised variables:
//
//	WEKNORA_SANDBOX_MODE          "docker" | "local" | "cube" | "disabled" (default "disabled")
//	WEKNORA_SANDBOX_TIMEOUT       per-execute timeout in seconds (default 60)
//	WEKNORA_SANDBOX_DOCKER_IMAGE  custom docker image (docker mode only)
//	WEKNORA_SANDBOX_CUBE_API_URL      CubeAPI endpoint             (default http://127.0.0.1:33000)
//	WEKNORA_SANDBOX_CUBE_PROXY_URL    CubeProxy endpoint (envd)    (default http://127.0.0.1:80)
//	WEKNORA_SANDBOX_CUBE_SANDBOX_DOMAIN  sandbox routing domain     (default cube.app)
//	WEKNORA_SANDBOX_CUBE_ENVD_PORT     internal envd port           (default 49983)
//	WEKNORA_SANDBOX_CUBE_API_KEY      X-API-Key value              (default empty; Cube auth disabled)
//	WEKNORA_SANDBOX_CUBE_TEMPLATE     template ID                  (default tpl-2b7911a5c3bb419a8745957a)
//	WEKNORA_SANDBOX_CUBE_SANDBOX_TTL  Cube-side sandbox timeout, seconds (default 1800)
//	WEKNORA_SANDBOX_CUBE_IDLE_TTL     idle reap threshold, seconds  (default 1800)
//	WEKNORA_SANDBOX_CUBE_HTTP_TIMEOUT single HTTP call timeout, seconds (default 30)
//
// Behavior on failure:
//   - Docker mode: if docker is unavailable, we transparently fall back to
//     LocalSandbox (matches the historical behavior in agent_service).
//   - Cube mode: SessionBoundManager itself falls back to LocalSandbox when
//     the Cube API is unreachable and FallbackEnabled=true. If both the
//     construction of SessionBoundManager and the local fallback fail, we
//     return NewDisabledManager() so the application keeps booting.
func newSandboxManager() sandbox.Manager {
	ctx := context.Background()

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("WEKNORA_SANDBOX_MODE")))
	if mode == "" {
		mode = "disabled"
	}

	switch mode {
	case "docker":
		dockerImage := os.Getenv("WEKNORA_SANDBOX_DOCKER_IMAGE")
		if dockerImage == "" {
			dockerImage = sandbox.DefaultDockerImage
		}
		m, err := sandbox.NewManagerFromType("docker", true, dockerImage)
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize Docker sandbox, falling back to disabled: %v", err)
			return sandbox.NewDisabledManager()
		}
		logger.Infof(ctx, "Sandbox configured: mode=docker image=%s", dockerImage)
		return m

	case "local":
		m, err := sandbox.NewManagerFromType("local", false, "")
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize local sandbox: %v", err)
			return sandbox.NewDisabledManager()
		}
		logger.Infof(ctx, "Sandbox configured: mode=local")
		return m

	case "cube":
		cfg := buildCubeSandboxConfig()
		client, err := sandbox.NewCubeRemoteClient(cfg)
		if err != nil {
			logger.Warnf(ctx, "Failed to build Cube sandbox client: %v (falling back to disabled)", err)
			return sandbox.NewDisabledManager()
		}
		// TODO: task 8 wires a Redis-backed binding store and a real session
		// existence checker. Until then the process-local memory store keeps
		// single-instance deployments correct; multi-instance deployments
		// must not run this branch in production without Redis.
		m, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
			Config:  cfg,
			Client:  client,
			Store:   sandbox.NewMemorySessionSandboxBindingStore(),
			Checker: sandbox.PermissiveSessionExistenceChecker{},
		})
		if err != nil {
			logger.Warnf(ctx, "Failed to initialize Cube sandbox: %v (falling back to disabled)", err)
			return sandbox.NewDisabledManager()
		}
		logger.Infof(ctx,
			"Sandbox configured: mode=cube api=%s proxy=%s domain=%s template=%s idle_ttl=%s",
			cfg.CubeAPIURL, cfg.CubeProxyURL, cfg.CubeSandboxDomain, cfg.CubeTemplate, cfg.CubeIdleTTL,
		)
		return m

	default:
		logger.Infof(ctx, "Sandbox configured: mode=disabled")
		return sandbox.NewDisabledManager()
	}
}

// buildCubeSandboxConfig assembles a fully-populated *sandbox.Config for the
// Cube backend, applying environment overrides on top of the package
// defaults.
func buildCubeSandboxConfig() *sandbox.Config {
	cfg := sandbox.DefaultConfig()
	cfg.Type = sandbox.SandboxTypeCube
	cfg.FallbackEnabled = true

	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_API_URL"); v != "" {
		cfg.CubeAPIURL = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_PROXY_URL"); v != "" {
		cfg.CubeProxyURL = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_SANDBOX_DOMAIN"); v != "" {
		cfg.CubeSandboxDomain = v
	}
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_ENVD_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CubeEnvdPort = n
		}
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
	if v := os.Getenv("WEKNORA_SANDBOX_CUBE_IDLE_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.CubeIdleTTL = time.Duration(n) * time.Second
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
