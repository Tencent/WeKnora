package agent

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type failingOrderedSteerSink struct {
	fakeSteerSink
	failID   string
	attempts []string
}

func (s *failingOrderedSteerSink) PersistSteerMessage(
	ctx context.Context, sessionID, messageID, steerID, content string,
	mentions types.MentionedItems, channel string,
) string {
	s.attempts = append(s.attempts, steerID)
	if steerID == s.failID {
		return ""
	}
	return s.fakeSteerSink.PersistSteerMessage(ctx, sessionID, messageID, steerID, content, mentions, channel)
}

func TestDrainSteerMessagesPreservesOrderAcrossFailures(t *testing.T) {
	for _, failID := range []string{"first", "middle"} {
		t.Run(failID, func(t *testing.T) { testSteerOrderRecovery(t, failID) })
	}
}

func testSteerOrderRecovery(t *testing.T, failID string) {
	t.Helper()
	sink := &failingOrderedSteerSink{failID: failID, fakeSteerSink: fakeSteerSink{
		queued: []map[string]interface{}{
			steerEntry("first", "first"), steerEntry("middle", "middle"), steerEntry("last", "last"),
		},
	}}
	engine := newTestEngine(t, &mockChat{})
	engine.SetSteerSink(sink)
	state := &types.AgentState{}
	messages := []chat.Message{{Role: "user", Content: "original task"}}
	drain := func() int {
		return engine.drainSteerMessages(t.Context(), state, &messages, "sess", "msg")
	}
	prefix := 0
	if failID == "middle" {
		prefix = 1
	}
	require.Equal(t, prefix, drain())
	require.Len(t, sink.persisted, prefix)
	require.NotContains(t, sink.attempts, "last", "later instructions must not overtake a failed write")
	sink.attempts = nil
	require.Zero(t, drain(), "a sustained failure must not consume later instructions")
	require.Equal(t, []string{failID}, sink.attempts)
	sink.failID = ""
	require.Equal(t, len(sink.queued)-prefix, drain())
	require.Equal(t, []string{"first", "middle", "last"}, sink.persisted)
	require.Len(t, state.PendingSteerMessages, len(sink.queued))
	for i, content := range sink.persisted {
		require.Equal(t, types.SteerMessageContent(content), messages[i+1].Content)
	}
	require.Zero(t, drain(), "successful writes must not be replayed")
	require.Len(t, messages, 1+len(sink.queued))
}
