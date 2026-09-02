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

// SkillRequirementsPath is where the installer agent writes what the skill
// needs at run time. It lives inside the skill's own directory so it travels
// with the skill into the snapshot, and it is a file rather than a tool call
// on purpose: the agent reads a third-party SKILL.md, so nothing it produces
// may reach the database except as bytes the server parses and validates.
//
// An invalid skill name yields an empty path, which every caller treats as
// "no declaration": the name is already validated before an install starts,
// and a best-effort read is not a place to fail an install.
func SkillRequirementsPath(skillName string) string {
	dir, err := SkillDirFor(skillName)
	if err != nil {
		return ""
	}
	return path.Join(dir, ".weknora", "requirements.json")
}

// RunnableWorkspaceScript reports whether scriptPath is a session-writable
// file under /workspace that may be executed with an installed skill's
// interpreter. /workspace/input is reserved for attachments and is excluded;
// so are the workspace directory roots themselves.
func RunnableWorkspaceScript(scriptPath string) (string, bool) {
	clean := path.Clean(strings.TrimSpace(scriptPath))
	if clean == SessionWorkspaceRoot || clean == SessionOutputRoot || clean == SessionInputRoot {
		return "", false
	}
	if !strings.HasPrefix(clean, SessionWorkspaceRoot+"/") {
		return "", false
	}
	if strings.HasPrefix(clean, SessionInputRoot+"/") {
		return "", false
	}
	return clean, true
}

// ValidatedSessionOutputDir normalises a configured artifact directory and
// reports whether it may be used.
//
// It is the single gate for every WEKNORA_SKILL_OUTPUT_DIR override, wherever
// it comes from: the host environment the app reads at startup, or a tenant's
// sandbox config. Without it the two disagreed — execution validated the path
// and fell back to SessionOutputRoot, while the tools and the artifact
// collector took the host value as-is, so an override pointing outside
// /workspace moved the readers somewhere the writers never wrote.
func ValidatedSessionOutputDir(dir string) (string, bool) {
	clean, err := cleanSessionWorkDir(dir, false)
	if err != nil {
		return "", false
	}
	return clean, true
}

// ValidatedImageSkillDir reports whether skillDir is exactly one installed
// skill directory under SkillsImageRoot (for example /opt/weknora/tenant/skills/pdf).
func ValidatedImageSkillDir(skillDir string) (string, bool) {
	clean := path.Clean(strings.TrimSpace(skillDir))
	expected, err := SkillDirFor(path.Base(clean))
	if err != nil || expected != clean {
		return "", false
	}
	return clean, true
}

// InterpreterSkillDir chooses the skill directory whose venv/node_modules
// should run remote. Image-skill scripts always win from the path so a
// mismatched SkillDir cannot redirect them. Workspace scripts require an
// explicit, validated SkillDir.
func InterpreterSkillDir(remotePath, skillDir string) (string, bool) {
	if dir, ok := SkillDirForImageScript(remotePath); ok {
		return dir, true
	}
	if _, ok := RunnableWorkspaceScript(remotePath); !ok {
		return "", false
	}
	return ValidatedImageSkillDir(skillDir)
}

// SkillNameFromImagePath reports whether p sits at or under SkillsImageRoot.
// The skills root itself returns ("", true). A skill directory or a file
// inside one returns (skillName, true). Paths outside the image return
// ("", false).
func SkillNameFromImagePath(p string) (name string, inImage bool) {
	clean := path.Clean(strings.TrimSpace(p))
	root := path.Clean(SkillsImageRoot)
	if clean == root {
		return "", true
	}
	prefix := root + "/"
	if !strings.HasPrefix(clean, prefix) {
		return "", false
	}
	name = strings.SplitN(strings.TrimPrefix(clean, prefix), "/", 2)[0]
	if !IsValidSkillName(name) {
		return "", false
	}
	return name, true
}

// SkillDirForImageScript returns the owning skill directory for an image script.
// It anchors on SkillsImageRoot so nested script layouts still use the venv that
// was installed beside the skill, not a shallower scripts directory.
func SkillDirForImageScript(scriptPath string) (string, bool) {
	name, inImage := SkillNameFromImagePath(scriptPath)
	if !inImage || name == "" {
		return "", false
	}
	clean := path.Clean(strings.TrimSpace(scriptPath))
	dir, err := SkillDirFor(name)
	if err != nil || clean == dir {
		return "", false
	}
	return dir, true
}

// SessionSkillPackageDir is the per-session extra-packages overlay for one
// skill. The image venv is frozen after install (root-owned, mode 555, and
// often created with `uv venv` so it has no pip). Skills that lazily
// `pip install` on first use cannot write there; packages installed with
// `python3 -m pip install --target` this directory are visible to
// execute_skill_script via PYTHONPATH / NODE_PATH. The directory is under
// /workspace so it dies with the session and never mutates the snapshot.
func SessionSkillPackageDir(skillName string) string {
	if !IsValidSkillName(skillName) {
		return path.Join(SessionWorkspaceRoot, ".skill-packages")
	}
	return path.Join(SessionWorkspaceRoot, ".skill-packages", skillName)
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
	case ".js", ".mjs", ".cjs":
		return "node", []string{scriptPath}
	case ".sh":
		// bash, with sh only as a fallback. Skill shell scripts carry a
		// `#!/bin/bash` shebang almost exclusively, and /bin/sh is dash on
		// Debian: an array literal, `function f()`, a C-style for loop and
		// process substitution are all syntax errors there, so running these
		// files with sh breaks scripts that are perfectly valid. The
		// install-time check parses them with the same shell.
		script := ShellQuote(scriptPath)
		return "/bin/sh", []string{"-c", fmt.Sprintf(
			`if command -v bash >/dev/null 2>&1; then exec bash %s "$@"; else exec sh %s "$@"; fi`,
			script, script,
		), skillShellArgv0}
	default:
		return "/bin/sh", []string{scriptPath}
	}
}
