package service

import (
	"context"
	_ "embed"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// agentBrowserSkillMD teaches the model the agent-browser CLI that the desktop
// base image ships. It is a skill row synthesized per run rather than an
// installed one: the CLI and its wrapper live in the image's system paths
// (never in the tenant-writable skills tree), so there is nothing to install
// and no per-workspace step to forget.
//
//go:embed builtin_skills/agent-browser/SKILL.md
var agentBrowserSkillMD string

// builtinSkillRows returns the skills the backend itself offers on a sandbox
// config. Today that is agent-browser on desktop configs: DesktopEnabled is the
// switch that says the base template carries Chrome and the wrapper.
//
// The rows never reach the database. They are Ready and Enabled by
// construction, and Builtin tells the skill source not to look for a bundle.
func builtinSkillRows(
	ctx context.Context, cfg *types.TenantSandboxConfig, tenantID uint64, configID string,
) []*types.TenantSkillEntity {
	if cfg == nil || !cfg.DesktopEnabled {
		return nil
	}
	parsed, err := skills.ParseSkillFile(agentBrowserSkillMD)
	if err != nil || !sandbox.IsValidSkillName(parsed.Name) {
		logger.Warnf(ctx, "[skill] builtin agent-browser skill is unusable: %v", err)
		return nil
	}
	return []*types.TenantSkillEntity{{
		ID:              "builtin:" + parsed.Name,
		TenantID:        tenantID,
		SandboxConfigID: configID,
		Name:            parsed.Name,
		Description:     parsed.Description,
		Instructions:    parsed.Instructions,
		Enabled:         true,
		Status:          types.SkillStatusReady,
		Builtin:         true,
	}}
}

// mergeBuiltinSkills appends builtin rows after the installed ones. An
// installed skill of the same name wins: an administrator who installed their
// own agent-browser skill meant to replace this one.
func mergeBuiltinSkills(installed, builtin []*types.TenantSkillEntity) []*types.TenantSkillEntity {
	merged := installed
	for _, b := range builtin {
		if b == nil {
			continue
		}
		taken := false
		for _, row := range installed {
			if row != nil && row.Name == b.Name {
				taken = true
				break
			}
		}
		if !taken {
			merged = append(merged, b)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}
