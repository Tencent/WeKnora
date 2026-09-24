// Package service - per-tenant sandbox resolution helpers.
//
// The sandbox package must not depend on repositories, so the config lookup is
// adapted here and injected as sandbox.TenantSandboxConfigLoader.
package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// tenantSandboxConfigLoader reads one named sandbox config for a workspace.
type tenantSandboxConfigLoader struct {
	repo repository.TenantSandboxConfigRepository
	now  func() time.Time
}

// NewTenantSandboxConfigLoader adapts the config repository onto the sandbox
// package's loader contract.
func NewTenantSandboxConfigLoader(
	repo repository.TenantSandboxConfigRepository,
) sandbox.TenantSandboxConfigLoader {
	return &tenantSandboxConfigLoader{repo: repo, now: time.Now}
}

// Load reports whether the config exists and whether it is currently cordoned.
// A cordon is honoured here - the single choke point every sandbox operation
// passes through - so no new sandbox can be created on credentials that are
// about to be replaced.
func (l *tenantSandboxConfigLoader) Load(
	ctx context.Context,
	tenantID uint64,
	configID string,
) (sandbox.ResolvedTenantSandboxConfig, error) {
	if l.repo == nil {
		return sandbox.ResolvedTenantSandboxConfig{}, nil
	}
	entity, err := l.repo.GetByID(ctx, tenantID, configID)
	if err != nil {
		return sandbox.ResolvedTenantSandboxConfig{}, err
	}
	if entity == nil {
		return sandbox.ResolvedTenantSandboxConfig{Found: false}, nil
	}
	return sandbox.ResolvedTenantSandboxConfig{
		Config:   entity.Config,
		Found:    true,
		Cordoned: entity.IsCordoned(l.now(), types.SandboxCordonLease),
	}, nil
}

// WorkspaceSandboxPolicy reports whether sandbox execution is disabled for the
// entire workspace, including agents bound to any named backend config.
type WorkspaceSandboxPolicy interface {
	WorkspaceScriptsDisabled(ctx context.Context, tenantID uint64) (bool, error)
}

// HostSandboxManager is Lite's OS sandbox. A nil Manager means this process
// is not Lite (or cannot enforce a sandbox). Web binaries always inject nil,
// so empty remote configs resolve to disabled rather than host.
type HostSandboxManager struct {
	Manager sandbox.Manager
}

type resolveOption func(*resolveOptions)

type resolveOptions struct {
	// liteHost is Lite's OS sandbox. Nil on the web binary. Named remote
	// configs never consult it; an empty config on web stays disabled.
	liteHost sandbox.Manager
}

// withLiteHostSandbox opts a resolve into Lite's host backend when the
// session has no named remote config. Web callers pass a nil manager.
func withLiteHostSandbox(m sandbox.Manager) resolveOption {
	return func(o *resolveOptions) { o.liteHost = liteHostSandbox(m) }
}

func liteHostSandbox(m sandbox.Manager) sandbox.Manager {
	if m == nil || m.GetType() != sandbox.SandboxTypeHost {
		return nil
	}
	return m
}

func applyResolveOptions(opts []resolveOption) resolveOptions {
	var o resolveOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

func workspaceScriptsDisabled(ctx context.Context, policy WorkspaceSandboxPolicy, tenantID uint64) bool {
	if policy == nil || tenantID == 0 {
		return false
	}
	disabled, err := policy.WorkspaceScriptsDisabled(ctx, tenantID)
	if err != nil {
		logger.Warnf(ctx,
			"[sandbox] failed to read workspace sandbox policy for %d: %v",
			tenantID, err)
		return false
	}
	return disabled
}

// resolveTenantSandboxForConfig returns the Manager for an explicit config.
//
// Unlike the previous tenant-only helper this does NOT degrade to the default
// manager on error: with several configs per workspace, a silent substitution
// would run scripts on a different backend than the one selected - and then
// artifact collection and sandbox teardown would target the wrong account.
func resolveTenantSandboxForConfig(
	ctx context.Context,
	resolver sandbox.TenantSandboxResolver,
	_ sandbox.Manager,
	tenantID uint64,
	configID string,
	policy WorkspaceSandboxPolicy,
) (sandbox.Manager, error) {
	if workspaceScriptsDisabled(ctx, policy, tenantID) {
		return sandbox.NewDisabledManager(), nil
	}

	// No named workspace config means disabled. Lite host is not selected
	// here: web and Lite share this helper, and an empty config on web must
	// not become host. Lite passes withLiteHostSandbox to
	// resolveSandboxForExecution instead.
	if configID == "" || configID == types.SandboxConfigIDGlobalDefault {
		return sandbox.NewDisabledManager(), nil
	}

	// Named config: must not silently fall back to another backend.
	if tenantID == 0 {
		return nil, fmt.Errorf(
			"sandbox: resolve config %q: missing workspace context", configID)
	}
	if resolver == nil {
		return nil, fmt.Errorf(
			"sandbox: resolve config %q: per-tenant resolver unavailable", configID)
	}
	mgr, err := resolver.Resolve(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx,
			"[sandbox] failed to resolve config %q for workspace %d: %v",
			configID, tenantID, err)
		return nil, err
	}
	return mgr, nil
}
