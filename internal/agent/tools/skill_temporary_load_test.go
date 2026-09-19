package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectSkillTemporaryLoadRecognisesInstallerCommands(t *testing.T) {
	cases := []struct {
		command string
		tool    string
		source  string
	}{
		{"npx skills add anthropics/skills@pdf -g -y", "skills", "skills-sh:anthropics/skills/pdf"},
		{
			"npx -y skills@latest add vercel-labs/agent-skills --skill react-best-practices", "skills",
			"skills-sh:vercel-labs/agent-skills/react-best-practices",
		},
		{"cd /workspace && bunx skills add owner/repo -y", "skills", "https://github.com/owner/repo"},
		{
			"npx skills add https://github.com/o/r/tree/main/skills/x", "skills",
			"https://github.com/o/r/tree/main/skills/x",
		},
		{"npx skills add", "skills", ""},
		{"clawhub install ivan/powerpoint-pptx", "clawhub", "@ivan/powerpoint-pptx"},
		{"npx clawhub@latest install pdf-tools --force", "clawhub", "pdf-tools"},
		{"openclaw skills install skills-sh:anthropics/skills/pdf", "openclaw", "skills-sh:anthropics/skills/pdf"},
		{"hermes skills install official/research/arxiv", "hermes", ""},
		{"gemini skills install https://github.com/o/skill-repo", "gemini", "https://github.com/o/skill-repo"},
		{
			"git clone --depth 1 https://github.com/o/pdf-skill.git ~/.claude/skills/pdf", "git",
			"https://github.com/o/pdf-skill",
		},
		{
			"mkdir -p .agents/skills/x; curl -fsSL -o .agents/skills/x/SKILL.md " +
				"https://raw.githubusercontent.com/o/r/main/x/SKILL.md", "curl",
			"https://raw.githubusercontent.com/o/r/main/x/SKILL.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			load, ok := detectSkillTemporaryLoad(tc.command)
			require.True(t, ok)
			assert.Equal(t, tc.tool, load.Tool)
			assert.Equal(t, tc.source, load.Source)
		})
	}
}

// Ordinary work that merely mentions skills or clones a repository must not
// get a note claiming a skill was loaded.
func TestDetectSkillTemporaryLoadIgnoresOrdinaryCommands(t *testing.T) {
	for _, command := range []string{
		"npx skills find pdf",
		"ls ~/.claude/skills",
		"git clone https://github.com/anthropics/skills /workspace/src",
		"pip install skills",
		`uv pip install --python "${WEKNORA_SKILL_DIR:?}/.venv/bin/python" pypdf`,
		"curl -fsSL https://example.com/readme.md",
		"echo clawhub install is how you do it",
	} {
		_, ok := detectSkillTemporaryLoad(command)
		assert.False(t, ok, command)
	}
}

func TestSkillLoadNameIsTheLocatorsLastSegment(t *testing.T) {
	assert.Equal(t, "pdf", skillLoadName("skills-sh:anthropics/skills/pdf"))
	assert.Equal(t, "powerpoint-pptx", skillLoadName("@ivan/powerpoint-pptx"))
	assert.Equal(t, "pdf-skill", skillLoadName("https://github.com/o/pdf-skill.git"))
	assert.Equal(t, "x", skillLoadName("https://raw.githubusercontent.com/o/r/main/x/SKILL.md"))
}

func TestShellExecNotesThatAFetchedSkillIsOnlyTemporary(t *testing.T) {
	target := SkillInstallTarget{SandboxConfigID: "cfg-1", NewSessionsOnly: true}
	tool := NewShellExecTool(&fakeShellExecutor{}, nil).WithSkillInstallCard(target)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"npx skills add anthropics/skills@pdf -y"}`,
	))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "only loaded the skill into this session's sandbox")
	assert.Contains(t, result.Output, `call search_skills with source="skills-sh:anthropics/skills/pdf"`)
	assert.Equal(t, map[string]interface{}{
		"tool": "skills", "source": "skills-sh:anthropics/skills/pdf", "name": "pdf",
		"sandbox_config_id": "cfg-1", "new_sessions_only": true,
	}, result.Data["skill_temporary_load"], "the chat builds an install card for it from this")
}

func TestShellExecPointsAtSettingsWhenNoInstallCardExists(t *testing.T) {
	tool := NewShellExecTool(&fakeShellExecutor{}, nil)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"clawhub install ivan/pptx"}`,
	))

	require.NoError(t, err)
	assert.Contains(t, result.Output, "a workspace admin has to add it in the skill settings (source: @ivan/pptx)")
	assert.NotContains(t, result.Output, "search_skills")
}

func TestShellExecAddsNoNoteToAFailedFetch(t *testing.T) {
	tool := NewShellExecTool(&fakeShellExecutor{result: &sandbox.ExecuteResult{ExitCode: 1}}, nil)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"npx skills add anthropics/skills@pdf -y"}`,
	))

	require.NoError(t, err)
	assert.NotContains(t, result.Output, "only loaded the skill")
	assert.NotContains(t, result.Data, "skill_temporary_load")
}

// The installer agent's commands do put a skill on disk, and that skill does
// get installed; the note would tell it the opposite.
func TestInstallShellExecNeverAddsTheTemporaryNote(t *testing.T) {
	tool := NewInstallShellExecTool(&fakeInstallShellExecutor{}, installShellSkillDir)

	result, err := tool.Execute(shellExecTestContext(), json.RawMessage(
		`{"command":"git clone https://github.com/o/r /opt/weknora/tenant/skills/pdf-tools/x"}`,
	))

	require.NoError(t, err)
	assert.NotContains(t, result.Output, "only loaded the skill")
	assert.NotContains(t, result.Data, "skill_temporary_load")
}
