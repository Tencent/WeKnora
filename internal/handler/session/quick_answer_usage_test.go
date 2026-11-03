package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// A completed quick-answer turn reports the provider usage on its complete
// event, the same way Agent turns do.
func TestQuickAnswerCompletionReportsUsage(t *testing.T) {
	for name, usage := range map[string]*types.TokenUsage{
		"reported":   {PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		"unreported": nil,
	} {
		t.Run(name, func(t *testing.T) {
			msg := &types.Message{ID: "m", SessionID: "s", Role: "assistant", Content: "answer", Usage: usage}
			messages := &imageCompletionMessages{}
			stream := &imageCompletionStream{}
			h := &Handler{messageService: messages, streamManager: stream}
			bus := event.NewEventBus()
			ctx := types.WithExecutionTenant(context.Background(), 1)
			streamHandler := h.setupStreamHandler(ctx, "s", "m", "req", 1, time.Now(), msg, bus)
			streamCtx := &sseStreamContext{eventBus: bus, streamHandler: streamHandler, assistantMessage: msg}

			h.completeQuickAnswerTurn(ctx, streamCtx, "question", "user-message")

			last := stream.events[len(stream.events)-1]
			require.Equal(t, types.ResponseTypeComplete, last.Type)
			require.Equal(t, usage, last.Usage)
		})
	}
}
