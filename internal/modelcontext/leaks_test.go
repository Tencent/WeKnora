package modelcontext

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	leakDocID   = "3f1c2a8e-0000-4000-8000-000000000001"
	leakChunkID = "3f1c2a8e-0000-4000-8000-000000000002"
	leakRawID   = "3f1c2a8e-0000-4000-8000-0000000000ff"
)

func TestLeakedIdentifiersIgnoresEncodedHandlesAndUserText(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterChunk(ChunkReference{ChunkID: leakChunkID, KnowledgeID: leakDocID, KnowledgeBaseID: "kb-1"})
	messages := registry.EncodeMessages([]chat.Message{
		{Role: "system", Content: "directory: " + registry.ModelToolResult(searchResultTool(leakChunkID, leakDocID))},
		{Role: "user", Content: "look at " + leakRawID + " please"},
		{Role: "assistant", Content: `<kb doc="` + leakDocID + `" chunk_id="` + leakChunkID + `" kb_id="kb-1"/>`},
	})
	if leaks := registry.LeakedIdentifiers(messages); len(leaks) != 0 {
		t.Fatalf("encoded messages must not report leaks, got %s", SummarizeLeaks(leaks))
	}
}

func TestLeakedIdentifiersClassifiesRegisteredAndUnregistered(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterDocument(leakDocID)
	messages := []chat.Message{
		{Role: "user", Content: leakRawID},
		{
			Role: "tool", Name: "shell_exec",
			Content: "wrote summary/" + leakRawID + " and " + strings.ToUpper(leakRawID),
		},
		{Role: "assistant", Content: "Document " + leakDocID + " is relevant.", ToolCalls: []chat.ToolCall{{
			Function: chat.FunctionCall{Name: "wiki_write_page", Arguments: `{"source_refs":["` + leakDocID + `|T"]}`},
		}}},
		{Role: "assistant", MultiContent: []chat.MessageContentPart{{Type: "text", Text: "see " + leakRawID}}},
	}
	leaks := registry.LeakedIdentifiers(messages)
	if len(leaks) != 4 {
		t.Fatalf("expected 4 leaking fields, got %d: %s", len(leaks), SummarizeLeaks(leaks))
	}
	tool := leaks[0]
	if tool.Role != "tool" || tool.ToolName != "shell_exec" || tool.Field != "content" {
		t.Fatalf("unexpected first leak origin: %+v", tool)
	}
	if len(tool.Registered) != 0 || len(tool.Unregistered) != 1 {
		t.Fatalf("case-insensitive duplicates must collapse to one unregistered ID: %+v", tool)
	}
	content := leaks[1]
	if content.Field != "content" || len(content.Registered) != 1 || content.Registered[0] != leakDocID {
		t.Fatalf("registered document in assistant prose must be reported as registered: %+v", content)
	}
	args := leaks[2]
	if args.Field != "tool_call_arguments" || args.ToolName != "wiki_write_page" || len(args.Registered) != 1 {
		t.Fatalf("tool call arguments must be scanned per call: %+v", args)
	}
	if leaks[3].Field != "multi_content" {
		t.Fatalf("multi-part text must be scanned: %+v", leaks[3])
	}

	summary := SummarizeLeaks(leaks)
	for _, want := range []string{
		"message[1] tool/shell_exec content: 0 registered [], 1 unregistered [" + leakRawID + "]",
		"message[2] assistant content: 1 registered [" + leakDocID + "], 0 unregistered []",
		"message[2] assistant/wiki_write_page tool_call_arguments",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestLeakedIdentifiersDoesNotMatchHandlesOrPartialUUIDs(t *testing.T) {
	registry := NewRegistry(true)
	messages := []chat.Message{
		{Role: "assistant", Content: "c1 d2 b3 w4 res://0001 i1 and k_3f1c2a8e_0000_4000_8000_000000000001"},
		{Role: "tool", Name: "x", Content: "3f1c2a8e-0000-4000-8000-00000000000"}, // one hex digit short
	}
	if leaks := registry.LeakedIdentifiers(messages); len(leaks) != 0 {
		t.Fatalf("handles and non-UUID tokens must not be reported: %s", SummarizeLeaks(leaks))
	}
	if SummarizeLeaks(nil) != "" {
		t.Fatal("empty leak list must summarize to an empty string")
	}
	if got := sampleIDs([]string{"a", "b", "c", "d"}); got != "[a b c ...]" {
		t.Fatalf("sample truncation = %q", got)
	}
}

func searchResultTool(chunkID, docID string) *types.ToolResult {
	return &types.ToolResult{
		Success: true,
		Output:  "raw " + chunkID,
		Data: map[string]interface{}{
			"display_type": "search_results",
			"results": []map[string]interface{}{{
				"chunk_id":          chunkID,
				"knowledge_id":      docID,
				"knowledge_base_id": "kb-1",
				"knowledge_title":   "Doc",
				"chunk_index":       1,
				"content":           "body mentioning " + docID,
			}},
		},
	}
}
