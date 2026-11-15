package chatpipeline

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func runStream(t *testing.T, ctx context.Context, bus types.EventBusInterface, chunks ...types.StreamResponse) {
	t.Helper()
	manage := &types.ChatManage{}
	manage.SessionID = "usage"
	manage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: &openStreamChat{closeStream: true, chunks: chunks}}}
	require.Nil(t, plugin.OnEvent(ctx, types.CHAT_COMPLETION_STREAM, manage, func() *PluginError { return nil }))
}

// OpenAI-compatible providers with stream_options.include_usage send usage in
// a trailing chunk after the finish_reason chunk. The single completion must
// carry it.
func TestStreamReportsLateProviderUsageOnSingleCompletion(t *testing.T) {
	bus := &syncEventBus{}
	runStream(t, context.Background(), bus,
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "hello"},
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: &types.TokenUsage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120}},
	)
	require.Eventually(t, func() bool {
		events := bus.finalAnswerEvents()
		return len(events) > 0 && events[len(events)-1].Done
	}, 2*time.Second, 5*time.Millisecond)
	completions := 0
	for _, answer := range bus.finalAnswerEvents() {
		if !answer.Done {
			continue
		}
		completions++
		raw, err := json.Marshal(answer)
		require.NoError(t, err)
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.Equal(t, float64(120), payload["usage"].(map[string]interface{})["total_tokens"])
	}
	require.Equal(t, 1, completions)
}

// A stream that errors after its finish_reason never completes successfully.
func TestStreamWithErrorAfterFinishNeverCompletes(t *testing.T) {
	bus := &syncEventBus{}
	runStream(t, context.Background(), bus,
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "partial"},
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
		types.StreamResponse{ResponseType: types.ResponseTypeError, Content: "provider failure"},
	)
	require.Eventually(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		for _, evt := range bus.events {
			if _, ok := evt.Data.(event.ErrorData); ok {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
	require.Never(t, func() bool {
		for _, answer := range bus.finalAnswerEvents() {
			if answer.Done {
				return true
			}
		}
		return false
	}, 200*time.Millisecond, 5*time.Millisecond)
}

type cancellingBus struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	events []types.Event
}

func (b *cancellingBus) On(types.EventType, types.EventHandler) {}

func (b *cancellingBus) Emit(_ context.Context, evt types.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	if thought, ok := evt.Data.(event.AgentThoughtData); ok && thought.Done {
		b.cancel()
	}
	return nil
}

// Cancellation between the last provider chunk and the completion must not
// publish a successful completion.
func TestStreamCancelledAtCloseDoesNotComplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := &cancellingBus{cancel: cancel}
	runStream(t, ctx, bus,
		types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "tail"},
		types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
	)
	require.Eventually(t, func() bool { return ctx.Err() != nil }, 2*time.Second, 5*time.Millisecond)
	require.Never(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		for _, evt := range bus.events {
			if answer, ok := evt.Data.(event.AgentFinalAnswerData); ok && answer.Done {
				return true
			}
		}
		return false
	}, 200*time.Millisecond, 5*time.Millisecond)
}
