package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// skillImageConfigReader reads the sandbox config an agent selected. Narrowed
// to the one method this derivation needs so a chat turn cannot reach the
// config write surface.
type skillImageConfigReader interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
}

// installedSkillLister lists the skills installed onto one sandbox config.
type installedSkillLister interface {
	ListSkillsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillEntity, error)
}

// skillsForRun returns the sandbox config this run's sandbox boots and the
// installed skills that config offers.
//
// The config is not simply the agent's. A session's remote sandbox is
// long-lived and stays on the config it was pinned to, so an agent re-pointed
// mid-conversation still runs on the pinned image; deriving skills from the
// agent's current choice would announce skills the running image does not
// carry, which is exactly the failure this derivation exists to prevent.
//
// A pin we cannot read leaves us unable to say which image boots, and the
// agent's config is precisely the wrong guess in the case that matters, so
// that path offers nothing.
func skillsForRun(
	ctx context.Context,
	pinner *SessionSandboxPinner,
	configs skillImageConfigReader,
	skills installedSkillLister,
	tenantID uint64,
	sessionID string,
	agentConfigID string,
) (string, []*types.TenantSkillEntity) {
	configID := agentConfigID
	pinned, err := sandboxConfigForExistingSandbox(ctx, pinner, sessionID)
	if err != nil {
		logger.Warnf(ctx, "[skill] read the pinned sandbox config of session %s failed: %v",
			sessionID, err)
		return "", nil
	}
	if !pinned.IsZero() {
		configID = pinned.ConfigID
		// A config is read in the workspace that owns it, which for a shared
		// agent is the lending one. tenantID is that same workspace on every
		// path today (the chat turn runs there); taking it from the pin is
		// what keeps the two from drifting apart again.
		tenantID = pinned.TenantOr(tenantID)
	}
	return configID, effectiveTenantSkills(ctx, configs, skills, tenantID, configID)
}

// effectiveTenantSkills returns the installed skills this run can actually
// invoke, which is the only set worth telling a model about.
//
// Two conditions have to hold, and both are about the same thing - whether the
// files exist where the call would look for them:
//
//   - The stored snapshot must be the image sessions boot. When the backend
//     cannot snapshot, or the credentials no longer resolve the recorded
//     snapshot, sessions boot the BASE template, which carries none of these
//     skills. Announcing them then costs the model several turns on calls that
//     can only fail, so ZERO skills are returned - not a degraded subset.
//   - The row must be enabled and the image must serve a version of it: the
//     row itself when ready, or the version kept while a newer install is in
//     flight or has failed (see TenantSkillEntity.ServedView). Anything else is
//     either not in the image yet, not in it any more, or deliberately hidden
//     by an administrator.
//
// Every failure returns nothing: a chat turn must not fail because a skill
// lookup did, and a workspace with no installed skills is the common case.
func effectiveTenantSkills(
	ctx context.Context,
	configs skillImageConfigReader,
	skills installedSkillLister,
	tenantID uint64,
	configID string,
) []*types.TenantSkillEntity {
	if configs == nil || skills == nil || configID == "" || tenantID == 0 {
		return nil
	}

	cfgEntity, err := configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] load sandbox config %s for skill injection failed: %v",
			configID, err)
		return nil
	}
	if cfgEntity == nil || cfgEntity.Config == nil {
		return nil
	}
	if !sandbox.SkillImageActive(cfgEntity.Config) {
		if image := cfgEntity.Config.SkillImage; image != nil && image.SnapshotID != "" {
			logger.Warnf(ctx,
				"[skill] sandbox config %s boots its base template rather than skill image %s; "+
					"no installed skills are offered to the agent",
				configID, image.SnapshotID)
		}
		return nil
	}

	rows, err := skills.ListSkillsByConfig(ctx, tenantID, configID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list skills of sandbox config %s failed: %v", configID, err)
		return nil
	}
	usable := make([]*types.TenantSkillEntity, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Enabled {
			continue
		}
		if served := row.ServedView(); served != nil {
			usable = append(usable, served)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	return usable
}

// hostSkillsForRun is skillsForRun on Lite: skills installed on this machine,
// offered only while their files are actually on disk.
func hostSkillsForRun(
	ctx context.Context, skills installedSkillLister, tree HostSkillTree, tenantID uint64,
) (string, []*types.TenantSkillEntity) {
	if skills == nil || tree == nil || tenantID == 0 {
		return sandbox.HostSkillTargetID, nil
	}
	rows, err := skills.ListSkillsByConfig(ctx, tenantID, sandbox.HostSkillTargetID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list local skills failed: %v", err)
		return sandbox.HostSkillTargetID, nil
	}
	usable := make([]*types.TenantSkillEntity, 0, len(rows))
	for _, row := range rows {
		if row == nil || !row.Enabled || !tree.Installed(row.Name) {
			continue
		}
		if served := row.ServedView(); served != nil {
			usable = append(usable, served)
		}
	}
	if len(usable) == 0 {
		return sandbox.HostSkillTargetID, nil
	}
	return sandbox.HostSkillTargetID, usable
}
