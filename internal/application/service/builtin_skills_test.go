package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAgentBrowserSkillTextMatchesTheWrapper(t *testing.T) {
	parsed, err := skills.ParseSkillFile(agentBrowserSkillMD)
	require.NoError(t, err)
	require.Equal(t, "agent-browser", parsed.Name)
	require.LessOrEqual(t, len(parsed.Description), 600)
	require.Contains(t, parsed.Description, "local_browser")
	require.NotContains(t, parsed.Description, "ask_user",
		"the browser is visible by default; the agent must not ask headed-vs-headless")
	require.NotContains(t, parsed.Description, "<")

	body := parsed.Instructions
	for _, phrase := range []string{
		"agent-browser open", "snapshot -i", "agent-browser close",
		"agent-browser --help", "without skill_name",
	} {
		require.Contains(t, body, phrase)
	}
	require.NotContains(t, body, "ask_user",
		"the browser is visible by default; the agent must not ask headed-vs-headless")
	require.NotContains(t, body, "--headed",
		"the wrapper adds --headed itself; teaching the flag invites --headed false")
	require.Contains(t, body, "Desktop tab",
		"the skill must say where the visible browser window appears")
	require.Contains(t, body, "skills get core",
		"the skill is a discovery stub: it must point the model at the CLI's own guide")
	require.NotContains(t, body, "wk-desk")
	require.NotContains(t, body, "/opt/agent-browser")
	require.NotContains(t, body, "vault.json")
	require.NotContains(t, agentBrowserSkillMD, "wk-auth")
}

func TestBuiltinSkillRowsOnlyOnDesktopConfigs(t *testing.T) {
	rows := builtinSkillRows(t.Context(), &types.TenantSandboxConfig{DesktopEnabled: true}, 7, "cfg-1")
	require.Len(t, rows, 1)
	require.Equal(t, "builtin:agent-browser", rows[0].ID)
	require.True(t, rows[0].Builtin)
	require.True(t, rows[0].Enabled)
	require.Equal(t, types.SkillStatusReady, rows[0].Status)
	require.True(t, sandbox.IsValidSkillName(rows[0].Name))

	require.Nil(t, builtinSkillRows(t.Context(), &types.TenantSandboxConfig{}, 7, "cfg-1"))
	require.Nil(t, builtinSkillRows(t.Context(), nil, 7, "cfg-1"))
}
