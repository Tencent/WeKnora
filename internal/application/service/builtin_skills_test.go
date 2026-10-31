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
	require.Contains(t, parsed.Description, "ask_user")
	require.NotContains(t, parsed.Description, "<")

	body := parsed.Instructions
	for _, phrase := range []string{
		"agent-browser open", "snapshot -i", "agent-browser close",
		"agent-browser --help", "without skill_name", "--headed",
	} {
		require.Contains(t, body, phrase)
	}
	require.Contains(t, body, "ask the user with `ask_user`",
		"the skill must make the agent ask headed-vs-headless before the first browser command")
	require.Contains(t, body, "headless",
		"the skill must still name the fallback when the user does not care")
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
