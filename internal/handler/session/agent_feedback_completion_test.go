// Package session tests Agent completion and feedback attribution behavior.
package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type recordingStreamManager struct {
	mu     sync.Mutex
	events []interfaces.StreamEvent
}

func (m *recordingStreamManager) AppendEvent(
	_ context.Context,
	_, _ string,
	streamEvent interfaces.StreamEvent,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, streamEvent)
	return nil
}

func (m *recordingStreamManager) GetEvents(
	_ context.Context,
	_, _ string,
	fromOffset int,
) ([]interfaces.StreamEvent, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := append([]interfaces.StreamEvent(nil), m.events[fromOffset:]...)
	return result, len(m.events), nil
}

func (m *recordingStreamManager) snapshot() []interfaces.StreamEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]interfaces.StreamEvent(nil), m.events...)
}

func TestAgentCompletionIsPublishedOnlyAfterPersistence(t *testing.T) {
	ctx := context.Background()
	manager := &recordingStreamManager{}
	message := &types.Message{ID: "message-a", SessionID: "session-a", Role: "assistant"}
	handler := NewAgentStreamHandler(
		ctx, message.SessionID, message.ID, "request-a", time.Now(),
		message, manager, event.NewEventBus(), true,
	)

	err := handler.handleComplete(ctx, event.Event{
		ID: "complete-engine",
		Data: event.AgentCompleteData{
			MessageID: message.ID, TotalSteps: 2, TotalDurationMs: 10,
			FeedbackEligible: true,
		},
	})
	require.NoError(t, err)
	assert.Empty(t, manager.snapshot(), "engine completion must remain private until commit")

	handler.PrepareAgentCompletion()
	assert.True(t, message.CanonicalChunkReferencesSet)
	handler.CompleteAfterPersistence(true, nil)

	events := manager.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, types.ResponseTypeComplete, events[0].Type)
	assert.Equal(t, true, events[0].Data["feedback_eligible"])
}

func TestAgentCompletionUsesDisplayedKBCanonicalReferencesOnly(t *testing.T) {
	ctx := context.Background()
	manager := &recordingStreamManager{}
	message := &types.Message{ID: "message-a", SessionID: "session-a", Role: "assistant"}
	handler := NewAgentStreamHandler(
		ctx, message.SessionID, message.ID, "request-a", time.Now(),
		message, manager, event.NewEventBus(), true,
	)
	kbReferences := []types.AgentFeedbackReference{
		{
			TenantID: 21,
			Result: &types.SearchResult{
				ID: "chunk-a", KnowledgeBaseID: "kb-a", ChunkType: string(types.ChunkTypeText),
			},
		},
		{
			TenantID: 21,
			Result: &types.SearchResult{
				ID: "chunk-a", KnowledgeBaseID: "kb-a", ChunkType: string(types.ChunkTypeText),
			},
		},
		{
			TenantID: 21,
			Result: &types.SearchResult{
				ID: "chunk-b", KnowledgeBaseID: "kb-a", ChunkType: string(types.ChunkTypeText),
			},
		},
	}
	require.NoError(t, handler.handleToolResult(ctx, event.Event{
		Data: event.AgentToolResultData{
			ToolCallID: "kb-tool", ToolName: "knowledge_search", Success: true,
			FeedbackReferences: kbReferences,
		},
	}))
	require.NoError(t, handler.handleToolResult(ctx, event.Event{
		Data: event.AgentToolResultData{
			ToolCallID: "web-tool", ToolName: "web_search", Success: true,
			FeedbackReferences: kbReferences,
		},
	}))

	handler.PrepareAgentCompletion()
	assert.True(t, message.CanonicalChunkReferencesSet)
	require.Len(t, message.KnowledgeReferences, 2)
	require.Len(t, message.CanonicalChunkReferences, 2)
	assert.Equal(t, []string{"chunk-a", "chunk-b"}, []string{
		message.KnowledgeReferences[0].ID,
		message.KnowledgeReferences[1].ID,
	})
}

func TestAgentCompletionPersistenceFailureNeverPublishesEligibility(t *testing.T) {
	ctx := context.Background()
	manager := &recordingStreamManager{}
	message := &types.Message{ID: "message-a", SessionID: "session-a", Role: "assistant"}
	handler := NewAgentStreamHandler(
		ctx, message.SessionID, message.ID, "request-a", time.Now(),
		message, manager, event.NewEventBus(), true,
	)

	require.NoError(t, handler.handleComplete(ctx, event.Event{
		ID: "complete-engine",
		Data: event.AgentCompleteData{
			MessageID: message.ID, FeedbackEligible: true,
		},
	}))
	handler.CompleteAfterPersistence(true, errors.New("commit failed"))

	events := manager.snapshot()
	require.Len(t, events, 2)
	assert.Equal(t, types.ResponseTypeError, events[0].Type)
	assert.Equal(t, types.ResponseTypeComplete, events[1].Type)
	assert.Equal(t, false, events[1].Data["feedback_eligible"])
}

func TestStandardCompletionPublishesCommittedEligibilityImmediately(t *testing.T) {
	ctx := context.Background()
	manager := &recordingStreamManager{}
	message := &types.Message{ID: "message-a", SessionID: "session-a", Role: "assistant"}
	handler := NewAgentStreamHandler(
		ctx, message.SessionID, message.ID, "request-a", time.Now(),
		message, manager, event.NewEventBus(), false,
	)

	require.NoError(t, handler.handleComplete(ctx, event.Event{
		ID: "complete-standard",
		Data: event.AgentCompleteData{
			MessageID: message.ID, FeedbackEligible: true,
		},
	}))

	events := manager.snapshot()
	require.Len(t, events, 1)
	assert.Equal(t, types.ResponseTypeComplete, events[0].Type)
	assert.Equal(t, true, events[0].Data["feedback_eligible"])
}
