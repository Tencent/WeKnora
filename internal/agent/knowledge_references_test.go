package agent

import (
	"context"
	"encoding/json"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type knowledgeReferenceTool struct {
	agenttools.BaseTool
	references []*types.SearchResult
}

func newKnowledgeReferenceTool(references []*types.SearchResult) *knowledgeReferenceTool {
	return &knowledgeReferenceTool{
		BaseTool: agenttools.NewBaseTool(
			agenttools.ToolKnowledgeSearch,
			"test retrieval tool",
			json.RawMessage(`{"type":"object"}`),
		),
		references: references,
	}
}

func (t *knowledgeReferenceTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return &types.ToolResult{
		Success:             true,
		Output:              "retrieved evidence",
		KnowledgeReferences: t.references,
	}, nil
}

func TestExecuteLoopPersistsKnowledgeReferencesFromRetrievalTools(t *testing.T) {
	reference := &types.SearchResult{
		ID:              "chunk-agent-1",
		Content:         "grounding evidence",
		KnowledgeID:     "document-1",
		KnowledgeBaseID: "kb-1",
		SubChunkID:      []string{"subchunk-agent-1"},
		MatchType:       types.MatchTypeEmbedding,
	}
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID:   "call-knowledge-search",
				Type: "function",
				Function: types.FunctionCall{
					Name:      agenttools.ToolKnowledgeSearch,
					Arguments: `{}`,
				},
			}},
			Done:         true,
			FinishReason: "tool_calls",
		}}},
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "answer grounded in the retrieved chunk",
			Done:         true,
			FinishReason: "stop",
		}}},
	}}

	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(newKnowledgeReferenceTool([]*types.SearchResult{reference}))

	var completion event.AgentCompleteData
	engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		completion = evt.Data.(event.AgentCompleteData)
		return nil
	})

	state := &types.AgentState{}
	_, err := engine.executeLoop(
		context.Background(),
		state,
		"question",
		emptyMessages(),
		engine.buildToolsForLLM(),
		"session-1",
		"message-1",
	)
	require.NoError(t, err)
	require.Equal(t, []*types.SearchResult{reference}, state.KnowledgeRefs)
	require.Len(t, completion.KnowledgeRefs, 1)
	require.Same(t, reference, completion.KnowledgeRefs[0])
}

func TestCollectAgentKnowledgeReferencesDeduplicatesAndSkipsFailedOrExternalResults(t *testing.T) {
	existing := &types.SearchResult{ID: "chunk-1", SubChunkID: []string{"subchunk-1"}}
	duplicateWithAnotherSubChunk := &types.SearchResult{
		ID:         "chunk-1",
		SubChunkID: []string{"subchunk-1", "subchunk-1b"},
	}
	added := &types.SearchResult{ID: "chunk-2", SubChunkID: []string{"subchunk-2"}}
	state := &types.AgentState{KnowledgeRefs: []*types.SearchResult{existing}}

	collectAgentKnowledgeReferences(state, []types.ToolCall{
		{Result: &types.ToolResult{
			Success:             true,
			KnowledgeReferences: []*types.SearchResult{duplicateWithAnotherSubChunk, nil, {ID: ""}, added},
		}},
		{Result: &types.ToolResult{
			Success:             false,
			KnowledgeReferences: []*types.SearchResult{{ID: "chunk-failed"}},
		}},
		{Result: &types.ToolResult{
			Success: true,
			KnowledgeReferences: []*types.SearchResult{{
				ID:        "web-result",
				MatchType: types.MatchTypeWebSearch,
			}},
		}},
	})

	require.Equal(t, []*types.SearchResult{existing, added}, state.KnowledgeRefs)
	require.Equal(t, []string{"subchunk-1", "subchunk-1b"}, existing.SubChunkID)
}
