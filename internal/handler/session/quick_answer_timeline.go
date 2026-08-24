package session

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
)

// quickAnswerTimelineTools are the stages a fast-answer turn draws on its
// timeline. A turn that retrieved nothing still reports the search it ran, so
// the set is what decides whether the step survives a reload — not whether it
// produced citations. Keep in sync with RAG_TIMELINE_TOOL_NAMES on the frontend.
var quickAnswerTimelineTools = map[string]struct{}{
	"query_understand":   {},
	"knowledge_search":   {},
	"attachment_parsing": {},
	"image_analysis":     {},
}

// quickAnswerTimelineRecorder persists the fast-answer pipeline's timeline into
// the assistant message's AgentSteps.
//
// Reconstructing the timeline from knowledge_references only works for turns
// that cited something: a turn that searched and found nothing, or that only
// parsed an attachment, came back from history with no steps at all and lost
// the timeline it had while streaming.
type quickAnswerTimelineRecorder struct {
	mu        sync.Mutex
	msg       *types.Message
	pending   map[string]map[string]any
	startedAt map[string]time.Time
}

// registerQuickAnswerTimelineRecorder subscribes to the pipeline's tool events
// for the lifetime of one fast-answer turn. Agent mode is excluded on purpose:
// its own steps are written by the agent stream handler, which overwrites
// AgentSteps wholesale.
func registerQuickAnswerTimelineRecorder(bus *event.EventBus, msg *types.Message) {
	if bus == nil || msg == nil {
		return
	}
	rec := &quickAnswerTimelineRecorder{
		msg:       msg,
		pending:   make(map[string]map[string]any),
		startedAt: make(map[string]time.Time),
	}
	bus.On(event.EventAgentToolCall, rec.onToolCall)
	bus.On(event.EventAgentToolResult, rec.onToolResult)
}

func (r *quickAnswerTimelineRecorder) onToolCall(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolCallData)
	if !ok || !isQuickAnswerTimelineTool(data.ToolName) || data.ToolCallID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending[data.ToolCallID] = data.Arguments
	r.startedAt[data.ToolCallID] = time.Now()
	return nil
}

// onToolResult is where the step is persisted. Waiting for the result keeps a
// half-finished stage out of history: a stage still running when the turn is
// stopped would come back from a reload looking like it had completed.
func (r *quickAnswerTimelineRecorder) onToolResult(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolResultData)
	if !ok || !isQuickAnswerTimelineTool(data.ToolName) || data.ToolCallID == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	args := r.pending[data.ToolCallID]
	delete(r.pending, data.ToolCallID)
	started, hasStart := r.startedAt[data.ToolCallID]
	delete(r.startedAt, data.ToolCallID)

	duration := data.Duration
	if duration == 0 && hasStart {
		duration = time.Since(started).Milliseconds()
	}

	appendQuickAnswerToolCall(r.msg, types.ToolCall{
		ID:   types.PipelineToolCallIDPrefix + data.ToolCallID,
		Name: data.ToolName,
		Args: args,
		Result: &types.ToolResult{
			Success: data.Success,
			Output:  data.Output,
			Error:   data.Error,
			Data:    data.Data,
		},
		Duration: duration,
	})
	return nil
}

func isQuickAnswerTimelineTool(name string) bool {
	_, ok := quickAnswerTimelineTools[name]
	return ok
}

// ensureQuickAnswerStep returns the single step a fast-answer turn records
// into. There is no ReAct loop here, so everything belongs to iteration 0.
func ensureQuickAnswerStep(msg *types.Message) *types.AgentStep {
	if len(msg.AgentSteps) == 0 {
		msg.AgentSteps = types.AgentSteps{{
			Iteration: 0,
			Timestamp: time.Now(),
			ToolCalls: make([]types.ToolCall, 0),
		}}
	}
	return &msg.AgentSteps[0]
}

func appendQuickAnswerToolCall(msg *types.Message, call types.ToolCall) {
	step := ensureQuickAnswerStep(msg)
	for i := range step.ToolCalls {
		if step.ToolCalls[i].ID == call.ID {
			step.ToolCalls[i] = call
			return
		}
	}
	step.ToolCalls = append(step.ToolCalls, call)
}
