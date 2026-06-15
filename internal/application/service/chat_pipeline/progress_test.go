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

type recordingEventBus struct {
	events []types.Event
}

func (b *recordingEventBus) On(types.EventType, types.EventHandler) {}

func (b *recordingEventBus) Emit(_ context.Context, evt types.Event) error {
	b.events = append(b.events, evt)
	return nil
}

func TestIsConsolidatedRetrievalStage(t *testing.T) {
	cm := &types.ChatManage{}
	assert.True(t, IsConsolidatedRetrievalStage(types.CHUNK_SEARCH_PARALLEL, cm))
	assert.False(t, IsConsolidatedRetrievalStage(types.QUERY_UNDERSTAND, cm))
	assert.False(t, IsConsolidatedRetrievalStage(types.LOAD_HISTORY, cm))
}

func TestLastConsolidatedRetrievalStage(t *testing.T) {
	cm := &types.ChatManage{}
	pipeline := []types.EventType{
		types.LOAD_HISTORY,
		types.QUERY_UNDERSTAND,
		types.CHUNK_SEARCH_PARALLEL,
		types.CHUNK_RERANK,
		types.CHUNK_MERGE,
		types.FILTER_TOP_K,
		types.INTO_CHAT_MESSAGE,
		types.CHAT_COMPLETION_STREAM,
	}
	assert.Equal(t, types.FILTER_TOP_K, LastConsolidatedRetrievalStage(pipeline, cm))
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
	bus := &recordingEventBus{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SessionID: "sess-1", EnableRewrite: true},
		PipelineContext: types.PipelineContext{EventBus: bus},
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
	bus := &recordingEventBus{}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{SessionID: "sess-1"},
		PipelineContext: types.PipelineContext{EventBus: bus},
		PipelineState: types.PipelineState{
			MergeResult: []*types.SearchResult{{ID: "r1"}, {ID: "r2"}, {ID: "r3"}},
		},
	}

	start := time.Now()
	progress := BeginRetrievalProgress(context.Background(), cm)
	require.NotNil(t, progress)
	EndRetrievalProgress(context.Background(), cm, progress, start, nil)

	require.Len(t, bus.events, 2)
	assert.Equal(t, types.EventType(event.EventAgentToolCall), bus.events[0].Type)
	assert.Equal(t, types.EventType(event.EventAgentToolResult), bus.events[1].Type)

	callData, ok := bus.events[0].Data.(event.AgentToolCallData)
	require.True(t, ok)
	assert.Equal(t, "knowledge_search", callData.ToolName)

	resultData, ok := bus.events[1].Data.(event.AgentToolResultData)
	require.True(t, ok)
	assert.True(t, resultData.Success)
	assert.Equal(t, 3, resultData.Data["count"])
}
