package sandbox

import (
	"fmt"
	"path"
	"strings"
)

const (
	// SkillsImageRoot is where installed skills live inside the snapshot image.
	// It is outside /workspace on purpose: /workspace is per-session scratch
	// and is wiped before every snapshot.
	SkillsImageRoot = "/opt/weknora/tenant/skills"

	// SkillsManifestPath lists what the image claims to contain. It is a
	// troubleshooting aid, never the source of truth for execution.
	SkillsManifestPath = SkillsImageRoot + "/.manifest.json"
)

const skillShellArgv0 = "weknora-skill"

// SkillDirFor returns the image directory of a skill. The key is the skill
// name from SKILL.md: that is what the installer writes, and what the agent
// is told to execute. The database id stays a row key and is not part of
// the path.
func SkillDirFor(skillName string) string {
	return path.Join(SkillsImageRoot, skillName)
}

// SkillDirForImageScript returns the owning skill directory for an image script.
// It anchors on SkillsImageRoot so nested script layouts still use the venv that
// was installed beside the skill, not a shallower scripts directory.
func SkillDirForImageScript(scriptPath string) (string, bool) {
	cleanRoot := path.Clean(SkillsImageRoot)
	cleanScript := path.Clean(scriptPath)
	prefix := cleanRoot + "/"
	if !strings.HasPrefix(cleanScript, prefix) {
		return "", false
	}

	rest := strings.TrimPrefix(cleanScript, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", false
	}
	return path.Join(cleanRoot, parts[0]), true
}

// SkillInterpreterCommand picks how to run one script of a skill.
//
// The interpreter is derived per script rather than stored per skill: one skill
// may ship both .py and .js entry points, so a single stored "interpreter"
// column could never be right for all of them.
//
// For Python we prefer the skill's own venv. The choice is made inside the
// sandbox with a shell conditional instead of an extra round trip to stat the
// path, because the extra Exec would double the latency of every skill call.
func SkillInterpreterCommand(skillDir, scriptPath string) (string, []string) {
	switch {
	case strings.HasSuffix(scriptPath, ".py"):
		venvPython := path.Join(skillDir, ".venv", "bin", "python")
		script := shellQuote(scriptPath)
		return "/bin/sh", []string{"-c", fmt.Sprintf(
			`if [ -x %s ]; then exec %s %s "$@"; else exec python3 %s "$@"; fi`,
			shellQuote(venvPython), shellQuote(venvPython), script, script,
		), skillShellArgv0}
	case strings.HasSuffix(scriptPath, ".js"), strings.HasSuffix(scriptPath, ".mjs"):
		return "node", []string{scriptPath}
	case strings.HasSuffix(scriptPath, ".sh"):
		return "/bin/sh", []string{scriptPath}
	default:
		return "/bin/sh", []string{scriptPath}
	}
}
