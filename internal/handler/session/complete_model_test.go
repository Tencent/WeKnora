package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func publishedCompletion(t *testing.T, data event.AgentCompleteData) interfaces.StreamEvent {
	t.Helper()
	stream := &completionEventRecorder{}
	message := &types.Message{ID: "m"}
	handler := NewAgentStreamHandler(
		context.Background(), "s", "m", "req", 1, time.Now(), message, stream, nil, nil, nil, nil,
	)
	require.NoError(t, handler.handleComplete(context.Background(), event.Event{Data: data}))
	require.NoError(t, handler.publishCompletion(context.Background()))
	last := stream.events[len(stream.events)-1]
	require.Equal(t, types.ResponseTypeComplete, last.Type)
	return last
}

// A relay that prices a turn's usage needs to know which model produced it.
// The model rides next to usage in the complete event's data, survives the
// stream manager's JSON round trip and reaches the response.
func TestCompletionEventCarriesTheTurnsChatModel(t *testing.T) {
	last := publishedCompletion(t, event.AgentCompleteData{
		MessageID: "m", ModelID: "model-1",
		Usage: &types.TokenUsage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
	})

	require.Equal(t, "model-1", last.Data["model_id"])

	raw, err := json.Marshal(last)
	require.NoError(t, err)
	var restored interfaces.StreamEvent
	require.NoError(t, json.Unmarshal(raw, &restored))
	response := buildStreamResponse(restored, "req")
	require.Equal(t, "model-1", response.Data["model_id"])
	require.NotNil(t, response.Usage)
}

func TestCompletionEventOmitsAnUnknownModel(t *testing.T) {
	last := publishedCompletion(t, event.AgentCompleteData{MessageID: "m"})

	require.NotContains(t, last.Data, "model_id")
}
