package agent

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func captureCompletion(t *testing.T, engine *AgentEngine) event.AgentCompleteData {
	t.Helper()
	var got event.AgentCompleteData
	engine.eventBus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		got, _ = evt.Data.(event.AgentCompleteData)
		return nil
	})
	engine.emitCompletionEvent(context.Background(), &types.AgentState{}, "session", "message", time.Now())
	require.Equal(t, "message", got.MessageID, "the completion event was emitted")
	return got
}

// The turn's usage comes only from calls through the engine's chat model, so
// the completion event names that model as the one the usage belongs to.
func TestCompletionEventNamesTheTurnsChatModel(t *testing.T) {
	got := captureCompletion(t, newTestEngine(t, &mockChat{}))

	require.Equal(t, "mock-id", got.ModelID)
}

func TestCompletionEventWithoutAChatModelNamesNone(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.chatModel = nil

	require.Empty(t, captureCompletion(t, engine).ModelID)
}
