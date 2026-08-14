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
	// ① Workspace kill switch — independent of resolver availability.
	if policy != nil && tenantID != 0 {
		disabled, err := policy.WorkspaceScriptsDisabled(ctx, tenantID)
		if err != nil {
			logger.Warnf(ctx,
				"[sandbox] failed to read workspace sandbox policy for %d: %v",
				tenantID, err)
		} else if disabled {
			return sandbox.NewDisabledManager(), nil
		}
	}

	// ② No named workspace config means sandbox execution is disabled. There is
	// no deployment-level provider fallback anymore.
	if configID == "" || configID == types.SandboxConfigIDGlobalDefault {
		return sandbox.NewDisabledManager(), nil
	}

	// ③ Named config: must not silently fall back to another backend.
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
