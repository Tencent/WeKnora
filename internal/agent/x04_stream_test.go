package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type x04EstablishmentChat struct{ mockChat }

func (c *x04EstablishmentChat) ChatStream(
	ctx context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestX04EstablishmentIsBounded(t *testing.T) {
	engine := newTestEngine(t, &x04EstablishmentChat{}, func(c *types.AgentConfig) { c.LLMCallTimeout = 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := engine.streamLLMToEventBus(ctx, nil, nil, nil)
	require.ErrorContains(t, err, "establishment stalled")
	require.Less(t, time.Since(start), 2*time.Second)
}

func TestX04BackpressureIsNotProviderStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var last atomic.Int64
	last.Store(time.Now().Add(-time.Hour).UnixNano())
	var waiting atomic.Bool
	stalled, stop := watchStreamStallActivity(ctx, cancel, 20*time.Millisecond, &last, &waiting)
	defer stop()
	time.Sleep(60 * time.Millisecond)
	require.False(t, stalled.Load())
	waiting.Store(true)
	require.Eventually(t, func() bool { return stalled.Load() }, time.Second, 5*time.Millisecond)
}
