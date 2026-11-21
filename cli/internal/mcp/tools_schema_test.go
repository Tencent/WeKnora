package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolOutputSchemas_AllToolsAdvertiseExpectedShape(t *testing.T) {
	expectedProperties := map[string][]string{
		"kb_list":       {"items"},
		"kb_view":       {"id", "name", "chunking_config"},
		"doc_list":      {"items", "page", "page_size", "total"},
		"doc_view":      {"id", "file_name", "metadata"},
		"doc_download":  {"doc_id", "file_name", "bytes", "content", "is_base64"},
		"search_chunks": {"results"},
		"chat":          {"events", "session_id", "kb_id", "query"},
		"agent_list":    {"items"},
		"session_ask":   {"events", "session_id", "agent_id", "query"},
		"chunk_list":    {"chunks", "total", "truncated_at_limit"},
	}

	c, _ := newTestServer(t, &fakeSvc{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.ListTools(ctx, nil)
	require.NoError(t, err)

	tools := make(map[string]*mcpsdk.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	for name, properties := range expectedProperties {
		t.Run(name, func(t *testing.T) {
			tool := tools[name]
			require.NotNil(t, tool)
			schema := decodeToolOutputSchema(t, tool)
			require.Equal(t, "object", schema.Type)
			for _, property := range properties {
				require.Contains(t, schema.Properties, property)
			}
			_, err := schema.Resolve(nil)
			require.NoError(t, err)
		})
	}
}

func decodeToolOutputSchema(t *testing.T, tool *mcpsdk.Tool) *jsonschema.Schema {
	t.Helper()
	require.NotNil(t, tool.OutputSchema, "tool %q must advertise an output schema", tool.Name)
	data, err := json.Marshal(tool.OutputSchema)
	require.NoError(t, err)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(data, &schema))
	return &schema
}

func validateToolOutput(t *testing.T, c *mcpsdk.ClientSession, name string, result *mcpsdk.CallToolResult) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.ListTools(ctx, nil)
	require.NoError(t, err)
	var tool *mcpsdk.Tool
	for _, candidate := range res.Tools {
		if candidate.Name == name {
			tool = candidate
			break
		}
	}
	require.NotNil(t, tool)
	schema := decodeToolOutputSchema(t, tool)
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err)
	data, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var output any
	require.NoError(t, json.Unmarshal(data, &output))
	require.NoError(t, resolved.Validate(output), "tool %q returned output that violates its schema", name)
}
