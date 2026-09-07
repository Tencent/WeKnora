package agent

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestAppendKnowledgeRefsFromStepCollectsSearchResults(t *testing.T) {
	state := &types.AgentState{}
	appendKnowledgeRefsFromStep(state, types.AgentStep{
		ToolCalls: []types.ToolCall{{
			Name: "knowledge_search",
			Result: &types.ToolResult{
				Success: true,
				Data: map[string]interface{}{
					"results": []map[string]interface{}{{
						"id":                "chunk-1",
						"content":           "body",
						"knowledge_id":      "knowledge-1",
						"knowledge_base_id": "kb-1",
						"images": []map[string]interface{}{{
							"url": "resource://ShArEdKbHaNdLe00000000",
						}},
					}},
				},
			},
		}},
	})

	if len(state.KnowledgeRefs) != 1 {
		t.Fatalf("len(KnowledgeRefs)=%d, want 1", len(state.KnowledgeRefs))
	}
	ref := state.KnowledgeRefs[0]
	if ref.ID != "chunk-1" || ref.KnowledgeBaseID != "kb-1" || ref.Content != "body" {
		t.Fatalf("ref = %+v", ref)
	}
	if !strings.Contains(ref.ImageInfo, "resource://ShArEdKbHaNdLe00000000") {
		t.Fatalf("ImageInfo = %q, want resource handle", ref.ImageInfo)
	}

	appendKnowledgeRefsFromStep(state, types.AgentStep{
		ToolCalls: []types.ToolCall{{
			Result: &types.ToolResult{
				Data: map[string]interface{}{
					"results": []map[string]interface{}{{
						"id":                "chunk-1",
						"content":           "body",
						"knowledge_base_id": "kb-1",
					}},
				},
			},
		}},
	})
	if len(state.KnowledgeRefs) != 1 {
		t.Fatalf("duplicate refs appended: %d", len(state.KnowledgeRefs))
	}
}

func TestAppendKnowledgeRefsFromStepUsesParentKnowledgeBase(t *testing.T) {
	state := &types.AgentState{}
	appendKnowledgeRefsFromStep(state, types.AgentStep{
		ToolCalls: []types.ToolCall{{
			Result: &types.ToolResult{
				Data: map[string]interface{}{
					"knowledge_id":   "knowledge-1",
					"knowledge_base": "kb-parent",
					"chunks": []interface{}{
						map[string]interface{}{"content": "![img](resource://AbCdEfGhIjKlMnOpQrStUv)"},
					},
				},
			},
		}},
	})
	if len(state.KnowledgeRefs) != 1 {
		t.Fatalf("len(KnowledgeRefs)=%d, want 1", len(state.KnowledgeRefs))
	}
	if state.KnowledgeRefs[0].KnowledgeBaseID != "kb-parent" {
		t.Fatalf("KnowledgeBaseID=%q", state.KnowledgeRefs[0].KnowledgeBaseID)
	}
	if state.KnowledgeRefs[0].KnowledgeID != "knowledge-1" {
		t.Fatalf("KnowledgeID=%q", state.KnowledgeRefs[0].KnowledgeID)
	}
}
