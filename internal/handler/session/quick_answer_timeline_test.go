package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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

func TestQuickAnswerTruncationPersistsOnSharedStep(t *testing.T) {
	msg := &types.Message{}
	appendQuickAnswerReasoning(msg, "思考中")
	markQuickAnswerTruncated(msg)

	require.Len(t, msg.AgentSteps, 1)
	assert.Equal(t, "思考中", msg.AgentSteps[0].ReasoningContent)
	assert.True(t, msg.AgentSteps[0].Truncated)
}

// Verify the durable completion boundary, including the message update and
// JSON reload, independently of the provider's stream generation.
func TestQuickAnswerFallbackTruncationSurvivesCompletion(t *testing.T) {
	for _, content := range []string{"output budget exhausted", "partial answer"} {
		t.Run(content, func(t *testing.T) {
			messages := &imageCompletionMessages{}
			stream := &imageCompletionStream{}
			h := &Handler{messageService: messages, streamManager: stream}
			bus := event.NewEventBus()
			msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant", Content: content, IsFallback: true}
			appendQuickAnswerReasoning(msg, "reasoning")
			markQuickAnswerTruncated(msg)
			ctx := types.WithExecutionTenant(context.Background(), 1)
			handler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), msg, bus)
			require.NoError(t, bus.Emit(ctx, event.Event{
				ID: "fallback", Type: event.EventAgentFinalAnswer,
				Data: event.AgentFinalAnswerData{Content: content, Done: true, IsFallback: true, Truncated: true},
			}))
			require.NotEmpty(t, stream.events)
			require.Equal(t, true, stream.events[0].Data["truncated"])
			require.Equal(t, true, stream.events[0].Data["is_fallback"])
			h.completeQuickAnswerTurn(ctx, &sseStreamContext{
				eventBus: bus, streamHandler: handler, assistantMessage: msg,
			}, "", "")
			require.NotNil(t, messages.saved)
			raw, err := json.Marshal(messages.saved)
			require.NoError(t, err)
			var restored types.Message
			require.NoError(t, json.Unmarshal(raw, &restored))
			require.Equal(t, content, restored.Content)
			require.True(t, restored.IsFallback)
			require.True(t, restored.IsCompleted)
			require.Len(t, restored.AgentSteps, 1)
			require.True(t, restored.AgentSteps[0].Truncated)
			require.Equal(t, "reasoning", restored.AgentSteps[0].ReasoningContent)
		})
	}
}
