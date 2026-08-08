// Package container - sandbox provider.
//
// This file wires up the sandbox.Manager singleton that gets injected into
// agent_service and session_service. It is intentionally isolated from
// container.go so environment-variable parsing and provider-specific
// configuration stay out of the main provider table.
package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newSandboxManager builds a sandbox.Manager from environment variables.
// Recognised variables:
//
//	WEKNORA_SANDBOX_MODE          "docker" | "local" | "cube" | "e2b" | "disabled" (default "disabled")
//	WEKNORA_SANDBOX_TIMEOUT       per-execute timeout in seconds (default 60)
//	WEKNORA_SANDBOX_DOCKER_IMAGE  custom docker image (docker mode only)
//	WEKNORA_SANDBOX_REDIS_NAMESPACE     Redis key namespace suffix (default derives from
//	                              WEKNORA_REDIS_NAMESPACE, then "weknora")
//	WEKNORA_SANDBOX_CUBE_API_URL      CubeAPI endpoint             (default http://127.0.0.1:33000)
//	WEKNORA_SANDBOX_CUBE_PROXY_URL    CubeProxy endpoint (envd)    (default http://127.0.0.1:80)
//	WEKNORA_SANDBOX_CUBE_SANDBOX_DOMAIN  sandbox routing domain     (default cube.app)
//	WEKNORA_SANDBOX_CUBE_ENVD_PORT     internal envd port           (default 49983)
//	WEKNORA_SANDBOX_CUBE_API_KEY      X-API-Key value              (default empty; Cube auth disabled)
//	WEKNORA_SANDBOX_CUBE_TEMPLATE     template ID                  (default tpl-2b7911a5c3bb419a8745957a)
//	WEKNORA_SANDBOX_CUBE_SANDBOX_TTL  Cube-side sandbox timeout, seconds (default 1800)
//	WEKNORA_SANDBOX_CUBE_HTTP_TIMEOUT single HTTP call timeout, seconds (default 30)
//	WEKNORA_SANDBOX_E2B_API_KEY           X-API-Key for the E2B backend (required for mode=e2b)
//	WEKNORA_SANDBOX_E2B_API_URL           E2B control-plane endpoint    (default https://api.e2b.app)
//	WEKNORA_SANDBOX_E2B_SANDBOX_DOMAIN    sandbox routing domain        (default e2b.app)
//	WEKNORA_SANDBOX_E2B_TEMPLATE          template ID / alias
//	WEKNORA_SANDBOX_E2B_SANDBOX_TTL       E2B-side idle timeout, seconds (default 300)
//	WEKNORA_SANDBOX_E2B_HTTP_TIMEOUT      single HTTP call timeout, seconds (default 30)
func newSandboxManager(
	redisClient *redis.Client,
	sessionRepo interfaces.SessionRepository,
) sandbox.Manager {
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
		return buildCubeManager(ctx, redisClient, sessionRepo)

	case "e2b":
		return buildE2BManager(ctx, redisClient, sessionRepo)

	default:
		logger.Infof(ctx, "Sandbox configured: mode=disabled")
		return sandbox.NewDisabledManager()
	}
}

func buildCubeManager(
	ctx context.Context,
	redisClient *redis.Client,
	sessionRepo interfaces.SessionRepository,
) sandbox.Manager {
	cfg := sandbox.CubeConfigFromEnv()
	client, err := sandbox.NewCubeRemoteClient(cfg)
	if err != nil {
		logger.Warnf(ctx, "Failed to build Cube sandbox client: %v (falling back to disabled)", err)
		return sandbox.NewDisabledManager()
	}
	store, storeKind, err := selectSessionBindingStore(redisClient, true)
	if err != nil {
		logger.Errorf(ctx, "Refusing to start Cube sandbox: %v", err)
		return sandbox.NewDisabledManager()
	}
	m, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config:  cfg,
		Client:  client,
		Store:   store,
		Checker: sessionExistenceCheckerFor(sessionRepo),
	})
	if err != nil {
		logger.Warnf(ctx, "Failed to initialize Cube sandbox: %v (falling back to disabled)", err)
		return sandbox.NewDisabledManager()
	}
	logger.Infof(ctx,
		"Sandbox configured: mode=cube api=%s proxy=%s domain=%s template=%s binding=%s",
		cfg.CubeAPIURL, cfg.CubeProxyURL, cfg.CubeSandboxDomain, cfg.CubeTemplate, storeKind,
	)
	return m
}

func buildE2BManager(
	ctx context.Context,
	redisClient *redis.Client,
	sessionRepo interfaces.SessionRepository,
) sandbox.Manager {
	cfg := sandbox.E2BConfigFromEnv()
	client, err := sandbox.NewE2BRemoteClient(cfg)
	if err != nil {
		logger.Warnf(ctx, "Failed to build E2B sandbox client: %v (falling back to disabled)", err)
		return sandbox.NewDisabledManager()
	}
	store, storeKind, err := selectSessionBindingStore(redisClient, true)
	if err != nil {
		logger.Errorf(ctx, "Refusing to start E2B sandbox: %v", err)
		return sandbox.NewDisabledManager()
	}
	m, err := sandbox.NewSessionBoundManager(sandbox.SessionBoundManagerConfig{
		Config:  cfg,
		Client:  client,
		Store:   store,
		Checker: sessionExistenceCheckerFor(sessionRepo),
	})
	if err != nil {
		logger.Warnf(ctx, "Failed to initialize E2B sandbox: %v (falling back to disabled)", err)
		return sandbox.NewDisabledManager()
	}
	logger.Infof(ctx,
		"Sandbox configured: mode=e2b api=%s domain=%s template=%s ttl=%s binding=%s",
		cfg.E2BAPIURL, cfg.E2BSandboxDomain, cfg.E2BTemplate, cfg.E2BSandboxTTL, storeKind,
	)
	return m
}

