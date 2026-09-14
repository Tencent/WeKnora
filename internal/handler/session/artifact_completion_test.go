package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type completionEventRecorder struct {
	interfaces.StreamManager
	events []interfaces.StreamEvent
}

func (s *completionEventRecorder) AppendEvent(_ context.Context, _, _ string, e interfaces.StreamEvent) error {
	s.events = append(s.events, e)
	return nil
}

type completionHistory struct{ previous []types.MessageArtifact }

func (s completionHistory) KnownArtifacts(context.Context, string) ([]types.MessageArtifact, error) {
	return s.previous, nil
}

func TestCompletionPublishesReconciledArtifactContent(t *testing.T) {
	old := types.MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "deck.pptx", SourcePath: "/workspace/output/deck.pptx"}
	next := old
	next.URL = "resource://4N1nAo-FZZoDEExDQz2yoA"
	stream := &completionEventRecorder{}
	message := &types.Message{ID: "m", Content: "![deck](" + old.URL + ")", Artifacts: types.MessageArtifacts{next}}
	handler := NewAgentStreamHandler(context.Background(), "s", "m", "req", 1, next.CreatedAt,
		message, stream, nil, service.NewArtifactCollector(nil, nil, completionHistory{[]types.MessageArtifact{old}}, nil, service.ArtifactCollectorConfig{}))
	err := handler.handleComplete(context.Background(), event.Event{Data: event.AgentCompleteData{MessageID: "m"}})
	require.NoError(t, err)
	last := stream.events[len(stream.events)-1]
	require.Equal(t, types.ResponseTypeComplete, last.Type)
	require.Equal(t, message.Content, last.Data["final_content"])
	require.Contains(t, message.Content, old.URL)
	require.Contains(t, message.Content, next.URL)
	response := buildStreamResponse(last, "req")
	require.Equal(t, message.Content, response.Data["final_content"])
}
