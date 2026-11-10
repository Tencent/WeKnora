package tools

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- sanitizeName ---

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"lowercase", "MyService", "myservice"},
		{"spaces to underscores", "my service", "my_service"},
		{"hyphens to underscores", "my-service", "my_service"},
		{"strips special chars", "my@service!v2", "myservicev2"},
		{"chinese chars stripped", "危化品查询", ""},
		{"mixed alphanumeric", "svc_123-abc", "svc_123_abc"},
		{"already clean", "my_service_v2", "my_service_v2"},
		{"empty string", "", ""},
		{"only special chars", "@#$%", ""},
		{"unicode mixed", "mcp-服务-test", "mcp__test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeName(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- MCPTool.Name() ---

func newTestMCPTool(serviceName, serviceID, toolName string) *MCPTool {
	return &MCPTool{
		service: &types.MCPService{
			ID:   serviceID,
			Name: serviceName,
		},
		mcpTool: &types.MCPTool{
			Name: toolName,
		},
	}
}

func TestMCPToolName_UsesServiceNameNotUUID(t *testing.T) {
	tool := newTestMCPTool("hazardous_chemicals", "ed606721-b7a5-4e74-8917-40ebfd29f17a", "getHazardousChemicals")

	name := tool.Name()

	assert.Equal(t, "mcp_hazardous_chemicals_gethazardouschemicals", name)
	assert.NotContains(t, name, "ed606721", "tool name must not contain service UUID")
}

func TestMCPToolName_StableAcrossReconnections(t *testing.T) {
	tool1 := newTestMCPTool("my_mcp_service", "aaaaaaaa-1111-2222-3333-444444444444", "doStuff")
	tool2 := newTestMCPTool("my_mcp_service", "bbbbbbbb-5555-6666-7777-888888888888", "doStuff")

	assert.Equal(t, tool1.Name(), tool2.Name(),
		"same service name + tool name should produce the same tool name regardless of UUID")
}

func TestMCPToolName_BasicFormat(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		toolName    string
		expected    string
	}{
		{
			"simple names",
			"weather", "getforecast",
			"mcp_weather_getforecast",
		},
		{
			"names with spaces and hyphens",
			"My Service", "get-data",
			"mcp_my_service_get_data",
		},
		{
			"uppercase normalized",
			"ChemDB", "ListCompounds",
			"mcp_chemdb_listcompounds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := newTestMCPTool(tt.serviceName, "test-id", tt.toolName)
			assert.Equal(t, tt.expected, tool.Name())
		})
	}
}

func TestMCPToolName_ValidPattern(t *testing.T) {
	tool := newTestMCPTool("Test Service-v2", "id-123", "get_data-v1")
	name := tool.Name()

	for _, ch := range name {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_'
		assert.True(t, valid, "character %q is not in [a-z0-9_]", string(ch))
	}
}

func TestMCPToolName_MaxLength(t *testing.T) {
	t.Run("short names within limit", func(t *testing.T) {
		tool := newTestMCPTool("svc", "id", "tool")
		name := tool.Name()
		assert.LessOrEqual(t, len(name), maxFunctionNameLength)
	})

	t.Run("long service name truncated, tool name preserved", func(t *testing.T) {
		longService := strings.Repeat("a", 60)
		toolName := "mytool"
		tool := newTestMCPTool(longService, "id", toolName)
		name := tool.Name()

		assert.LessOrEqual(t, len(name), maxFunctionNameLength,
			"name must not exceed %d chars", maxFunctionNameLength)
		assert.True(t, strings.HasSuffix(name, "_"+toolName),
			"tool name should be preserved at the end: got %q", name)
	})

	t.Run("long tool name causes hard truncation", func(t *testing.T) {
		longTool := strings.Repeat("t", 70)
		tool := newTestMCPTool("svc", "id", longTool)
		name := tool.Name()

		assert.LessOrEqual(t, len(name), maxFunctionNameLength)
	})

	t.Run("both names long", func(t *testing.T) {
		longService := strings.Repeat("s", 50)
		longTool := strings.Repeat("t", 50)
		tool := newTestMCPTool(longService, "id", longTool)
		name := tool.Name()

		assert.LessOrEqual(t, len(name), maxFunctionNameLength)
		assert.True(t, strings.HasPrefix(name, "mcp_"))
	})

	t.Run("exactly at limit", func(t *testing.T) {
		// mcp_ (4) + service + _ (1) + tool = 64
		// service + tool = 59
		serviceName := strings.Repeat("s", 30)
		toolName := strings.Repeat("t", 29)
		tool := newTestMCPTool(serviceName, "id", toolName)
		name := tool.Name()

		assert.LessOrEqual(t, len(name), maxFunctionNameLength)
	})
}

// --- MCPTool.Description() ---

