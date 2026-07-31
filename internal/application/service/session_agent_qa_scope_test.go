package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestApplyPerRequestSkillScope_SelectedEmptyIntersectionDisables(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true, AllowedSkills: []string{"a", "b"}}
	applyPerRequestSkillScope(context.Background(), cfg, "selected", []string{"c"})
	assert.False(t, cfg.SkillsEnabled)
	assert.Empty(t, cfg.PinnedSkillNames)
}

func TestApplyPerRequestSkillScope_AllPinsMentioned(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true}
	applyPerRequestSkillScope(context.Background(), cfg, "all", []string{"analysis", "analysis"})
	assert.True(t, cfg.SkillsEnabled)
	assert.Equal(t, []string{"analysis"}, cfg.AllowedSkills)
	assert.Equal(t, []string{"analysis"}, cfg.PinnedSkillNames)
}

func TestApplyPerRequestSkillScope_NoneIgnores(t *testing.T) {
	cfg := &types.AgentConfig{SkillsEnabled: true, AllowedSkills: []string{"a"}}
	applyPerRequestSkillScope(context.Background(), cfg, "none", []string{"a"})
	assert.Empty(t, cfg.PinnedSkillNames)
}

func TestApplyPreparedAgentKnowledgeScopeKeepsDisabledFolderTarget(t *testing.T) {
	filter, err := types.NewResolvedFolderFilter(false, nil)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		77,
		[]string{"knowledge-1"},
		nil,
		nil,
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope([]types.KnowledgeScopeTarget{target})
	require.NoError(t, err)
	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{KnowledgeBaseIDs: []string{"kb-1"}},
		scope,
		"execution-hash",
	)
	require.NoError(t, err)
	config := &types.AgentConfig{WebSearchEnabled: true}

	applyPreparedAgentKnowledgeScope(config, projection)

	assert.Equal(t, []string{"kb-1"}, config.KnowledgeBases)
	assert.Equal(t, []string{"knowledge-1"}, config.KnowledgeIDs)
	require.Len(t, config.SearchTargets, 1)
	assert.Equal(t, "kb-1", config.SearchTargets[0].KnowledgeBaseID)
	assert.True(t, config.WebSearchEnabled)
	assert.True(t, agentHasKnowledgeScope(config))
}

func TestApplyPreparedAgentKnowledgeScopeDropsEnabledEmptyTarget(t *testing.T) {
	filter, err := types.NewResolvedFolderFilter(true, nil)
	require.NoError(t, err)
	target, err := types.NewKnowledgeScopeTarget(
		"kb-1",
		77,
		nil,
		nil,
		nil,
		filter,
	)
	require.NoError(t, err)
	scope, err := types.NewKnowledgeScope([]types.KnowledgeScopeTarget{target})
	require.NoError(t, err)
	folderScopes := []types.FolderScopeRequest{{
		KnowledgeBaseID: "kb-1",
		FolderIDs:       []string{},
	}}
	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{FolderScopes: &folderScopes},
		scope,
		"execution-hash",
	)
	require.NoError(t, err)
	config := &types.AgentConfig{
		KnowledgeBases:   []string{"legacy-kb"},
		KnowledgeIDs:     []string{"legacy-knowledge"},
		WebSearchEnabled: true,
	}

	applyPreparedAgentKnowledgeScope(config, projection)

	assert.Empty(t, config.KnowledgeBases)
	assert.Empty(t, config.KnowledgeIDs)
	assert.Empty(t, config.SearchTargets)
	assert.False(t, config.WebSearchEnabled)
	assert.False(t, agentHasKnowledgeScope(config))
}

func TestApplyPreparedAgentKnowledgeScopePreservesTopLevelExplicitEmpty(t *testing.T) {
	emptyFolderScopes := []types.FolderScopeRequest{}
	scope, err := types.NewKnowledgeScope(nil)
	require.NoError(t, err)
	projection, err := projectKnowledgeQARuntime(
		&types.KnowledgeScopeRequest{FolderScopes: &emptyFolderScopes},
		scope,
		"execution-hash",
	)
	require.NoError(t, err)
	config := &types.AgentConfig{
		KnowledgeBases:   []string{"legacy-kb"},
		KnowledgeIDs:     []string{"legacy-knowledge"},
		WebSearchEnabled: true,
	}

	applyPreparedAgentKnowledgeScope(config, projection)

	assert.True(t, projection.retrievalExplicitlyEmpty)
	assert.Empty(t, config.KnowledgeBases)
	assert.Empty(t, config.KnowledgeIDs)
	assert.Empty(t, config.SearchTargets)
	assert.False(t, config.WebSearchEnabled)
	assert.False(t, agentHasKnowledgeScope(config))
}
