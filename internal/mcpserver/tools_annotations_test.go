package mcpserver

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestReadOnlyEndpointToolsAreNotDestructive(t *testing.T) {
	for _, tool := range []mcp.Tool{
		listKnowledgeBasesTool(),
		searchKnowledgeTool(),
		grepChunksTool(),
		listDocumentsTool(),
		readDocumentTool(),
		wikiSearchTool(),
		wikiReadPageTool(),
		wikiIndexTool(),
	} {
		ann := tool.Annotations
		if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
			t.Fatalf("%s must be read-only", tool.Name)
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Fatalf("%s must not be destructive", tool.Name)
		}
		if ann.IdempotentHint == nil || !*ann.IdempotentHint {
			t.Fatalf("%s must be idempotent", tool.Name)
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Fatalf("%s must not be open-world", tool.Name)
		}
	}
}

func TestMutatingEndpointToolsStayDestructive(t *testing.T) {
	ask := askTool()
	if ask.Annotations.ReadOnlyHint == nil || *ask.Annotations.ReadOnlyHint {
		t.Fatal("ask must not be read-only")
	}
	if ask.Annotations.DestructiveHint == nil || !*ask.Annotations.DestructiveHint {
		t.Fatal("ask keeps the default destructive hint")
	}
	for _, tool := range []mcp.Tool{
		addDocumentTool(),
		updateDocumentTool(),
		deleteDocumentTool(),
	} {
		if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Fatalf("%s must stay destructive", tool.Name)
		}
	}
}
