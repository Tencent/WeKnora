// Package service - per-tenant sandbox resolution helpers.
//
// The sandbox package must not depend on repositories, so the config lookup is
// adapted here and injected as sandbox.TenantSandboxConfigLoader.
package service

import (
	"context"
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

// resolveTenantSandboxForConfig returns the Manager for an explicit config.
//
// Unlike the previous tenant-only helper this does NOT degrade to the default
// manager on error: with several configs per workspace, a silent substitution
// would run scripts on a different backend than the one selected - and then
// artifact collection and sandbox teardown would target the wrong account.
func resolveTenantSandboxForConfig(
	ctx context.Context,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
	tenantID uint64,
	configID string,
) (sandbox.Manager, error) {
	if resolver == nil || tenantID == 0 {
		return fallback, nil
	}
	if configID == "" || configID == types.SandboxConfigIDGlobalDefault {
		return fallback, nil
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
