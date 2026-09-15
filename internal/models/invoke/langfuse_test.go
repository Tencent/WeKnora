package invoke

// langfuse_test.go: ports the v1 chat/langfuse_wrapper_test.go tool-metadata
// coverage (upstream 2026-09) onto the entry-builtin metadata builder.

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildLangfuseToolMetadataIncludesMCPCatalog(t *testing.T) {
	meta := buildLangfuseToolMetadata(&ChatOptions{
		Tools: []ToolDef{
			{Name: "knowledge_search", Description: "search kb"},
			{
				Name:        langfuseDiscoverMCPTool,
				Description: "MCP tools are available without an @mention.\n{\"server_id\":\"svc\"}",
			},
			{Name: "call_mcp_tool", Description: "call"},
		},
	})
	assert.Equal(t, true, meta["has_tools"])
	assert.Equal(t, []string{"knowledge_search", langfuseDiscoverMCPTool, "call_mcp_tool"}, meta["tool_names"])
	catalog, _ := meta["mcp_catalog"].(string)
	assert.Contains(t, catalog, `"server_id":"svc"`)
}

func TestBuildLangfuseToolMetadataEmpty(t *testing.T) {
	assert.Nil(t, buildLangfuseToolMetadata(nil))
	assert.Nil(t, buildLangfuseToolMetadata(&ChatOptions{}))
}

func TestTruncateRunesAppendsEllipsis(t *testing.T) {
	assert.Equal(t, "一二三...", truncateRunes("一二三四五", 3))
	assert.Equal(t, "一二三", truncateRunes("一二三", 3))
}
