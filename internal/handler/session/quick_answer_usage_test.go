package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// The KnowledgeQA complete event must report the provider's token usage both
// inside the data payload and on the typed event field, and the persisted
// message must keep it, matching the agent path (issue #3865).
func TestQuickAnswerCompleteCarriesUsage(t *testing.T) {
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	ctx := types.WithExecutionTenant(context.Background(), 1)
	handler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), msg, bus)
	streamCtx := &sseStreamContext{
		eventBus:         bus,
		streamHandler:    handler,
		assistantMessage: msg,
		asyncCtx:         ctx,
	}
	h.registerQuickAnswerCompletionHandler(streamCtx, "s", "question", "u", 1)

	usage := &types.TokenUsage{PromptTokens: 21203, CompletionTokens: 27, TotalTokens: 21230}
	require.NoError(t, bus.Emit(ctx, event.Event{
		ID:   "answer",
		Type: event.EventAgentFinalAnswer,
		Data: event.AgentFinalAnswerData{Content: "DONE", Done: true, Usage: usage},
	}))

	var complete *interfaces.StreamEvent
	for i := range stream.events {
		if stream.events[i].Type == types.ResponseTypeComplete {
			complete = &stream.events[i]
		}
	}
	require.NotNil(t, complete, "completion must publish a complete stream event")
	require.Equal(t, usage, complete.Usage, "complete event must carry usage at top level")
	require.Equal(t, usage, complete.Data["usage"], "complete event must carry usage inside data")
	require.NotNil(t, messages.saved, "assistant message must be persisted")
	// The persistence path may re-read the message; assert by value.
	require.Equal(t, "DONE", messages.saved.Content)
	require.Equal(t, usage, messages.saved.Usage, "persisted message must keep the turn usage")
}

// A turn that never receives usage (stop, failure, usage-less provider) must
// not manufacture a zero usage: the complete event and the message keep the
// previous behavior.
func TestQuickAnswerCompleteWithoutUsageUnchanged(t *testing.T) {
	messages := &imageCompletionMessages{}
	stream := &imageCompletionStream{}
	h := &Handler{messageService: messages, streamManager: stream}
	bus := event.NewEventBus()
	msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant"}
	ctx := types.WithExecutionTenant(context.Background(), 1)
	handler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), msg, bus)
	streamCtx := &sseStreamContext{
		eventBus:         bus,
		streamHandler:    handler,
		assistantMessage: msg,
		asyncCtx:         ctx,
	}
	h.registerQuickAnswerCompletionHandler(streamCtx, "s", "question", "u", 1)

	require.NoError(t, bus.Emit(ctx, event.Event{
		ID:   "answer",
		Type: event.EventAgentFinalAnswer,
		Data: event.AgentFinalAnswerData{Content: "DONE", Done: true},
	}))

	var complete *interfaces.StreamEvent
	for i := range stream.events {
		if stream.events[i].Type == types.ResponseTypeComplete {
			complete = &stream.events[i]
		}
	}
	require.NotNil(t, complete)
	require.Nil(t, complete.Usage)
	_, hasUsage := complete.Data["usage"]
	require.False(t, hasUsage, "usage key must stay absent when the provider sent none")
	require.NotNil(t, messages.saved)
	require.Nil(t, messages.saved.Usage)
}
