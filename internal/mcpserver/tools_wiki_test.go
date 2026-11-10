package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestWithOutputInDataAddsWikiContent(t *testing.T) {
	const output = "<page slug=\"concept/example\">Page body</page>"
	result := withOutputInData(&types.ToolResult{
		Success: true,
		Output:  output,
		Data:    map[string]interface{}{"found_kbs": map[string][]string{"concept/example": {"kb-1"}}},
	})

	if got := result.Data["content"]; got != output {
		t.Fatalf("content = %v, want %q", got, output)
	}
	if got := result.Data["found_kbs"]; got == nil {
		t.Fatal("withOutputInData removed existing structured data")
	}
}

func TestWithOutputInDataInitializesStructuredData(t *testing.T) {
	const output = "page body"
	result := withOutputInData(&types.ToolResult{Success: true, Output: output})

	if result.Data == nil {
		t.Fatal("withOutputInData left structured data nil")
	}
	if got := result.Data["content"]; got != output {
		t.Fatalf("content = %v, want %q", got, output)
	}
}

func TestWithOutputInDataDoesNotOverwriteExistingContent(t *testing.T) {
	const existing = "already structured"
	result := withOutputInData(&types.ToolResult{
		Success: true,
		Output:  "human-readable output",
		Data:    map[string]interface{}{"content": existing},
	})

	if got := result.Data["content"]; got != existing {
		t.Fatalf("content = %v, want %q", got, existing)
	}
}

func TestWithOutputInDataLeavesNonSuccessfulResultsUnchanged(t *testing.T) {
	failed := &types.ToolResult{Output: "failure output", Data: map[string]interface{}{}}
	if got := withOutputInData(failed); got != failed {
		t.Fatal("non-successful result was replaced")
	}
	if _, exists := failed.Data["content"]; exists {
		t.Fatal("non-successful result received structured content")
	}

	empty := &types.ToolResult{Success: true, Data: map[string]interface{}{}}
	if got := withOutputInData(empty); got != empty {
		t.Fatal("empty-output result was replaced")
	}
	if _, exists := empty.Data["content"]; exists {
		t.Fatal("empty-output result received structured content")
	}
}

func TestWikiResultIncludesOutputInStructuredContent(t *testing.T) {
	const output = "<page slug=\"concept/example\">Page body</page>"
	result := toolResultFromAgentTool(withOutputInData(&types.ToolResult{
		Success: true,
		Output:  output,
		Data:    map[string]interface{}{"found_kbs": map[string][]string{"concept/example": {"kb-1"}}},
	}), nil)

	structured, ok := result.StructuredContent.(map[string]interface{})
	if !ok {
		t.Fatalf("structured content has type %T, want map[string]interface{}", result.StructuredContent)
	}
	if got := structured["content"]; got != output {
		t.Fatalf("structured content = %v, want %q", got, output)
	}

	if len(result.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(result.Content))
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content block has type %T, want mcp.TextContent", result.Content[0])
	}
	if text.Text != output {
		t.Fatalf("text content = %q, want %q", text.Text, output)
	}

	wire, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal MCP result: %v", err)
	}
	var payload struct {
		Content           []map[string]interface{} `json:"content"`
		StructuredContent map[string]interface{}   `json:"structuredContent"`
	}
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatalf("unmarshal MCP result: %v", err)
	}
	if got := payload.StructuredContent["content"]; got != output {
		t.Fatalf("wire structured content = %v, want %q", got, output)
	}
	if len(payload.Content) != 1 || payload.Content[0]["text"] != output {
		t.Fatalf("wire text content = %v, want %q", payload.Content, output)
	}
}

func TestGenericToolResultDoesNotGainWikiContent(t *testing.T) {
	result := toolResultFromAgentTool(&types.ToolResult{
		Success: true,
		Output:  "document output",
		Data:    map[string]interface{}{"document": "structured document"},
	}, nil)

	structured, ok := result.StructuredContent.(map[string]interface{})
	if !ok {
		t.Fatalf("structured content has type %T, want map[string]interface{}", result.StructuredContent)
	}
	if _, exists := structured["content"]; exists {
		t.Fatal("generic tool result unexpectedly gained wiki content")
	}
}
