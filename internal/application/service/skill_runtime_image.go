package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// reconcileRuntimeSkills runs after normal session/image resolution. A config
// may have moved to a new snapshot while an existing session deliberately kept
// the old one (new_session rollout). In that case the database's current skill
// version is not what is on disk; never inject those newer instructions.
func reconcileRuntimeSkills(
	ctx context.Context,
	mgr sandbox.Manager,
	sessionID string,
	rows []*types.TenantSkillEntity,
) []*types.TenantSkillEntity {
	if len(rows) == 0 {
		return rows
	}
	reader, ok := mgr.(sandbox.SessionFileReader)
	executor := sessionSandboxShellExecutor(mgr)
	if !ok || executor == nil {
		return rows
	} // Host-only/test skill sources.
	checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result, err := executor.ExecShellCommand(checkCtx, sessionID, "true", "", 30*time.Second, nil)
	if err != nil || result == nil || result.ExitCode != 0 {
		logger.Warnf(ctx, "[skill] session image unavailable; withholding installed skill instructions")
		return nil
	}
	raw, err := reader.ReadSessionFile(checkCtx, sessionID, sandbox.SkillsManifestPath)
	if err != nil || len(raw) > 1024*1024 {
		return nil
	}
	return skillsMatchingRuntimeManifest(rows, raw)
}

func skillsMatchingRuntimeManifest(rows []*types.TenantSkillEntity, raw []byte) []*types.TenantSkillEntity {
	var manifest skillImageManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return nil
	}
	installed := make(map[string]skillImageManifestEntry, len(manifest.Skills))
	for _, entry := range manifest.Skills {
		installed[entry.ID] = entry
	}
	result := make([]*types.TenantSkillEntity, 0, len(rows))
	for _, row := range rows {
		if row == nil {
			continue
		}
		entry, exists := installed[row.ID]
		if exists && entry.Name == row.Name && entry.SHA256 != "" && entry.SHA256 == row.BundleSHA256 {
			result = append(result, row)
		}
	}
	return result
}