func selectSessionBindingStore(
	redisClient *redis.Client,
	requireRedis bool,
) (sandbox.SessionSandboxBindingStore, string, error) {
	namespace := strings.TrimSpace(os.Getenv("WEKNORA_SANDBOX_REDIS_NAMESPACE"))
	if namespace == "" {
		namespace = strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
	}
	if namespace == "" {
		namespace = "weknora"
	}
	if redisClient != nil {
		store, err := sandbox.NewRedisSessionSandboxBindingStore(redisClient, namespace)
		if err != nil {
			return nil, "", fmt.Errorf("build redis binding store: %w", err)
		}
		return store, "redis", nil
	}
	if requireRedis && !allowMemorySandboxBinding() {
		return nil, "", errors.New(
			"remote sandbox modes (cube/e2b) require Redis for session binding; " +
				"set WEKNORA_SANDBOX_ALLOW_MEMORY_BINDING=true only for single-instance dev",
		)
	}
	logger.Warnf(context.Background(),
		"[sandbox] No Redis configured, using in-memory binding store (single-instance)")
	return sandbox.NewMemorySessionSandboxBindingStore(), "memory", nil
}

func allowMemorySandboxBinding() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("WEKNORA_SANDBOX_ALLOW_MEMORY_BINDING")), "true")
}

// sessionExistenceLookup is the narrow slice of SessionRepository the
// session existence checker actually needs. Declaring it here (rather than
// depending on interfaces.SessionRepository) keeps the checker easy to test
// and lets the container inject a nil repository in Lite mode without
// dragging the whole database contract along.
type sessionExistenceLookup interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
}

// sessionExistenceCheckerFor returns a SessionExistenceChecker backed by the
// tenant session repository. When the repository is unavailable (Lite mode
// without a database) the returned checker is permissive so single-process
// deployments still work; multi-instance production paths always resolve a
// real repository because the container refuses to boot without one.
func sessionExistenceCheckerFor(
	lookup sessionExistenceLookup,
) sandbox.SessionExistenceChecker {
	if lookup == nil {
		return sandbox.PermissiveSessionExistenceChecker{}
	}
	return &repositorySessionExistenceChecker{lookup: lookup}
}

// repositorySessionExistenceChecker adapts SessionRepository.GetByID onto the
// SessionExistenceChecker contract. gorm.ErrRecordNotFound → false, other
// errors propagate so the lifecycle coordinator preserves bindings under
// transient database failures.
type repositorySessionExistenceChecker struct {
	lookup sessionExistenceLookup
}

func (c *repositorySessionExistenceChecker) SessionExists(
	ctx context.Context,
	key sandbox.SessionSandboxKey,
) (bool, error) {
	if c == nil || c.lookup == nil {
		return true, nil
	}
	session, err := c.lookup.GetByID(ctx, key.TenantID, key.SessionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, apperrors.ErrSessionNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("session existence check: %w", err)
	}
	return session != nil, nil
}

// buildGlobalSandboxConfig returns the process-wide *sandbox.Config that
// per-tenant overrides are merged onto.
func buildGlobalSandboxConfig() *sandbox.Config {
	return sandbox.DeploymentConfig()
}

// newSandboxConfigDefaults exposes the deployment's inheritable sandbox
// defaults (secret-free) so the settings API can show what a workspace without
// its own overrides actually runs on.
func newSandboxConfigDefaults() *sandbox.ConfigDefaults {
	return sandbox.DescribeDefaults(buildGlobalSandboxConfig())
}

// newTenantSandboxResolver wires the per-tenant resolver. The process-wide
// manager remains the fallback for tenants that configured nothing, so
// existing deployments are unaffected.
func newTenantSandboxResolver(
	defaultManager sandbox.Manager,
	loader sandbox.TenantSandboxConfigLoader,
	redisClient *redis.Client,
	sessionRepo interfaces.SessionRepository,
) sandbox.TenantSandboxResolver {
	ctx := context.Background()

	// Tenants may configure cube/e2b regardless of the global mode, so the
	// resolver needs the same Redis-backed binding guarantees the global
	// remote path demands. Without them per-tenant config stays disabled and
	// every workspace keeps using the process-wide manager.
	store, storeKind, err := selectSessionBindingStore(redisClient, true)
	if err != nil {
		logger.Warnf(ctx,
			"Per-tenant sandbox config disabled: %v", err)
		return nil
	}
	resolver, err := sandbox.NewTenantSandboxResolver(sandbox.TenantSandboxResolverDeps{
		GlobalConfig:    buildGlobalSandboxConfig(),
		DefaultManager:  defaultManager,
		Loader:          loader,
		Store:           store,
		Checker:         sessionExistenceCheckerFor(sessionRepo),
		SharedTransport: sandbox.NewGuardedTransport(),
	})
	if err != nil {
		logger.Warnf(ctx,
			"Failed to initialize tenant sandbox resolver: %v "+
				"(per-tenant sandbox config disabled)", err)
		return nil
	}
	logger.Infof(ctx, "Tenant sandbox resolver configured: binding=%s", storeKind)
	return resolver
}
