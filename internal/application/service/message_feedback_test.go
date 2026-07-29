package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// noOpAdd stands in for the add callback extractChunkRefs passes in.
// extractChunkRefsFromAgentSteps currently builds its own refs slice and
// never invokes add, but a real func avoids a nil panic if that changes.
func noOpAdd(_, _ string, _ bool) {}

func newAgentMsgWithSteps(steps ...types.AgentStep) *types.Message {
	return &types.Message{
		ID:          "msg-1",
		SessionID:   "sess-1",
		Role:        "assistant",
		KnowledgeID: "kb-doc-1",
		AgentSteps:  types.AgentSteps(steps),
	}
}

func toolCallWithResult(name string, data map[string]interface{}) types.ToolCall {
	return types.ToolCall{
		ID:   "tc-1",
		Name: name,
		Result: &types.ToolResult{
			Success: true,
			Data:    data,
		},
	}
}

// chunkRefItem builds a _chunk_refs entry as map[string]interface{} — the
// shape produced by JSON unmarshal of the persisted tool Data.
func chunkRefItem(chunkID, kbID string) map[string]interface{} {
	return map[string]interface{}{
		"chunk_id":          chunkID,
		"knowledge_base_id": kbID,
	}
}

// resultItem builds a knowledge_search/hybrid_search results entry. Empty
// kbID/knowledgeID are omitted to mimic the merged-result shape where those
// fields are sometimes absent.
func resultItem(chunkID, kbID, knowledgeID string) map[string]interface{} {
	m := map[string]interface{}{"chunk_id": chunkID}
	if kbID != "" {
		m["knowledge_base_id"] = kbID
	}
	if knowledgeID != "" {
		m["knowledge_id"] = knowledgeID
	}
	return m
}

// TestExtractChunkRefsFromAgentSteps_ChunkRefsPath covers the generic
// _chunk_refs attribution path used by grep_chunks / list_knowledge_chunks
// after SanitizeToolDataForPersist strips the bulky chunk payload.
func TestExtractChunkRefsFromAgentSteps_ChunkRefsPath(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		Iteration: 0,
		ToolCalls: []types.ToolCall{
			toolCallWithResult("grep_chunks", map[string]interface{}{
				"_chunk_refs": []interface{}{
					chunkRefItem("chunk-a", "kb-1"),
					chunkRefItem("chunk-b", "kb-1"),
				},
			}),
		},
	})
	seen := make(map[string]bool)
	refs := extractChunkRefsFromAgentSteps(42, msg, seen, noOpAdd)

	require.Len(t, refs, 2)
	assert.Equal(t, "chunk-a", refs[0].ChunkID)
	assert.Equal(t, "kb-1", refs[0].KnowledgeBaseID)
	assert.Equal(t, uint64(42), refs[0].TenantID)
	assert.Equal(t, "msg-1", refs[0].MessageID)
	assert.Equal(t, "sess-1", refs[0].SessionID)
	assert.Equal(t, "kb-doc-1", refs[0].KnowledgeID)
	assert.False(t, refs[0].IsSubChunk)
	assert.Equal(t, "chunk-b", refs[1].ChunkID)
	assert.True(t, seen["chunk-a"] && seen["chunk-b"], "seen map must record extracted chunks")
}

// TestExtractChunkRefsFromAgentSteps_ChunkRefsMapStringSliceShape covers the
// []map[string]interface{} slice shape produced by go-json/jsonpb interop.
// toInterfaceSlice normalises this to []interface{} before iteration.
func TestExtractChunkRefsFromAgentSteps_ChunkRefsMapStringSliceShape(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("list_knowledge_chunks", map[string]interface{}{
				"_chunk_refs": []map[string]interface{}{
					chunkRefItem("chunk-ms", "kb-ms"),
				},
			}),
		},
	})
	refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
	require.Len(t, refs, 1)
	assert.Equal(t, "chunk-ms", refs[0].ChunkID)
	assert.Equal(t, "kb-ms", refs[0].KnowledgeBaseID)
}

// TestExtractChunkRefsFromAgentSteps_KnowledgeSearchPath covers the
// knowledge_search tool branch that reads chunk_id directly from Data["results"].
func TestExtractChunkRefsFromAgentSteps_KnowledgeSearchPath(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("knowledge_search", map[string]interface{}{
				"results": []interface{}{
					resultItem("chunk-k1", "kb-2", ""),
					resultItem("chunk-k2", "kb-2", ""),
				},
			}),
		},
	})
	refs := extractChunkRefsFromAgentSteps(7, msg, make(map[string]bool), noOpAdd)

	require.Len(t, refs, 2)
	assert.Equal(t, "chunk-k1", refs[0].ChunkID)
	assert.Equal(t, "kb-2", refs[0].KnowledgeBaseID)
	assert.Equal(t, uint64(7), refs[0].TenantID)
	assert.Equal(t, "chunk-k2", refs[1].ChunkID)
}

// TestExtractChunkRefsFromAgentSteps_HybridSearchPath covers the hybrid_search
// alias — it shares the results-reading branch with knowledge_search.
func TestExtractChunkRefsFromAgentSteps_HybridSearchPath(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("hybrid_search", map[string]interface{}{
				"results": []interface{}{
					resultItem("chunk-h1", "kb-3", ""),
				},
			}),
		},
	})
	refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
	require.Len(t, refs, 1)
	assert.Equal(t, "chunk-h1", refs[0].ChunkID)
	assert.Equal(t, "kb-3", refs[0].KnowledgeBaseID)
}

