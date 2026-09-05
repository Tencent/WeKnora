package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestResolvePerRequestMCPScope_SelectedIntersection(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-b", "mcp-c"},
		[]string{"mcp-a", "mcp-b"},
		"selected",
		false,
	)
	assert.Equal(t, "selected", mode)
	assert.Equal(t, []string{"mcp-b"}, effective)
}

func TestResolvePerRequestMCPScope_SelectedRejectsOutsidePreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-x"},
		[]string{"mcp-a"},
		"selected",
		false,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "selected", mode)
}

func TestResolvePerRequestMCPScope_NoneRejectsMention(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-iwiki"},
		nil,
		"none",
		false,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "none", mode)
}

func TestResolvePerRequestMCPScope_SharedAgentBlocksOutsidePreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-x"},
		[]string{"mcp-a"},
		"all",
		true,
	)
	assert.Empty(t, effective)
	assert.Equal(t, "all", mode)
}

func TestResolvePerRequestMCPScope_SharedAgentAllowsPreset(t *testing.T) {
	effective, mode := resolvePerRequestMCPScope(
		[]string{"mcp-a", "mcp-x"},
		[]string{"mcp-a", "mcp-b"},
		"all",
		true,
	)
	assert.Equal(t, "selected", mode)
	assert.Equal(t, []string{"mcp-a"}, effective)
}

func TestApplyPerRequestMCPScope_SelectedNarrowsAndPins(t *testing.T) {
	cfg := &types.AgentConfig{MCPSelectionMode: "selected", MCPServices: []string{"mcp-a", "mcp-b"}}
	applyPerRequestMCPScope(context.Background(), cfg, []string{"mcp-a", "mcp-b"}, false, []string{"mcp-b"})
	assert.Equal(t, "selected", cfg.MCPSelectionMode)
	assert.Equal(t, []string{"mcp-b"}, cfg.MCPServices)
	assert.Equal(t, []string{"mcp-b"}, cfg.PinnedMCPServiceIDs)
}

func TestApplyPerRequestMCPScope_NoneIgnoresMentionAndDoesNotPin(t *testing.T) {
	cfg := &types.AgentConfig{MCPSelectionMode: "none", MCPServices: []string{"mcp-a"}}
	applyPerRequestMCPScope(context.Background(), cfg, []string{"mcp-a"}, false, []string{"mcp-a"})
	assert.Equal(t, "none", cfg.MCPSelectionMode)
	assert.Empty(t, cfg.PinnedMCPServiceIDs)
}

func TestApplyPerRequestSkillScope_SelectedPinsMentionedAndKeepsAllowed(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true, AllowedSkills: []string{"a", "b"}}
	applyPerRequestSkillScope(context.Background(), cfg, "selected", []string{"a"})
	assert.True(t, cfg.SkillsEnabled)
	// Allow-gate is not narrowed: a prompt-mandated skill the user did not
	// @mention (b) must remain callable.
	assert.Equal(t, []string{"a", "b"}, cfg.AllowedSkills)
	assert.Equal(t, []string{"a"}, cfg.PinnedSkillNames)
}

func TestApplyPerRequestSkillScope_SelectedMentionOutsideAllowedIsNotPinned(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true, AllowedSkills: []string{"a", "b"}}
	applyPerRequestSkillScope(context.Background(), cfg, "selected", []string{"c"})
	// Skills stay enabled (no narrowing-to-empty disable); the out-of-scope
	// mention is simply not pinned.
	assert.True(t, cfg.SkillsEnabled)
	assert.Equal(t, []string{"a", "b"}, cfg.AllowedSkills)
	assert.Empty(t, cfg.PinnedSkillNames)
}

func TestApplyPerRequestSkillScope_AllPinsMentionedWithoutNarrowingGate(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true}
	applyPerRequestSkillScope(context.Background(), cfg, "all", []string{"analysis", "analysis"})
	assert.True(t, cfg.SkillsEnabled)
	// "all" mode keeps AllowedSkills empty (= all allowed); it does not narrow
	// to only the mentioned set, so other skills the agent needs stay callable.
	assert.Empty(t, cfg.AllowedSkills)
	assert.Equal(t, []string{"analysis"}, cfg.PinnedSkillNames)
}

func TestApplyPerRequestSkillScope_NoneIgnores(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true, AllowedSkills: []string{"a"}}
	applyPerRequestSkillScope(context.Background(), cfg, "none", []string{"a"})
	assert.Empty(t, cfg.PinnedSkillNames)
}

func TestConfigureSkillsFromAgentDoesNotLoadHostPreloadedDir(t *testing.T) {
	svc := &sessionService{}
	cfg := &types.AgentConfig{}
	svc.configureSkillsFromAgent(context.Background(), cfg, &types.CustomAgent{
		Config: types.CustomAgentConfig{
			SandboxConfigID:     "cfg-1",
			SkillsSelectionMode: "all",
		},
	})
	assert.True(t, cfg.SkillsEnabled)
	assert.Equal(t, "cfg-1", cfg.SandboxConfigID)
	assert.Empty(t, cfg.SkillDirs,
		"a host skill directory is not what the sandbox image carries")
}
