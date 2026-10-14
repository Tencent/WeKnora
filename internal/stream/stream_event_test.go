package stream

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestPendingCompletionErrorSurvivesStreamStorage(t *testing.T) {
	redisManager, _ := newTestRedisStreamManager(t, time.Hour)
	for name, manager := range map[string]interfaces.StreamManager{
		"memory": NewMemoryStreamManager(), "redis": redisManager,
	} {
		t.Run(name, func(t *testing.T) {
			evt := interfaces.StreamEvent{
				ID: "err", Type: types.ResponseTypeError, Content: "model failed", Done: true,
				CompletionPending: true,
			}
			require.NoError(t, manager.AppendEvent(t.Context(), "s", "m", evt))
			events, offset, err := manager.GetEvents(t.Context(), "s", "m", 0)
			require.NoError(t, err)
			require.Equal(t, 1, offset)
			require.Len(t, events, 1)
			require.True(t, events[0].CompletionPending)
			require.True(t, events[0].Done)
		})
	}
}
