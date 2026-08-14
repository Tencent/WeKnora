package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func emitTimelineStage(
	t *testing.T,
	bus *event.EventBus,
	toolCallID, toolName string,
	args map[string]any,
	result event.AgentToolResultData,
) {
	t.Helper()
	require.NoError(t, bus.Emit(context.Background(), event.Event{
		Type: event.EventAgentToolCall,
		Data: event.AgentToolCallData{ToolCallID: toolCallID, ToolName: toolName, Arguments: args},
	}))
	result.ToolCallID = toolCallID
	result.ToolName = toolName
	require.NoError(t, bus.Emit(context.Background(), event.Event{
		Type: event.EventAgentToolResult,
		Data: result,
	}))
}

// A turn that searched and cited nothing is exactly the case history replay
// could not reconstruct from knowledge_references, so it must be persisted.
func TestQuickAnswerTimelineRecorderPersistsSearchWithoutResults(t *testing.T) {
	bus := event.NewEventBus()
	msg := &types.Message{}
	registerQuickAnswerTimelineRecorder(bus, msg)

	emitTimelineStage(t, bus, "call-1", "knowledge_search",
		map[string]any{"query": "你好", "search_source": "knowledge"},
		event.AgentToolResultData{
			Output:   "未检索到相关内容",
			Success:  true,
			Duration: 12,
			Data:     map[string]interface{}{"count": 0, "doc_count": 0, "web_count": 0},
		})

	require.Len(t, msg.AgentSteps, 1)
	require.Len(t, msg.AgentSteps[0].ToolCalls, 1)
	call := msg.AgentSteps[0].ToolCalls[0]
	assert.Equal(t, "knowledge_search", call.Name)
	assert.Equal(t, types.PipelineToolCallIDPrefix+"call-1", call.ID)
	assert.Equal(t, "你好", call.Args["query"])
	require.NotNil(t, call.Result)
	assert.True(t, call.Result.Success)
	assert.Equal(t, "未检索到相关内容", call.Result.Output)
	assert.Equal(t, 0, call.Result.Data["count"])
	assert.Equal(t, int64(12), call.Duration)
}

func TestQuickAnswerTimelineRecorderKeepsStageOrderAndIgnoresOtherTools(t *testing.T) {
	bus := event.NewEventBus()
	msg := &types.Message{}
	registerQuickAnswerTimelineRecorder(bus, msg)

	emitTimelineStage(t, bus, "call-1", "query_understand", nil,
		event.AgentToolResultData{Output: "已完成问题理解", Success: true})
	emitTimelineStage(t, bus, "call-2", "web_search", nil,
		event.AgentToolResultData{Output: "ignored", Success: true})
	emitTimelineStage(t, bus, "call-3", "knowledge_search", nil,
		event.AgentToolResultData{Output: "检索到 3 条相关内容", Success: true})

	require.Len(t, msg.AgentSteps, 1)
	names := make([]string, 0, len(msg.AgentSteps[0].ToolCalls))
	for _, call := range msg.AgentSteps[0].ToolCalls {
		names = append(names, call.Name)
	}
	assert.Equal(t, []string{"query_understand", "knowledge_search"}, names)
}

// A stage that never reported a result belongs to an interrupted turn. Keeping
// it out of history is what stops a reload from showing it as completed.
func TestQuickAnswerTimelineRecorderSkipsStagesWithoutResult(t *testing.T) {
	bus := event.NewEventBus()
	msg := &types.Message{}
	registerQuickAnswerTimelineRecorder(bus, msg)

	require.NoError(t, bus.Emit(context.Background(), event.Event{
		Type: event.EventAgentToolCall,
		Data: event.AgentToolCallData{ToolCallID: "call-1", ToolName: "knowledge_search"},
	}))

	assert.Empty(t, msg.AgentSteps)
}

// Reasoning and timeline stages share one step, so neither may clobber the other.
func TestQuickAnswerReasoningAndTimelineShareOneStep(t *testing.T) {
	bus := event.NewEventBus()
	msg := &types.Message{}
	registerQuickAnswerTimelineRecorder(bus, msg)

	appendQuickAnswerReasoning(msg, "思考中")
	emitTimelineStage(t, bus, "call-1", "knowledge_search", nil,
		event.AgentToolResultData{Output: "未检索到相关内容", Success: true})
	appendQuickAnswerReasoning(msg, "继续")

	require.Len(t, msg.AgentSteps, 1)
	assert.Equal(t, "思考中继续", msg.AgentSteps[0].ReasoningContent)
	require.Len(t, msg.AgentSteps[0].ToolCalls, 1)
}
