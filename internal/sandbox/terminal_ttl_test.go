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
	prevMin := terminalTTLRefreshMin
	terminalTTLRefreshMin = 40 * time.Millisecond
	t.Cleanup(func() { terminalTTLRefreshMin = prevMin })

	var n atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })

	startTerminalTTLRefresh(ctx, closed, 90*time.Millisecond, func(context.Context) error {
		n.Add(1)
		return nil
	})

	require.Eventually(t, func() bool {
		return n.Load() >= 1
	}, 500*time.Millisecond, 5*time.Millisecond, "expected an immediate TTL refresh")
	require.Eventually(t, func() bool {
		return n.Load() >= 2
	}, time.Second, 10*time.Millisecond, "expected a follow-up refresh on the interval")
}
