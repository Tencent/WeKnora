// Package sandbox: per-config sandbox manager resolution.
//
// A manager is built for one (tenant, sandbox config) pair — a workspace holds
// several named configs and each agent selects one. Managers are built per
// request and deliberately NOT cached, mirroring modelService, which rebuilds
// model clients on every call (see internal/application/service/model.go).
//
// Caching was considered and rejected after checking what SessionBoundManager
// actually holds:
//
//   - remoteSessionLifecycle has no mutex; create/recover serialization lives
//     entirely in the shared binding store (Redis SET NX, or the in-memory
//     store's own lock), and that store is injected, so it stays a singleton
//     either way.
//   - SessionBoundManager.mu guards only the Cleanup idempotency flag.
//   - No handles are held across requests: Execute re-Connects every time.
//
// That left the construction-time Health probe as the only real cost, which
// SkipHealthProbe removes. Connection reuse is preserved by sharing one
// http.Transport across tenants (Cube additionally routes its data plane
// through SandboxGatewayTransportPool; see gateway_transport.go). The upshot: no cache, no
// eviction, no invalidation plumbing, and a config change takes effect on the
// next request.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ErrSandboxConfigNotFound means the referenced config is gone (deleted, or
// never existed). Callers must surface it rather than degrade to the default
// backend: running an agent's scripts on a backend it was not pointed at is a
// silent, security-relevant substitution.
var ErrSandboxConfigNotFound = errors.New("sandbox: config not found")

// ErrSandboxConfigCordoned means the config's identity fields are mid-change.
// It is transient by construction (the cordon is a short lease), so callers
// should surface it as retriable.
var ErrSandboxConfigCordoned = errors.New("sandbox: config is being updated")

// ResolvedTenantSandboxConfig is what the loader reports about one config.
type ResolvedTenantSandboxConfig struct {
	// Config is nil when configID was empty (use the global default).
	Config *types.TenantSandboxConfig

	// Found is false when a non-empty configID matched no row.
	Found bool

	// Cordoned is true while the config's credentials are being replaced.
	Cordoned bool
}

// TenantSandboxConfigLoader fetches one stored sandbox config.
// Implemented in the application layer so this package stays free of
// repository dependencies.
type TenantSandboxConfigLoader interface {
	Load(ctx context.Context, tenantID uint64, configID string) (ResolvedTenantSandboxConfig, error)
}

// TenantSandboxResolver produces the Manager for a (tenant, config) pair.
type TenantSandboxResolver interface {
	// Resolve builds the manager for configID. An empty configID selects the
	// deployment-wide default manager.
	Resolve(ctx context.Context, tenantID uint64, configID string) (Manager, error)
}

// TenantSandboxResolverDeps bundles the resolver's wiring.
type TenantSandboxResolverDeps struct {
	// GlobalConfig supplies only built-in runtime tuning defaults.
	GlobalConfig *Config

	// DefaultManager serves tenants without their own configuration, which
	// preserves the pre-feature behaviour for existing deployments.
	DefaultManager Manager

	Loader  TenantSandboxConfigLoader
	Store   SessionSandboxBindingStore
	Checker SessionExistenceChecker

	// SharedTransport is reused by every tenant's HTTP client. Optional; a
	// guarded transport is installed when nil.
	SharedTransport *http.Transport
}

type tenantSandboxResolver struct {
	deps             TenantSandboxResolverDeps
	transport        *http.Transport
	privateTransport *http.Transport

	// gatewayTransports must outlive the per-request clients it serves, which is
	// the whole point of holding it here rather than building it per Resolve.
	gatewayTransports        *SandboxGatewayTransportPool
	privateGatewayTransports *SandboxGatewayTransportPool
}

// NewTenantSandboxResolver validates the wiring and returns a resolver.
func NewTenantSandboxResolver(deps TenantSandboxResolverDeps) (TenantSandboxResolver, error) {
	if deps.GlobalConfig == nil {
		return nil, errors.New("sandbox: tenant resolver requires a global config")
	}
	if deps.Loader == nil {
		return nil, errors.New("sandbox: tenant resolver requires a config loader")
	}
	if deps.Store == nil {
		return nil, errors.New("sandbox: tenant resolver requires a binding store")
	}
	if deps.Checker == nil {
		return nil, errors.New("sandbox: tenant resolver requires a session checker")
	}
	transport := deps.SharedTransport
	if transport == nil {
		transport = NewGuardedTransport()
	}
	return &tenantSandboxResolver{
		deps:                     deps,
		transport:                transport,
		privateTransport:         NewGuardedTransportWithPolicy(OutboundURLPolicy{AllowPrivate: true}),
		gatewayTransports:        NewSandboxGatewayTransportPool(transport),
		privateGatewayTransports: NewSandboxGatewayTransportPoolWithPolicy(nil, OutboundURLPolicy{AllowPrivate: true}),
	}, nil
}

