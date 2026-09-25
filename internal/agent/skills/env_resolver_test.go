package skills

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

// The runtime seeds the artifact directory variables before the resolver runs.
// Whatever a user stored, those keys must survive untouched: they are how the
// turn's products are collected.
func TestApplyResolvedEnvAddsNewKeysAndKeepsExistingOnes(t *testing.T) {
	env := map[string]string{
		artifactOutputEnvVar:  "/workspace/output",
		artifactHistoryEnvVar: "/workspace/output",
	}

	ApplyResolvedEnv(env, map[string]string{
		"TAVILY_API_KEY":      "user-key",
		artifactOutputEnvVar:  "/tmp/hijacked",
		artifactHistoryEnvVar: "/tmp/hijacked",
	})

	require.Equal(t, "user-key", env["TAVILY_API_KEY"])
	require.Equal(t, "/workspace/output", env[artifactOutputEnvVar])
	require.Equal(t, "/workspace/output", env[artifactHistoryEnvVar])
}

// Python is deliberately absent here: the skill's venv interpreter carries its
// own packages, so an injected PYTHONPATH would only be able to shadow them.
func TestApplySkillNodePathAppendsAfterTheCallersValue(t *testing.T) {
	skillDir := sandbox.SkillsImageRoot + "/律师助手"

	env := map[string]string{nodePathEnvVar: "/already"}
	applySkillNodePath(env, skillDir)
	require.Equal(t, "/already:"+skillDir+"/node_modules", env[nodePathEnvVar])

	empty := map[string]string{}
	applySkillNodePath(empty, skillDir)
	require.Equal(t, skillDir+"/node_modules", empty[nodePathEnvVar],
		"a caller that supplied nothing must not get a leading separator")
	require.NotContains(t, empty, pythonPathEnvVar)
}

// The message is read by the agent and relayed verbatim to a person, so it has
// to name the skill, the variables, and where to go and fix it.
func TestMissingSkillEnvErrorMessageNamesSkillVarsAndWhereToSetThem(t *testing.T) {
	err := error(&MissingSkillEnvError{
		SkillName: "web-search",
		Names:     []string{"TAVILY_API_KEY", "SERP_TOKEN"},
	})

	msg := err.Error()
	require.Contains(t, msg, "web-search")
	require.Contains(t, msg, "TAVILY_API_KEY")
	require.Contains(t, msg, "SERP_TOKEN")
	require.Contains(t, msg, "Sandbox secrets")

	var typed *MissingSkillEnvError
	require.True(t, errors.As(err, &typed))
	require.Equal(t, "web-search", typed.SkillName)
	require.Equal(t, []string{"TAVILY_API_KEY", "SERP_TOKEN"}, typed.Names)
}
