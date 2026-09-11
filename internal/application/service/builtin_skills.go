package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// ListBuiltinSkills shares the exact metadata-only resolution used by the
// runtime. It does not create definitions, installations, or sandboxes.
func (s *TenantSkillService) ListBuiltinSkills(ctx context.Context, tenantID uint64, configID string) []builtin.Entry {
	return builtin.CompatibleEntries(s.GetBuiltinSkillsManifest(ctx, tenantID, configID))
}

// GetBuiltinSkillsManifest preserves the distinction between an empty published
// manifest and unavailable metadata, for the sandbox settings summary.
func (s *TenantSkillService) GetBuiltinSkillsManifest(
	ctx context.Context,
	tenantID uint64,
	configID string,
) *types.BuiltinSkillsManifest {
	if s.sandboxes == nil || configID == "" {
		return nil
	}
	mgr, err := s.sandboxes.Resolve(ctx, tenantID, configID)
	if err != nil || mgr == nil {
		return nil
	}
	reader, ok := mgr.(sandbox.SessionBuiltinSkillsReader)
	if !ok {
		return nil
	}
	manifest, err := reader.BuiltinSkills(ctx, "")
	if err != nil {
		return nil
	}
	return manifest
}

// Snapshot metadata comes from the build sandbox, never the mutable base tag.
func builtinSkillsForSnapshot(ctx context.Context, mgr sandbox.Manager, sessionID string) *types.BuiltinSkillsManifest {
	if reader, ok := mgr.(sandbox.SessionBuiltinSkillsReader); ok {
		manifest, err := reader.BuiltinSkills(ctx, sessionID)
		if err == nil {
			return manifest
		}
	}
	return nil
}

// ListSessionSkillResources keeps the chat picker on the same pinned config
// and preinstalled image as the next turn. Ownership is checked before lookup.
func (s *TenantSkillService) ListSessionSkillResources(
	ctx context.Context,
	tenantID uint64,
	sessionID, configID string,
) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error) {
	if s.sessions == nil {
		return nil, nil, fmt.Errorf("session service unavailable")
	}
	if _, err := s.sessions.GetOwnedSession(ctx, sessionID); err != nil {
		return nil, nil, err
	}
	reader, ok := s.sessions.(interface {
		SessionSkillResources(
			context.Context, uint64, string, string,
		) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error)
	})
	if !ok {
		return nil, nil, fmt.Errorf("session skills unavailable")
	}
	return reader.SessionSkillResources(ctx, tenantID, sessionID, configID)
}

func (s *sessionService) SessionSkillResources(
	ctx context.Context,
	tenantID uint64,
	sessionID, configID string,
) ([]*types.TenantSkillEntity, *types.BuiltinSkillsManifest, error) {
	pinned, err := sandboxConfigForExistingSandbox(ctx, s.sandboxPinner, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if pinned != "" {
		configID = pinned
	}
	if configID == "" {
		return nil, nil, nil
	}
	rows := effectiveTenantSkills(ctx, s.sandboxConfigRepo, s.tenantSkillRepo, tenantID, configID)
	if s.sandboxResolver == nil {
		return rows, nil, nil
	}
	mgr, err := s.sandboxResolver.Resolve(ctx, tenantID, configID)
	if err != nil || mgr == nil {
		return rows, nil, nil
	}
	// This uses provider metadata only; do not reconcileRuntimeSkills here,
	// since that path executes a command and can start/resume the sandbox.
	if reader, ok := mgr.(interface {
		BuiltinSkillsForRun(context.Context, string) (*types.BuiltinSkillsManifest, error)
	}); ok {
		manifest, _ := reader.BuiltinSkillsForRun(ctx, sessionID)
		return rows, manifest, nil
	}
	return rows, nil, nil
}

// SessionBrowserAvailable follows the live image, not the next-turn rollout.
// Reading capabilities must never create, resume, replace or pin a sandbox.
func (s *sessionService) SessionBrowserAvailable(
	ctx context.Context,
	tenantID uint64,
	sessionID,
	configID string,
) (bool, error) {
	pinned, err := sandboxConfigForExistingSandbox(ctx, s.sandboxPinner, sessionID)
	if err != nil {
		return false, err
	}
	if pinned != "" {
		configID = pinned
	}
	if configID == "" || s.sandboxResolver == nil {
		return false, nil
	}
	mgr, err := resolveTenantSandboxForConfig(ctx, s.sandboxResolver, s.sandboxMgr, tenantID, configID, s.sandboxPolicy)
	if err != nil || mgr == nil {
		return false, err
	}
	rows := effectiveTenantSkills(ctx, s.sandboxConfigRepo, s.tenantSkillRepo, tenantID, configID)
	return sessionBrowserAvailable(ctx, mgr, sessionID, rows)
}

func sessionBrowserAvailable(
	ctx context.Context,
	mgr sandbox.Manager,
	sessionID string,
	rows []*types.TenantSkillEntity,
) (bool, error) {
	live, ok := mgr.(sandbox.SessionLiveShellExecutor)
	if !ok {
		return false, nil
	}
	if reader, ok := mgr.(sandbox.SessionBuiltinSkillsReader); ok {
		manifest, err := reader.BuiltinSkills(ctx, sessionID)
		if err != nil {
			return false, err
		}
		for _, entry := range builtin.CompatibleEntries(manifest) {
			if entry.Name == "browser" {
				return true, nil
			}
		}
	}
	// Legacy/custom controllers are not part of the builtin declaration. A
	// fixed file probe can inspect a running session without starting a browser.
	result, err := live.ExecLiveSessionCommand(ctx, sessionID,
		"if [ -x /opt/weknora/tenant/skills/browser/.venv/bin/python ] && "+
			"[ -f /opt/weknora/tenant/skills/browser/scripts/browser.py ]; then printf yes; else printf no; fi",
		5*time.Second,
	)
	if err == nil && result != nil && result.ExitCode == 0 {
		return strings.TrimSpace(result.Stdout) == "yes", nil
	}
	if err != nil && !errors.Is(err, sandbox.ErrNoLiveSessionSandbox) && !errors.Is(err, sandbox.ErrSandboxPaused) {
		return false, err
	}
	// Paused/uncreated custom environments cannot be inspected without waking
	// them. Keep the entry for a registered browser so it can be resumed.
	for _, row := range rows {
		if row != nil && row.Name == "browser" && row.Status == types.SkillStatusReady && row.Enabled {
			return true, nil
		}
	}
	return false, nil
}