func TestMCPToolDescription(t *testing.T) {
	t.Run("with description", func(t *testing.T) {
		tool := &MCPTool{
			service: &types.MCPService{Name: "ChemDB"},
			mcpTool: &types.MCPTool{Name: "getCompound", Description: "Get chemical compound info"},
		}
		desc := tool.Description()
		assert.Contains(t, desc, "[MCP Service: ChemDB (external)]")
		assert.Contains(t, desc, "Get chemical compound info")
	})

	t.Run("without description falls back to tool name", func(t *testing.T) {
		tool := &MCPTool{
			service: &types.MCPService{Name: "ChemDB"},
			mcpTool: &types.MCPTool{Name: "getCompound"},
		}
		desc := tool.Description()
		assert.Contains(t, desc, "[MCP Service: ChemDB (external)]")
		assert.Contains(t, desc, "getCompound")
	})
}

// --- MCPTool.Parameters() ---

func TestMCPToolParameters(t *testing.T) {
	t.Run("returns provided schema", func(t *testing.T) {
		schema := `{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`
		tool := &MCPTool{
			service: &types.MCPService{Name: "svc"},
			mcpTool: &types.MCPTool{
				Name:        "tool",
				InputSchema: []byte(schema),
			},
		}
		assert.JSONEq(t, schema, string(tool.Parameters()))
	})

	t.Run("returns default schema when none provided", func(t *testing.T) {
		tool := &MCPTool{
			service: &types.MCPService{Name: "svc"},
			mcpTool: &types.MCPTool{Name: "tool"},
		}
		params := tool.Parameters()
		assert.Contains(t, string(params), `"type": "object"`)
	})
}

// --- ToolRegistry integration: name collision (first-wins) ---

func TestToolRegistryFirstWinsPolicy(t *testing.T) {
	registry := NewToolRegistry()

	tool1 := newTestMCPTool("my_service", "id-1", "do_thing")
	tool2 := newTestMCPTool("my_service", "id-2", "do_thing")

	registry.RegisterTool(tool1)
	registry.RegisterTool(tool2)

	got, err := registry.GetTool(tool1.Name())
	require.NoError(t, err)

	mcpGot, ok := got.(*MCPTool)
	require.True(t, ok)
	assert.Equal(t, "id-1", mcpGot.service.ID,
		"first registered tool should win — the second registration must be rejected")
}

func TestToolRegistryMCPToolLookup(t *testing.T) {
	registry := NewToolRegistry()

	tool := newTestMCPTool("hazardous_chemicals", "ed606721-xxxx", "getHazardousChemicalByBizId")
	registry.RegisterTool(tool)

	expectedName := "mcp_hazardous_chemicals_gethazardouschemicalbybizid"

	got, err := registry.GetTool(expectedName)
	require.NoError(t, err, "tool should be found by service-name-based key")
	assert.Equal(t, expectedName, got.Name())

	oldName := "mcp_ed606721_gethazardouschemicalbybizid"
	_, err = registry.GetTool(oldName)
	assert.Error(t, err, "UUID-based tool name must NOT resolve — this was the old bug")
	assert.Contains(t, err.Error(), "tool not found")
}

func TestToolRegistryFunctionDefinitions(t *testing.T) {
	registry := NewToolRegistry()

	tool := newTestMCPTool("weather_api", "svc-id", "getCurrentWeather")
	registry.RegisterTool(tool)

	defs := registry.GetFunctionDefinitions()
	require.Len(t, defs, 1)
	assert.Equal(t, "mcp_weather_api_getcurrentweather", defs[0].Name)
}

// --- extractContentText ---

func TestExtractContentText(t *testing.T) {
	t.Run("empty content", func(t *testing.T) {
		result := extractContentText(nil)
		assert.Equal(t, "Tool executed successfully (no text output)", result)
	})

	t.Run("single text", func(t *testing.T) {
		items := []mcp.ContentItem{{Type: "text", Text: "hello"}}
		result := extractContentText(items)
		assert.Equal(t, "hello", result)
	})

	t.Run("multiple text items joined", func(t *testing.T) {
		items := []mcp.ContentItem{
			{Type: "text", Text: "line1"},
			{Type: "text", Text: "line2"},
		}
		result := extractContentText(items)
		assert.Equal(t, "line1\nline2", result)
	})

	t.Run("image item", func(t *testing.T) {
		items := []mcp.ContentItem{{Type: "image", MimeType: "image/png"}}
		result := extractContentText(items)
		assert.Contains(t, result, "[Image: image/png]")
	})

	t.Run("image with no mime", func(t *testing.T) {
		items := []mcp.ContentItem{{Type: "image"}}
		result := extractContentText(items)
		assert.Contains(t, result, "[Image: image]")
	})

	t.Run("resource item", func(t *testing.T) {
		items := []mcp.ContentItem{{Type: "resource", MimeType: "application/json"}}
		result := extractContentText(items)
		assert.Contains(t, result, "[Resource: application/json]")
	})
}
