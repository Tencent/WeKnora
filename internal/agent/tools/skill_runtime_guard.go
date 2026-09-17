package tools

import (
	"strconv"
	"strings"
)

// A skill's dependencies are supposed to be complete when the install
// finishes. When they are not, what the model gets back at chat time carries
// no direction: "No module named pip" from a uv-created venv, or a bare
// permission error against a `.venv` path, says nothing about which of the
// several plausible recoveries is the right one. The hints below attach that
// direction after the fact.
//
// The recovery is to install into the skill's own environment, in place. That
// works because the sandbox belongs to this session alone and runs as root, so
// the write lands in the session's own container and dies with it — it never
// reaches the image other sessions start from. The overlay this guidance used
// to recommend (`pip install --target /workspace/.skill-packages/<skill>`)
// bought nothing over that and split a skill's packages across two locations.
//
// An up-front command blacklist was tried and removed: matching `pip install`
// next to the skills root also rejected the recovery command this guidance
// recommends, while any indirection through a shell variable walked straight
// past it.

// These commands run with skill_name so the shell environment supplies the
// actual image or staged directory. Keep work_dir at its /workspace default.
const (
	skillPythonPackageInstallCommand = `uv pip install --python ` +
		`"${WEKNORA_SKILL_DIR:?}/.venv/bin/python" <package>`
	skillPythonPackageFallbackCommand = `"${WEKNORA_SKILL_DIR:?}/.venv/bin/python" -m ensurepip --upgrade && ` +
		`"${WEKNORA_SKILL_DIR:?}/.venv/bin/python" -m pip install <package>`
	skillPythonVenvCreateCommand   = `python3 -m venv --without-pip "${WEKNORA_SKILL_DIR:?}/.venv"`
	skillNodePackageInstallCommand = `npm --prefix "${WEKNORA_SKILL_DIR:?}" install <package>`
)

func missingSkillPackageGuidance(skillName string) string {
	skillArg := "skill_name=<skill>"
	if skillName != "" {
		skillArg = "skill_name=" + strconv.Quote(skillName)
	}
	return "Install missing packages with shell_exec(" + skillArg + ", command=...), leaving work_dir at /workspace. " +
		"That named call supplies $WEKNORA_SKILL_DIR, the actual installed or staged skill directory. " +
		"For Python, use `" + skillPythonPackageInstallCommand + "`; uv does not need pip in the virtualenv. " +
		"If .venv is absent (as with staged host resources), first run `" + skillPythonVenvCreateCommand + "`. " +
		"If uv is unavailable in a custom image, use `" + skillPythonPackageFallbackCommand + "`. " +
		"For Node, use `" + skillNodePackageInstallCommand + "`. " +
		"Then rerun the original command with the same skill_name. " +
		"These changes live and die with this session; reinstall the skill if every session needs the package."
}

func isSkillVenvInstallFailure(stderr string) bool {
	if stderr == "" {
		return false
	}
	lower := strings.ToLower(stderr)
	if strings.Contains(stderr, "No module named pip") ||
		strings.Contains(stderr, "No module named 'pip'") {
		return true
	}
	return strings.Contains(lower, ".venv") &&
		(isReadOnlyFilesystemFailure(lower) || isPermissionFailure(lower))
}

func isReadOnlyFilesystemFailure(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "read-only file system") || strings.Contains(lower, "erofs") ||
		strings.Contains(lower, "read-only filesystem")
}

func isPermissionFailure(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "permission denied") || strings.Contains(lower, "operation not permitted") ||
		strings.Contains(lower, "eperm")
}

func skillVenvFailureGuidance(skillName, stderr string) string {
	if isReadOnlyFilesystemFailure(stderr) {
		return "The skill virtualenv is on a read-only filesystem. Root, uv and pip cannot write through " +
			"a read-only mount. Configure a writable skill environment before installing packages."
	}
	if isPermissionFailure(stderr) {
		return "The skill virtualenv denied access. Check its permissions and mount restrictions first; " +
			"changing package managers does not grant access. Once the environment is writable: " +
			missingSkillPackageGuidance(skillName)
	}
	return missingSkillPackageGuidance(skillName)
}
