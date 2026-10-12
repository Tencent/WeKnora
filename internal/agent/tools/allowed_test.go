package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffectiveAllowedToolsSeparatesUnsetFromEmpty(t *testing.T) {
	require.Equal(t, DefaultAllowedTools(), EffectiveAllowedTools(nil), "an unset list selects the defaults")

	empty := EffectiveAllowedTools([]string{})
	require.NotNil(t, empty, "an empty list must stay distinguishable from an unset one")
	require.Empty(t, empty, "an empty list means no built-in tools")

	require.Equal(t,
		[]string{ToolSearchKnowledge, ToolThinking},
		EffectiveAllowedTools([]string{LegacyToolKnowledgeSearch, ToolThinking, LegacyToolGrepChunks}),
		"retired names map to their successors without duplicates")
}

func TestCheckAllowedTools(t *testing.T) {
	t.Run("an unset list stays unset", func(t *testing.T) {
		require.Equal(t, AllowedToolsCheck{}, CheckAllowedTools([]string{"none"}, nil))
	})

	t.Run("an empty list is stored as given", func(t *testing.T) {
		check := CheckAllowedTools(nil, []string{})
		require.NotNil(t, check.Tools)
		require.Empty(t, check.Tools)
	})

	t.Run("retired names are rewritten", func(t *testing.T) {
		check := CheckAllowedTools(nil, []string{LegacyToolKnowledgeSearch, LegacyToolGetDocumentInfo})
		require.Equal(t, []string{ToolSearchKnowledge, ToolReadDocument}, check.Tools)
		require.Empty(t, check.Rejected)
	})

	t.Run("names the request adds that no tool answers to are rejected", func(t *testing.T) {
		check := CheckAllowedTools([]string{ToolThinking}, []string{ToolThinking, "none", "serch_knowledge"})
		require.Equal(t, []string{"none", "serch_knowledge"}, check.Rejected)
	})

	t.Run("unknown names already stored are kept", func(t *testing.T) {
		check := CheckAllowedTools([]string{"none", ToolThinking}, []string{ToolThinking, "none"})
		require.Empty(t, check.Rejected)
		require.Equal(t, []string{"none"}, check.KeptUnknown)
		require.Equal(t, []string{ToolThinking, "none"}, check.Tools)
	})

	t.Run("switch-managed tools are dropped", func(t *testing.T) {
		check := CheckAllowedTools(nil, []string{ToolWebSearch, ToolThinking, ToolSearchMemory, ToolDiscoverMCPTools})
		require.Equal(t, []string{ToolThinking}, check.Tools)
		require.Equal(t, []string{ToolWebSearch, ToolSearchMemory, ToolDiscoverMCPTools}, check.Dropped)
		require.Empty(t, check.Rejected)
	})

	t.Run("retired skill tools without a successor are dropped", func(t *testing.T) {
		check := CheckAllowedTools(
			[]string{LegacyToolReadSkill},
			[]string{LegacyToolReadSkill, LegacyToolExecuteSkillScript},
		)
		require.Empty(t, check.Tools)
		require.Equal(t, []string{LegacyToolReadSkill, LegacyToolExecuteSkillScript}, check.Dropped)
		require.Empty(t, check.KeptUnknown)
	})

	t.Run("a retired name stored under its old spelling still counts as stored", func(t *testing.T) {
		check := CheckAllowedTools([]string{LegacyToolKnowledgeSearch}, []string{ToolSearchKnowledge})
		require.Empty(t, check.Rejected)
		require.Empty(t, check.KeptUnknown)
	})
}
