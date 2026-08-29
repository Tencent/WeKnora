package tools

import (
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// Session skill trees are snapshotted read-only (root-owned, mode 555). uv
// venv often has no pip. Skills that lazily pip-install extras on first use
// cannot write that venv; session agents must use the /workspace overlay
// instead of chown / ensurepip / pip into /opt/weknora/tenant/skills.
//
// That rule is enforced by the filesystem, not by inspecting commands: the
// kernel already refuses the write. What the model needs is a readable reason
// for the EROFS / EPERM / "No module named pip" it gets back, which is what
// the hints below attach after the fact. An up-front command blacklist was
// tried and removed: matching `pip install` next to the skills root also
// rejected the recovery command this guidance recommends (installing into the
// overlay with `-r <skill>/requirements.txt`), while any indirection through a
// shell variable walked straight past it.

func frozenSkillTreeGuidance(skillName string) string {
	pkgDir := sandbox.SessionSkillPackageDir(skillName)
	skillArg := "skill_name=<skill>"
	if skillName != "" {
		skillArg = "skill_name=" + strconv.Quote(skillName)
	}
	return "the skill tree under " + sandbox.SkillsImageRoot +
		" is frozen after install (read-only, root-owned; uv venv often has no pip). " +
		"Do not chown, chmod, ensurepip, or pip/npm install into it. " +
		"On-demand extras (python-docx, python-pptx, …) go in the session overlay: " +
		"`python3 -m pip install --target " + pkgDir + " <package>` " +
		"(system python3, not the skill venv), then execute_skill_script(" + skillArg + "). " +
		"Or ask the user to reinstall the skill so extras are baked into the image."
}

func isFrozenSkillVenvFailure(stderr string) bool {
	if stderr == "" {
		return false
	}
	lower := strings.ToLower(stderr)
	if strings.Contains(stderr, "No module named pip") ||
		strings.Contains(stderr, "No module named 'pip'") {
		return true
	}
	if strings.Contains(lower, "read-only file system") ||
		strings.Contains(lower, "erofs") ||
		strings.Contains(lower, "read-only filesystem") {
		return true
	}
	return strings.Contains(lower, "permission denied") && strings.Contains(lower, ".venv")
}

func skillOnDemandInstallHint(skillName, scriptPath, stdout, stderr string) string {
	installer := skills.IsOnDemandInstallerPath(scriptPath)
	failedInstaller := installer &&
		(isFrozenSkillVenvFailure(stderr) || strings.Contains(strings.ToLower(stdout+stderr), "安装失败") ||
			strings.Contains(strings.ToLower(stdout+stderr), "install failed"))
	if !failedInstaller && !isFrozenSkillVenvFailure(stderr) {
		return ""
	}
	msg := "Hint: " + frozenSkillTreeGuidance(skillName)
	if installer {
		msg += " Skip this installer and run the skill's real script."
	}
	return msg
}

func skillMissingPackageHint(skillName, stderr string) string {
	if !isMissingInterpreterModule(stderr) {
		return ""
	}
	return "Hint: this skill's frozen venv does not have that package. " +
		frozenSkillTreeGuidance(skillName)
}
