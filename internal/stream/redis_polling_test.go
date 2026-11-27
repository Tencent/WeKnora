package stream

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestRedisPollingPreservesEventOffsetsPastOriginalTTL(t *testing.T) {
	for _, poll := range []string{"replay", "waiting"} {
		t.Run(poll, func(t *testing.T) {
			mgr, mini := newTestRedisStreamManager(t, time.Hour)
			ctx := t.Context()
			const sessionID, messageID = "session", "run"
			require.NoError(t, mgr.SetLiveRun(ctx, sessionID, messageID, "request"))
			require.NoError(t, mgr.AppendEvent(ctx, sessionID, messageID, interfaces.StreamEvent{
				ID: "first", Type: types.ResponseTypeAnswer, Content: "answer",
			}))
			events, next, err := mgr.GetEvents(ctx, sessionID, messageID, 0)
			require.NoError(t, err)
			require.Len(t, events, 1)
			require.Equal(t, "first", events[0].ID)
			require.Equal(t, 1, next)

			mini.FastForward(45 * time.Minute)
			pollOffset := next
			if poll == "replay" {
				pollOffset = 0
			}
			events, next, err = mgr.GetEvents(ctx, sessionID, messageID, pollOffset)
			require.NoError(t, err)
			require.Len(t, events, 1-pollOffset)
			require.Equal(t, 1, next)

			mini.FastForward(30 * time.Minute)
			live, _, err := mgr.GetLiveRun(ctx, sessionID)
			require.NoError(t, err)
			require.Equal(t, messageID, live)
			require.NoError(t, mgr.AppendEvent(ctx, sessionID, messageID, interfaces.StreamEvent{
				ID: "complete", Type: types.ResponseTypeComplete, Done: true,
			}))
			events, next, err = mgr.GetEvents(ctx, sessionID, messageID, next)
			require.NoError(t, err)
			require.Len(t, events, 1, "continuation must receive the completion")
			require.Equal(t, "complete", events[0].ID)
			require.Equal(t, types.ResponseTypeComplete, events[0].Type)
			require.True(t, events[0].Done)
			require.Equal(t, 2, next)
		})
	}
}

func TestRedisPollingPreservesPendingSteerPastOriginalTTL(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	ctx := t.Context()
	const sessionID, messageID = "session", "run"
	require.NoError(t, mgr.AppendSteerEvents(ctx, sessionID, messageID, []interfaces.StreamEvent{{
		ID: "queued", Type: types.ResponseTypeSteer, Content: "next",
		Data: map[string]interface{}{"delivery": "after"},
	}}))

	mini.FastForward(45 * time.Minute)
	events, next, err := mgr.GetSteerEvents(ctx, sessionID, messageID, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "after", events[0].Data["delivery"])
	require.Equal(t, 1, next)

	mini.FastForward(30 * time.Minute)
	updated, err := mgr.UpdateSteerEventData(ctx, sessionID, messageID, "queued",
		map[string]interface{}{"delivery": "inject"})
	require.NoError(t, err)
	require.True(t, updated, "the queued event must still be available for promotion")
	events, next, err = mgr.GetSteerEvents(ctx, sessionID, messageID, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "queued", events[0].ID)
	require.Equal(t, "inject", events[0].Data["delivery"])
	require.Equal(t, 1, next)

	mini.FastForward(time.Hour + time.Second)
	events, next, err = mgr.GetSteerEvents(ctx, sessionID, messageID, 0)
	require.NoError(t, err)
	require.Empty(t, events)
	require.Zero(t, next)
}

func TestRedisPollingCompletedHistoryExpiresWithoutSuccessfulReads(t *testing.T) {
	mgr, mini := newTestRedisStreamManager(t, time.Hour)
	ctx := t.Context()
	const sessionID, messageID = "session", "run"
	require.NoError(t, mgr.AppendEvent(ctx, sessionID, messageID, interfaces.StreamEvent{
		ID: "complete", Type: types.ResponseTypeComplete, Done: true,
	}))

	mini.FastForward(45 * time.Minute)
	events, next, err := mgr.GetEvents(ctx, sessionID, messageID, 0)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "complete", events[0].ID)
	require.Equal(t, 1, next)

	mini.FastForward(45 * time.Minute)
	require.True(t, mini.Exists(mgr.buildKey(sessionID, messageID)), "replay must renew completed history")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, next, err = mgr.GetEvents(canceled, sessionID, messageID, next)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, next)

	mini.FastForward(16 * time.Minute)
	events, next, err = mgr.GetEvents(ctx, sessionID, messageID, next)
	require.NoError(t, err)
	require.Empty(t, events)
	require.Equal(t, 1, next)
	require.False(t, mini.Exists(mgr.buildKey(sessionID, messageID)))
}
