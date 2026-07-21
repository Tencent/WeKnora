package chatpipeline

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingBus struct {
	events []types.Event
}

func (b *recordingBus) On(types.Type, types.Handler) {}

func (b *recordingBus) Emit(_ context.Context, evt types.Event) error {
	b.events = append(b.events, evt)
	return nil
}

func TestIsConsolidatedRetrievalStage(t *testing.T) {
	cm := &types.ChatManage{}
	assert.True(t, IsConsolidatedRetrievalStage(types.ChunkSearchParallel, cm))
	assert.False(t, IsConsolidatedRetrievalStage(types.QueryUnderstand, cm))
	assert.False(t, IsConsolidatedRetrievalStage(types.LoadHistory, cm))
}

func TestLastConsolidatedRetrievalStage(t *testing.T) {
	cm := &types.ChatManage{}
	pipeline := []types.Type{
		types.LoadHistory,
		types.QueryUnderstand,
		types.ChunkSearchParallel,
		types.ChunkRerank,
		types.ChunkMerge,
		types.FilterTopK,
		types.IntoChatMessage,
		types.ChatCompletionStream,
	}
	assert.Equal(t, types.FilterTopK, LastConsolidatedRetrievalStage(pipeline, cm))
}

func TestShouldCloseRetrievalProgress(t *testing.T) {
	last := types.FilterTopK

	// Normal completion: only the last retrieval stage closes the window.
	assert.True(t, ShouldCloseRetrievalProgress(types.FilterTopK, last, nil))
	assert.False(t, ShouldCloseRetrievalProgress(types.ChunkSearchParallel, last, nil))

	// ErrSearchNothing at an earlier retrieval stage must still close the
	// window so the frontend stops spinning before the fallback answer streams.
	assert.True(t, ShouldCloseRetrievalProgress(types.ChunkSearchParallel, last, ErrSearchNothing))

	// A hard error at any retrieval stage must also close the window.
	assert.True(t, ShouldCloseRetrievalProgress(types.ChunkRerank, last, &PluginError{}))
}

func TestShouldEmitQueryUnderstandProgress(t *testing.T) {
	cm := &types.ChatManage{PipelineRequest: types.PipelineRequest{EnableRewrite: true}}
	assert.True(t, ShouldEmitQueryUnderstandProgress(cm))

	cm.EnableRewrite = false
	assert.False(t, ShouldEmitQueryUnderstandProgress(cm))

	cm.Images = []string{"data:image/png;base64,abc"}
	assert.True(t, ShouldEmitQueryUnderstandProgress(cm))
}

func TestQueryUnderstandProgressEmitsToolCallAndResult(t *testing.T) {
	bus := &recordingBus{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SessionID: "sess-1", EnableRewrite: true},
		PipelineContext: types.PipelineContext{Bus: bus},
	}

	start := time.Now()
	progress := BeginQueryUnderstandProgress(context.Background(), cm)
	require.NotNil(t, progress)
	EndQueryUnderstandProgress(context.Background(), cm, progress, start, nil)

	require.Len(t, bus.events, 2)
	callData, ok := bus.events[0].Data.(event.AgentToolCallData)
	require.True(t, ok)
	assert.Equal(t, "query_understand", callData.ToolName)
}

func TestRetrievalProgressEmitsSingleToolCallAndResult(t *testing.T) {
	bus := &recordingBus{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SessionID: "sess-1"},
		PipelineContext: types.PipelineContext{Bus: bus},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{{ID: "r1"}, {ID: "r2"}, {ID: "r3"}},
		},
	}

	start := time.Now()
	progress := BeginRetrievalProgress(context.Background(), cm)
	require.NotNil(t, progress)
	EndRetrievalProgress(context.Background(), cm, progress, start, nil)

	require.Len(t, bus.events, 2)
	assert.Equal(t, types.Type(event.EventAgentToolCall), bus.events[0].Type)
	assert.Equal(t, types.Type(event.EventAgentToolResult), bus.events[1].Type)

	callData, ok := bus.events[0].Data.(event.AgentToolCallData)
	require.True(t, ok)
	assert.Equal(t, "knowledge_search", callData.ToolName)
	assert.Equal(t, retrievalSourceKnowledge, callData.Arguments["search_source"])

	resultData, ok := bus.events[1].Data.(event.AgentToolResultData)
	require.True(t, ok)
	assert.True(t, resultData.Success)
	assert.Equal(t, 3, resultData.Data["count"])
	assert.Equal(t, 3, resultData.Data["doc_count"])
	assert.Equal(t, 0, resultData.Data["web_count"])
	assert.Equal(t, retrievalSourceKnowledge, resultData.Data["search_source"])
}

func TestRetrievalProgressWebOnlySearchSource(t *testing.T) {
	bus := &recordingBus{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			SessionID:        "sess-web",
			WebSearchEnabled: true,
		},
		PipelineContext: types.PipelineContext{Bus: bus},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{
				{ID: "w1", ChunkType: "web_search"},
				{ID: "w2", KnowledgeSource: "web_search"},
			},
		},
	}

	start := time.Now()
	progress := BeginRetrievalProgress(context.Background(), cm)
	require.NotNil(t, progress)
	EndRetrievalProgress(context.Background(), cm, progress, start, nil)

	callData, ok := bus.events[0].Data.(event.AgentToolCallData)
	require.True(t, ok)
	assert.Equal(t, retrievalSourceWeb, callData.Arguments["search_source"])

	resultData, ok := bus.events[1].Data.(event.AgentToolResultData)
	require.True(t, ok)
	assert.Equal(t, 2, resultData.Data["count"])
	assert.Equal(t, 0, resultData.Data["doc_count"])
	assert.Equal(t, 2, resultData.Data["web_count"])
	assert.Equal(t, retrievalSourceWeb, resultData.Data["search_source"])
}
