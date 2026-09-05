package skills

import (
	"context"
	"fmt"
	"path"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// PrepareShellEnvironment attaches an allowed, installed skill's runtime to
// the same shell primitive used for ordinary commands. Credentials remain the
// caller's responsibility and are resolved per tool call, never persisted here.
func (m *Manager) PrepareShellEnvironment(ctx context.Context, sessionID, skillName, command string, env map[string]string) (string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if m == nil || !m.enabled || !m.isSkillAllowed(skillName) {
		return "", nil, fmt.Errorf("skill %q is not available to this agent", skillName)
	}
	dir, ok := m.SandboxSkillDir(skillName)
	if !ok {
		var err error
		dir, err = m.stageShellSkill(ctx, sessionID, skillName)
		if err != nil {
			return "", nil, err
		}
	} else if _, valid := sandbox.ValidatedImageSkillDir(dir); !valid {
		return "", nil, fmt.Errorf("invalid installed directory for skill %q", skillName)
	}
	runtimeEnv := make(map[string]string, len(env)+6)
	for k, v := range env {
		runtimeEnv[k] = v
	}
	applySessionPackagePath(runtimeEnv, skillName)
	runtimeEnv[nodePathEnvVar] += ":" + path.Join(dir, "node_modules")
	runtimeEnv[skillDirEnvVar] = dir
	runtimeEnv[artifactOutputEnvVar] = ArtifactOutputDir()
	runtimeEnv[artifactHistoryEnvVar] = ArtifactOutputDir()
	runtimeEnv[sessionInputEnvVar] = sandbox.SessionInputRoot
	// Set PATH after the provider's login shell has loaded its profiles. Use a
	// child non-login shell so leading assignments and arbitrary shell grammar
	// keep their original meaning and cannot consume the setup prefix.
	prefix := path.Join(dir, ".venv", "bin") + ":" + path.Join(dir, "node_modules", ".bin")
	wrapped := "export PATH=" + sandbox.ShellQuote(prefix) + ":\"$PATH\"; exec /bin/bash --noprofile --norc -c " + sandbox.ShellQuote(command)
	return wrapped, runtimeEnv, nil
}