// TestExtractChunkRefsFromAgentSteps_KbIndexFallback covers the case where a
// knowledge_search result row omits knowledge_base_id but carries a
// knowledge_id that can be resolved through the tool's knowledge_results rollup.
func TestExtractChunkRefsFromAgentSteps_KbIndexFallback(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("knowledge_search", map[string]interface{}{
				"results": []interface{}{
					// No knowledge_base_id on the chunk row — must be resolved
					// via knowledge_results below.
					resultItem("chunk-fb", "", "doc-77"),
				},
				"knowledge_results": []interface{}{
					map[string]interface{}{
						"knowledge_id":      "doc-77",
						"knowledge_base_id": "kb-fb",
					},
				},
			}),
		},
	})
	refs := extractChunkRefsFromAgentSteps(9, msg, make(map[string]bool), noOpAdd)
	require.Len(t, refs, 1)
	assert.Equal(t, "chunk-fb", refs[0].ChunkID)
	assert.Equal(t, "kb-fb", refs[0].KnowledgeBaseID,
		"knowledge_base_id must be resolved from knowledge_results rollup")
}

// TestExtractChunkRefsFromAgentSteps_DedupViaSeen ensures chunks already
// present in the shared seen map (e.g. credited via KnowledgeReferences in
// the caller) are not double-counted, and that chunks added here also mark
// the seen map so later steps cannot re-add them.
func TestExtractChunkRefsFromAgentSteps_DedupViaSeen(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("grep_chunks", map[string]interface{}{
				"_chunk_refs": []interface{}{
					chunkRefItem("chunk-dup", "kb-1"),
					chunkRefItem("chunk-new", "kb-1"),
				},
			}),
			toolCallWithResult("knowledge_search", map[string]interface{}{
				"results": []interface{}{
					resultItem("chunk-dup", "kb-1", ""),
					resultItem("chunk-new", "kb-1", ""),
					resultItem("chunk-ks", "kb-1", ""),
				},
			}),
		},
	})
	// Pre-seed seen with chunk-dup as if the caller had already credited it.
	seen := map[string]bool{"chunk-dup": true}
	refs := extractChunkRefsFromAgentSteps(1, msg, seen, noOpAdd)

	// chunk-dup is skipped (already seen); chunk-new is added once (the
	// knowledge_search repeat is skipped because extractChunkRefsFromAgentSteps
	// marked it seen); chunk-ks is added.
	require.Len(t, refs, 2)
	assert.Equal(t, "chunk-new", refs[0].ChunkID)
	assert.Equal(t, "chunk-ks", refs[1].ChunkID)
	assert.True(t, seen["chunk-new"] && seen["chunk-ks"])
}

// TestExtractChunkRefsFromAgentSteps_SkipsInvalidToolResults verifies the
// guard clauses: nil Result, unsuccessful Result, and nil Data all yield no
// refs instead of panicking.
func TestExtractChunkRefsFromAgentSteps_SkipsInvalidToolResults(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		msg := newAgentMsgWithSteps(types.AgentStep{
			ToolCalls: []types.ToolCall{{ID: "tc", Name: "grep_chunks"}}, // Result is nil
		})
		refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
		assert.Empty(t, refs)
	})
	t.Run("unsuccessful result", func(t *testing.T) {
		msg := newAgentMsgWithSteps(types.AgentStep{
			ToolCalls: []types.ToolCall{{
				ID:   "tc",
				Name: "grep_chunks",
				Result: &types.ToolResult{
					Success: false,
					Data:    map[string]interface{}{"_chunk_refs": []interface{}{chunkRefItem("c", "kb")}},
				},
			}},
		})
		refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
		assert.Empty(t, refs)
	})
	t.Run("nil data", func(t *testing.T) {
		msg := newAgentMsgWithSteps(types.AgentStep{
			ToolCalls: []types.ToolCall{{
				ID:     "tc",
				Name:   "grep_chunks",
				Result: &types.ToolResult{Success: true}, // Data is nil
			}},
		})
		refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
		assert.Empty(t, refs)
	})
}

// TestExtractChunkRefsFromAgentSteps_SkipsEmptyIDs ensures rows missing
// chunk_id or knowledge_base_id (and unresolvable via kb_index) are dropped.
func TestExtractChunkRefsFromAgentSteps_SkipsEmptyIDs(t *testing.T) {
	msg := newAgentMsgWithSteps(types.AgentStep{
		ToolCalls: []types.ToolCall{
			toolCallWithResult("grep_chunks", map[string]interface{}{
				"_chunk_refs": []interface{}{
					chunkRefItem("", "kb-1"),    // empty chunk_id
					chunkRefItem("chunk-x", ""), // empty kb_id
					chunkRefItem("chunk-ok", "kb-1"),
				},
			}),
			toolCallWithResult("knowledge_search", map[string]interface{}{
				"results": []interface{}{
					resultItem("", "kb-1", ""),               // empty chunk_id
					resultItem("chunk-y", "", ""),            // empty kb_id, no knowledge_id to resolve
					resultItem("chunk-z", "", "doc-missing"), // kb_id missing, knowledge_id not in rollup
				},
			}),
		},
	})
	refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
	require.Len(t, refs, 1)
	assert.Equal(t, "chunk-ok", refs[0].ChunkID)
}

// TestExtractChunkRefsFromAgentSteps_EmptyAgentSteps ensures an empty
// AgentSteps list yields no refs (the fallback is a no-op).
func TestExtractChunkRefsFromAgentSteps_EmptyAgentSteps(t *testing.T) {
	msg := newAgentMsgWithSteps() // no steps
	refs := extractChunkRefsFromAgentSteps(1, msg, make(map[string]bool), noOpAdd)
	assert.Empty(t, refs)
}
