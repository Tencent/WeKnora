package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func skillSearchConfig() *types.AgentConfig {
	return &types.AgentConfig{
		SkillsEnabled:        true,
		SkillInstallCards:    true,
		SkillSandboxConfigID: "cfg-pinned",
	}
}

func TestSkillSearchIsOfferedOnlyWhereACardCanBeActedOn(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	installer := skillSearchConfig()
	installer.EnableSkillInstallMode(types.BuiltinSkillInstallerID, sandbox.SkillsImageRoot+"/pptx")

	cases := map[string]struct {
		ctx    context.Context
		config *types.AgentConfig
		want   bool
	}{
		"console chat with skills on": {ctx, skillSearchConfig(), true},
		"IM, embed or API client": {ctx, func() *types.AgentConfig {
			c := skillSearchConfig()
			c.SkillInstallCards = false
			return c
		}(), false},
		"skills off for this agent": {ctx, func() *types.AgentConfig {
			c := skillSearchConfig()
			c.SkillsEnabled = false
			return c
		}(), false},
		"no sandbox config to install onto": {ctx, func() *types.AgentConfig {
			c := skillSearchConfig()
			c.SkillSandboxConfigID = ""
			return c
		}(), false},
		"shared agent": {ctx, func() *types.AgentConfig {
			c := skillSearchConfig()
			c.SharedAgentReadOnly = true
			return c
		}(), false},
		"the installer agent":     {ctx, installer, false},
		"no workspace on the ctx": {context.Background(), skillSearchConfig(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			registry := tools.NewToolRegistry()
			(&agentService{}).registerSkillSearch(tc.ctx, registry, tc.config)
			_, err := registry.GetTool(tools.ToolSearchSkills)
			assert.Equal(t, tc.want, err == nil)
		})
	}
}

// The card installs onto the config the session's sandbox actually boots,
// which is the pinned one when the agent has since been re-pointed.
func TestSkillSearchCardsTargetTheRunsPinnedConfig(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	config := skillSearchConfig()
	config.SandboxConfigID = "cfg-agent"
	config.AllowedSkills = []string{"pdf"}
	registry := tools.NewToolRegistry()
	svc := &agentService{skillSearch: testRegistrySearch(
		registryServer(t, clawHubSearchBody, 200),
		registryServer(t, skillHubSearchBody, 200),
	)}

	svc.registerSkillSearch(ctx, registry, config)
	result, err := registry.ExecuteTool(ctx, tools.ToolSearchSkills, json.RawMessage(`{"query":"pptx"}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Equal(t, "cfg-pinned", result.Data["sandbox_config_id"])
	assert.Equal(t, true, result.Data["agent_selects_skills"])
}

// shell_exec's note on a fetched skill names the install card only when
// search_skills was registered for the same run.
func TestShellNoteFollowsSkillSearchRegistration(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	for _, withSearch := range []bool{true, false} {
		registry := tools.NewToolRegistry()
		config := skillSearchConfig()
		config.SkillInstallCards = withSearch
		svc := &agentService{}
		svc.registerSkillSearch(ctx, registry, config)
		svc.registerSandboxShellTool(ctx, registry,
			&capableManager{typ: sandbox.SandboxTypeE2B, shell: &stubShellExecutor{}}, config)

		result, err := registry.ExecuteTool(installShellToolContext(), tools.ToolShellExec,
			json.RawMessage(`{"command":"npx skills add anthropics/skills@pdf -y"}`))

		require.NoError(t, err)
		if withSearch {
			assert.Contains(t, result.Output, "call search_skills")
		} else {
			assert.NotContains(t, result.Output, "search_skills")
			assert.Contains(t, result.Output, "skill settings")
		}
	}
}
