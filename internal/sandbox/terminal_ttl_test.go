package sandbox

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTerminalTTLRefreshInterval(t *testing.T) {
	t.Parallel()
	require.Equal(t, 15*time.Second, terminalTTLRefreshInterval(0))
	require.Equal(t, 15*time.Second, terminalTTLRefreshInterval(10*time.Second))
	require.Equal(t, 20*time.Second, terminalTTLRefreshInterval(60*time.Second))
	require.Equal(t, 2*time.Minute, terminalTTLRefreshInterval(30*time.Minute))
	require.Equal(t, 2*time.Minute, terminalTTLRefreshInterval(time.Hour))

	for _, ttl := range []time.Duration{
		MinDockerIdleTTL,
		61 * time.Second,
		DefaultDockerIdleTTL,
	} {
		require.Less(t, terminalTTLRefreshInterval(ttl), ttl,
			"every valid Docker idle TTL must outlive its terminal refresh interval")
	}
}

func TestEffectiveTerminalIdleDisconnect(t *testing.T) {
	t.Parallel()
	require.Equal(t, DefaultTerminalIdleDisconnect, EffectiveTerminalIdleDisconnect(0))
	require.Equal(t, DefaultTerminalIdleDisconnect, EffectiveTerminalIdleDisconnect(-time.Second))
	require.Equal(t, minTerminalIdleDisconnect, EffectiveTerminalIdleDisconnect(30*time.Second))
	require.Equal(t, 20*time.Minute, EffectiveTerminalIdleDisconnect(20*time.Minute))
	require.Equal(t, maxTerminalIdleDisconnect, EffectiveTerminalIdleDisconnect(48*time.Hour))
}

func TestStartTerminalTTLRefreshCallsImmediately(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })

	startTerminalTTLRefresh(ctx, closed, MinDockerIdleTTL, func(context.Context) error {
		n.Add(1)
		return nil
	})

	require.Eventually(t, func() bool {
		return n.Load() >= 1
	}, 500*time.Millisecond, 5*time.Millisecond, "expected an immediate TTL refresh")
	cancel()
}

func TestTerminalTTLRefreshLoopCanWaitForInitialInterval(t *testing.T) {
	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })

	startTerminalTTLRefreshLoop(ctx, closed, 40*time.Millisecond, false, func(context.Context) error {
		n.Add(1)
		return nil
	})

	require.Never(t, func() bool {
		return n.Load() != 0
	}, 20*time.Millisecond, 2*time.Millisecond, "refresh must not run immediately")
	require.Eventually(t, func() bool {
		return n.Load() >= 1
	}, 500*time.Millisecond, 5*time.Millisecond, "expected refresh on the first interval")
}