// NewGuardedTransport returns an http.Transport whose dialer refuses addresses
// the outbound policy forbids. This closes the DNS-rebinding window that
// save-time URL validation cannot cover.
func NewGuardedTransport() *http.Transport {
	return NewGuardedTransportWithPolicy(DefaultOutboundURLPolicy())
}

func NewGuardedTransportWithPolicy(policy OutboundURLPolicy) *http.Transport {
	return &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   SafeDialControlForPolicy(policy),
		}).DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
	}
}

// Resolve builds the manager from the selected config's current settings.
func (r *tenantSandboxResolver) Resolve(
	ctx context.Context,
	tenantID uint64,
	configID string,
) (Manager, error) {
	if strings.TrimSpace(configID) == "" ||
		configID == types.SandboxConfigIDGlobalDefault {
		return NewDisabledManager(), nil
	}

	resolved, err := r.deps.Loader.Load(ctx, tenantID, configID)
	if err != nil {
		return nil, fmt.Errorf(
			"sandbox: load workspace %d config %q: %w", tenantID, configID, err)
	}
	if !resolved.Found {
		return nil, fmt.Errorf("%w: %s", ErrSandboxConfigNotFound, configID)
	}
	if resolved.Cordoned {
		return nil, fmt.Errorf("%w: %s", ErrSandboxConfigCordoned, configID)
	}

	effective, err := ResolveEffectiveConfig(resolved.Config, r.deps.GlobalConfig)
	if err != nil {
		return nil, err
	}

	switch effective.Type {
	case SandboxTypeDisabled:
		return NewDisabledManager(), nil
	case SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker:
		client, err := r.buildClient(effective)
		if err != nil {
			return nil, err
		}
		return NewSessionBoundManager(SessionBoundManagerConfig{
			Config:          effective,
			Client:          client,
			Store:           r.deps.Store,
			Checker:         r.deps.Checker,
			SkipHealthProbe: true,
			ConfigID:        configID,
		})
	default:
		return NewDisabledManager(), nil
	}
}

// buildClient constructs the provider adapter for this tenant, injecting the
// shared transport so pooling survives per-request construction.
func (r *tenantSandboxResolver) buildClient(cfg *Config) (RemoteSandboxClient, error) {
	switch cfg.Type {
	case SandboxTypeCube:
		if cfg.AllowPrivateEndpoints {
			return NewCubeRemoteClientWithPool(cfg, r.privateGatewayTransports)
		}
		return NewCubeRemoteClientWithPool(cfg, r.gatewayTransports)
	case SandboxTypeE2B:
		// The gateway pool is used even without a gateway URL: it then keeps
		// every request on the shared control transport, which is exactly what
		// a plain E2B Cloud config wants.
		if cfg.AllowPrivateEndpoints {
			return NewE2BRemoteClientWithPool(cfg, r.privateGatewayTransports)
		}
		return NewE2BRemoteClientWithPool(cfg, r.gatewayTransports)
	case SandboxTypeDocker:
		// The docker client keeps its own pooled connection per daemon
		// endpoint rather than using the transports above: the Engine API is
		// reached over a unix socket as often as over TCP. It installs the
		// same guarded dialer for TCP endpoints (see newDockerEngineClient).
		return NewDockerRemoteClient(cfg)
	default:
		return nil, fmt.Errorf("sandbox: provider %q has no remote client", cfg.Type)
	}
}

// NewRemoteClientForCheck builds a throwaway client for connectivity probes.
// It is never cached and never handed to a manager.
//
// Both providers get a guarded transport. The endpoints being probed come
// straight off an admin's unsaved form, so save-time ValidateOutboundURL has
// not necessarily run against the address this client will actually dial, and
// even when it has, a hostname can resolve to a public address during
// validation and to 169.254.169.254 at dial time.
func NewRemoteClientForCheck(cfg *Config) (RemoteSandboxClient, error) {
	if cfg == nil {
		return nil, errors.New("sandbox: config is required")
	}
	switch cfg.Type {
	case SandboxTypeCube:
		return NewCubeRemoteClientWithPool(cfg, NewSandboxGatewayTransportPoolWithPolicy(nil,
			OutboundURLPolicy{AllowPrivate: cfg.AllowPrivateEndpoints}))
	case SandboxTypeE2B:
		// Probing through the gateway pool is what makes the check meaningful
		// for a self-hosted control plane: it exercises the same data-plane
		// routing the resolved manager will use.
		return NewE2BRemoteClientWithPool(cfg, NewSandboxGatewayTransportPoolWithPolicy(nil,
			OutboundURLPolicy{AllowPrivate: cfg.AllowPrivateEndpoints}))
	case SandboxTypeDocker:
		return NewDockerRemoteClientForCheck(cfg)
	default:
		return nil, fmt.Errorf("sandbox: provider %q cannot be probed", cfg.Type)
	}
}
