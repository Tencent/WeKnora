package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildAgentToolCatalogDescribesEverySelectableTool(t *testing.T) {
	catalog := BuildAgentToolCatalog()
	groups := map[string]bool{
		ToolGroupBase: true, ToolGroupRAG: true, ToolGroupWikiRead: true,
		ToolGroupWikiEdit: true, ToolGroupWikiIssue: true, ToolGroupData: true,
	}

	require.Len(t, catalog.Tools, len(AvailableToolDefinitions()))
	selectable := make(map[string]bool, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		require.False(t, selectable[tool.Name], "duplicate tool %s", tool.Name)
		selectable[tool.Name] = true
		require.True(t, groups[tool.Group], "tool %s has unknown group %q", tool.Name, tool.Group)
		requirement := ToolCapabilityRequirements[tool.Name]
		require.Equal(t, requirement.AnyOf, nilIfEmpty(tool.Requirement.AnyOf), tool.Name)
		require.Equal(t, requirement.AllOf, nilIfEmpty(tool.Requirement.AllOf), tool.Name)
	}
	// The editor never offered this tool before it read the catalog.
	require.True(t, selectable[ToolSearchConversations])

	for _, name := range catalog.DefaultTools {
		require.True(t, selectable[name], "default tool %s must be selectable", name)
	}
	for _, managed := range catalog.SwitchManagedTools {
		require.False(t, selectable[managed], "switch-managed %s must not be a checkbox", managed)
	}
}

func TestBuildAgentToolCatalogDoesNotShareTheCapabilityMap(t *testing.T) {
	catalog := BuildAgentToolCatalog()
	for _, tool := range catalog.Tools {
		if tool.Name != ToolSearchKnowledge {
			continue
		}
		tool.Requirement.AnyOf[0] = CapGraph
	}
	require.Equal(t, CapVector, ToolCapabilityRequirements[ToolSearchKnowledge].AnyOf[0])
}

func nilIfEmpty(values []KBCapability) []KBCapability {
	if len(values) == 0 {
		return nil
	}
	return values
}
