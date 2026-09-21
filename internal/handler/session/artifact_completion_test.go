package session

import (
	"context"
	"strings"
	"testing"
	"time"

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

func (s completionHistory) RecordRestoredMtime(context.Context, string, string, time.Time, string) error {
	return nil
}

func TestCompletionPublishesReconciledArtifactContent(t *testing.T) {
	old := types.MessageArtifact{URL: "resource://dHZ_fFslfs0GgJGaJZGjGA", FileName: "deck.pptx", SourcePath: "/workspace/output/deck.pptx"}
	next := old
	next.URL = "resource://4N1nAo-FZZoDEExDQz2yoA"
	stream := &completionEventRecorder{}
	message := &types.Message{ID: "m", Content: "![deck](" + old.URL + ")", Artifacts: types.MessageArtifacts{next}}
	handler := NewAgentStreamHandler(
		context.Background(), "s", "m", "req", 1, next.CreatedAt,
		message, stream, nil,
		service.NewArtifactCollector(
			nil, nil, completionHistory{[]types.MessageArtifact{old}}, nil, service.ArtifactCollectorConfig{},
		),
		nil, nil,
	)
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

// A turn that re-references a file it did not regenerate must still resolve the
// name. ArtifactCollector de-duplicates identical content, so this turn's
// `artifacts` is empty — and the rewrite used to be gated on it, leaving the
// body's `sandbox:<name>` unnormalized and rendering as a missing file.
//
// The first turn after a session fork is exactly this: the sandbox is rolled
// back to the fork point, so every file carried over from before the fork is
// skipped as unchanged.
func TestCompletionResolvesReferencesWhenNothingWasPersisted(t *testing.T) {
	existing := types.MessageArtifact{
		URL:        types.BuildResourcePath(strings.Repeat("a", types.ResourceHandleLength)),
		FileName:   "report.pptx",
		SourcePath: "/workspace/output/report.pptx",
	}
	stream := &completionEventRecorder{}
	message := &types.Message{ID: "m", Content: "已生成 ![报告](sandbox:report.pptx)"}
	handler := NewAgentStreamHandler(context.Background(), "s", "m", "req", 1, time.Time{},
		message, stream, nil,
		service.NewArtifactCollector(nil, nil, completionHistory{[]types.MessageArtifact{existing}}, nil, service.ArtifactCollectorConfig{}),
		nil, nil)

	err := handler.handleComplete(context.Background(), event.Event{Data: event.AgentCompleteData{MessageID: "m"}})
	require.NoError(t, err)

	require.Len(t, message.Artifacts, 1, "the referenced file must be attached to this message")
	require.Equal(t, existing.URL, message.Artifacts[0].URL)
	require.Contains(t, message.Content, existing.URL)
	require.NotContains(t, message.Content, "sandbox:")

	last := stream.events[len(stream.events)-1]
	require.Equal(t, types.ResponseTypeComplete, last.Type)
	require.Equal(t, message.Content, last.Data["final_content"])
}

// KnownArtifacts is creation order (oldest first). A name with several
// versions must resolve to the latest recorded file — the one sitting in
// the sandbox at the fork point — not the first time that name appeared.
func TestCompletionResolvesNameToLatestKnownVersion(t *testing.T) {
	old := types.MessageArtifact{
		URL:        types.BuildResourcePath(strings.Repeat("a", types.ResourceHandleLength)),
		FileName:   "report.pptx",
		SourcePath: "/workspace/output/report.pptx",
	}
	latest := types.MessageArtifact{
		URL:        types.BuildResourcePath(strings.Repeat("b", types.ResourceHandleLength)),
		FileName:   "report.pptx",
		SourcePath: "/workspace/output/report.pptx",
	}
	stream := &completionEventRecorder{}
	message := &types.Message{ID: "m", Content: "已生成 ![报告](sandbox:report.pptx)"}
	handler := NewAgentStreamHandler(context.Background(), "s", "m", "req", 1, time.Time{},
		message, stream, nil,
		service.NewArtifactCollector(nil, nil, completionHistory{[]types.MessageArtifact{old, latest}}, nil, service.ArtifactCollectorConfig{}),
		nil, nil)

	err := handler.handleComplete(context.Background(), event.Event{Data: event.AgentCompleteData{MessageID: "m"}})
	require.NoError(t, err)

	require.Len(t, message.Artifacts, 1)
	require.Equal(t, latest.URL, message.Artifacts[0].URL)
	require.Contains(t, message.Content, latest.URL)
	require.NotContains(t, message.Content, old.URL)
	require.NotContains(t, message.Content, "sandbox:")
}
