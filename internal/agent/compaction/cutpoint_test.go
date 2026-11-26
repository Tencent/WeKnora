package compaction

import (
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestFindCutPointFallback(t *testing.T) {
	estimator := newEstimator(t)
	messages := []chat.Message{
		{Role: "system", Content: "Instructions"},
		{Role: "user", Content: "Find the document"},
		{Role: "assistant"},
		{Role: "tool", Content: "Search results"},
		{Role: "tool", Content: "More search results"},
	}

	// Even when the latest result alone exceeds the budget, the entire
	// tool group stays together.
	require.Equal(t, CutPoint{FirstKeptIdx: 2, TurnStartIdx: 1, IsSplitTurn: true},
		FindCutPoint(messages, 1, 0, estimator))

	// A tool-only range has no legal cut. Do not select the assistant
	// message outside that range.
	require.Equal(t, CutPoint{FirstKeptIdx: 3, TurnStartIdx: -1},
		FindCutPoint(messages, 3, 0, estimator))
}

func BenchmarkFindCutPointLongHistory(b *testing.B) {
	estimator, err := agenttoken.NewEstimator()
	require.NoError(b, err)
	const turns = 1024
	messages := make([]chat.Message, 0, 4*turns)
	for range turns {
		messages = append(messages,
			chat.Message{Role: "user", Content: "Find the document"},
			chat.Message{Role: "assistant"},
			chat.Message{Role: "tool", Content: "Search results"},
			chat.Message{Role: "assistant", Content: "Here is the document"},
		)
	}
	lastTurn := len(messages) - 4
	budget := 0
	for i := lastTurn; i < len(messages); i++ {
		budget += estimator.EstimateMessage(&messages[i])
	}
	require.Equal(b, CutPoint{FirstKeptIdx: lastTurn, TurnStartIdx: -1},
		FindCutPoint(messages, 0, budget, estimator))

	b.ReportAllocs()
	for b.Loop() {
		FindCutPoint(messages, 0, budget, estimator)
	}
}
