package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// Resume continues a run that was suspended on a prompt, now answered. It
// rebuilds the transcript from the snapshot, finishes the interrupted round
// with the answer delivered to the blocked call, then carries on with the
// ReAct loop. The run may suspend again; completion is emitted either way.
func (e *AgentEngine) Resume(
	ctx context.Context,
	sessionID, messageID string,
	llmContext []chat.Message,
	snapshot []byte,
	answered *types.AgentPrompt,
) (*types.AgentState, error) {
	if answered == nil {
		return nil, fmt.Errorf("resume needs an answered prompt")
	}
	snap, err := DecodeRunSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	p := snap.PendingRound
	if p.BlockedIndex < 0 || p.BlockedIndex >= len(p.Calls) || len(p.Step.ToolCalls) != p.BlockedIndex {
		return nil, fmt.Errorf("snapshot round is inconsistent: blocked %d of %d calls, %d done",
			p.BlockedIndex, len(p.Calls), len(p.Step.ToolCalls))
	}
	if p.Calls[p.BlockedIndex].ID != answered.ToolCallID {
		return nil, fmt.Errorf("prompt %s answers call %s, run is blocked on %s",
			answered.ID, answered.ToolCallID, p.Calls[p.BlockedIndex].ID)
	}
	logger.Infof(ctx, "[Agent] Resuming: session=%s, message=%s, prompt=%s, round=%d",
		sessionID, messageID, answered.ID, snap.CurrentRound+1)
	defer e.toolRegistry.Cleanup(ctx)

	state := &types.AgentState{
		RoundSteps:    append([]types.AgentStep{}, snap.CompletedSteps...),
		KnowledgeRefs: append([]*types.SearchResult{}, snap.KnowledgeRefs...),
		CurrentRound:  snap.CurrentRound,
		TurnUsage:     snap.TurnUsage,
	}
	systemPrompt := e.buildSystemPrompt(ctx)
	e.beginTurn(snap.Query, snap.ImageURLs, state)
	for id, content := range snap.UserMessages {
		e.recordSteerMessage(id, content)
	}
	messages := e.buildMessagesWithLLMContext(systemPrompt, snap.Query, sessionID, llmContext, snap.ImageURLs)
	appendUsers := func(step types.AgentStep) error {
		for _, id := range step.UserMessagesBefore {
			content, ok := snap.UserMessages[id]
			if !ok {
				return fmt.Errorf("missing steered message %s in run snapshot", id)
			}
			messages = append(messages, chat.Message{Role: "user", Content: types.SteerMessageContent(content)})
		}
		return nil
	}
	for _, step := range state.RoundSteps {
		if err := appendUsers(step); err != nil {
			return nil, err
		}
		messages = e.appendToolResults(messages, step)
		messages = e.appendToolImages(ctx, messages, step)
	}
	if err := appendUsers(p.Step); err != nil {
		return nil, err
	}
	if e.toolRegistry != nil {
		e.toolRegistry.RememberMCPHistory(messages)
		// These calls were issued before suspension, including the first call
		// to a tool whose definition was only exposed by a describe round.
		e.toolRegistry.RememberMCPHistory(pendingCallHistory(p))
		e.toolRegistry.RefreshMCPTools(ctx)
	}
	tools := e.buildToolsForLLM()

	startTime := time.Now()
	step, suspended := e.finishPendingRound(ctx, p, answered, state.CurrentRound, sessionID, messageID)
	if suspended != nil {
		logger.Infof(ctx, "[Agent][Round-%d] Suspended again waiting on prompt %s",
			state.CurrentRound+1, suspended.PromptID)
		state.Suspended = suspended
		state.SuspendedStep = &step
		e.emitCompletionEvent(context.WithoutCancel(ctx), state, sessionID, messageID, startTime)
		return state, nil
	}
	state.RoundSteps = append(state.RoundSteps, step)
	messages = e.appendToolResults(messages, step)
	messages = e.appendToolImages(ctx, messages, step)
	state.CurrentRound++

	if _, err := e.executeLoop(ctx, state, snap.Query, messages, tools, sessionID, messageID); err != nil {
		logger.Errorf(ctx, "[Agent] Resumed execution failed: %v", err)
		if emitErr := e.eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("error"),
			Type:      event.EventError,
			SessionID: sessionID,
			Data: event.ErrorData{
				Error:     err.Error(),
				Stage:     "agent_execution",
				SessionID: sessionID,
			},
		}); emitErr != nil {
			logger.Warnf(ctx, "[Agent] Failed to emit resumed execution error: %v", emitErr)
		}
		return nil, err
	}
	return state, nil
}

func pendingCallHistory(p PendingRound) []chat.Message {
	calls := make([]chat.ToolCall, len(p.Calls))
	for i, c := range p.Calls {
		calls[i] = chat.ToolCall{ID: c.ID, Type: "function", Function: chat.FunctionCall{
			Name: c.Name, Arguments: c.Arguments,
		}}
	}
	return []chat.Message{{Role: "assistant", ToolCalls: calls}}
}

// finishPendingRound runs the interrupted round from its blocked call on,
// sequentially. The blocked call receives the answer; a later call may raise
// a prompt of its own and suspend the round again.
func (e *AgentEngine) finishPendingRound(
	ctx context.Context, p PendingRound, answered *types.AgentPrompt,
	iteration int, sessionID, messageID string,
) (types.AgentStep, *types.PromptSuspension) {
	step := p.Step
	step.ToolCalls = append([]types.ToolCall(nil), p.Step.ToolCalls...)
	calls := make([]types.LLMToolCall, len(p.Calls))
	for i, c := range p.Calls {
		calls[i] = types.LLMToolCall{
			ID:                c.ID,
			Type:              "function",
			Function:          types.FunctionCall{Name: c.Name, Arguments: c.Arguments},
			UnresolvedHandles: c.UnresolvedHandles,
			ProviderMetadata:  c.ProviderMetadata,
		}
	}

	round := iteration + 1
	e.beginRound(step, calls)
	defer e.endRound()
	for i, done := range step.ToolCalls {
		e.recordCall(i, done)
	}
	for i := p.BlockedIndex; i < len(calls); i++ {
		var answer *types.AgentPrompt
		if i == p.BlockedIndex {
			answer = answered
		}
		toolCall := e.runToolCallAnswered(ctx, calls[i], i, iteration, round, sessionID, messageID, answer)
		if s := suspensionOf(toolCall); s != nil {
			return step, s
		}
		e.recordCall(i, toolCall)
		step.ToolCalls = append(step.ToolCalls, toolCall)
		e.emitToolOutcome(ctx, toolCall, iteration, sessionID)
	}
	return step, nil
}
