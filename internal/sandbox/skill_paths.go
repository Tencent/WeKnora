package sandbox

import (
	"errors"
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

// ErrInvalidSkillName is returned when a skill name would escape SkillsImageRoot
// or is not a single path segment.
var ErrInvalidSkillName = errors.New("sandbox: invalid skill name")

// IsValidSkillName reports whether name is a single directory segment under
// SkillsImageRoot. Empty names, "." / "..", and anything containing a path
// separator are rejected so install/exec cannot walk out of the skills root.
func IsValidSkillName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return path.Base(path.Clean(name)) == name
}

// SkillDirFor returns the image directory of a skill. The key is the skill
// name from SKILL.md: that is what the installer writes, and what the agent
// is told to execute. The database id stays a row key and is not part of
// the path.
func SkillDirFor(skillName string) (string, error) {
	if !IsValidSkillName(skillName) {
		return "", fmt.Errorf("%w %q", ErrInvalidSkillName, skillName)
	}
	return path.Join(SkillsImageRoot, skillName), nil
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
	if len(parts) != 2 || parts[0] == "" || !IsValidSkillName(parts[0]) {
		return "", false
	}
	return path.Join(cleanRoot, parts[0]), true
}

// SkillVenvPython is where a skill's own Python interpreter lives when the
// install created one. It is exported because the model needs to be told: the
// system python3 deliberately carries no skill dependencies, so anything that
// inspects or debugs a Python skill has to name this path.
func SkillVenvPython(skillDir string) string {
	return path.Join(skillDir, ".venv", "bin", "python")
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
	switch strings.ToLower(path.Ext(scriptPath)) {
	case ".py":
		venvPython := SkillVenvPython(skillDir)
		script := ShellQuote(scriptPath)
		return "/bin/sh", []string{"-c", fmt.Sprintf(
			`if [ -x %s ]; then exec %s %s "$@"; else exec python3 %s "$@"; fi`,
			ShellQuote(venvPython), ShellQuote(venvPython), script, script,
		), skillShellArgv0}
	case ".js", ".mjs":
		return "node", []string{scriptPath}
	case ".sh":
		return "/bin/sh", []string{scriptPath}
	default:
		return "/bin/sh", []string{scriptPath}
	}
}
