package sandbox

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pooledCubeClient(t *testing.T, mock *cubeMockServer, httpTimeout time.Duration) *CubeRemoteClient {
	t.Helper()
	cfg := testConfig(t, mock)
	cfg.CubeHTTPTimeout = httpTimeout
	cfg.AllowPrivateEndpoints = true
	policy := OutboundURLPolicy{AllowPrivate: true}
	pool := NewSandboxGatewayTransportPoolWithPolicy(
		NewGuardedTransportWithPolicy(policy),
		policy,
	)
	client, err := NewCubeRemoteClientWithPool(cfg, pool)
	require.NoError(t, err)
	return client
}

func TestCubeOpenTerminalStreamSurvivesHTTPClientTimeout(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.ptyHoldAfterStart = 300 * time.Millisecond
	mock.ptyLatePayload = "late-frame"
	client := pooledCubeClient(t, mock, 120*time.Millisecond)

	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  time.Minute,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	session, err := client.OpenTerminal(ctx, handle, RemoteTerminalOptions{Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	deadline := time.After(time.Second)
	var got []byte
	for {
		select {
		case ev, ok := <-session.Output():
			if !ok {
				t.Fatalf("PTY stream closed before the late frame; collected %q", got)
			}
			if ev.Err != nil {
				t.Fatalf("PTY stream failed before the late frame: %v (collected %q)", ev.Err, got)
			}
			got = append(got, ev.Data...)
			if bytes.Contains(got, []byte("late-frame")) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for late PTY frame; collected %q", got)
		}
	}
}

func TestCubeOpenTerminalRefreshesSandboxTTL(t *testing.T) {
	prevMin := terminalTTLRefreshMin
	terminalTTLRefreshMin = 40 * time.Millisecond
	t.Cleanup(func() { terminalTTLRefreshMin = prevMin })

	mock := newCubeMockServer(t)
	mock.ptyHoldAfterStart = time.Second
	client := pooledCubeClient(t, mock, 5*time.Second)
	cfg := testConfig(t, mock)
	cfg.CubeSandboxTTL = 3 * time.Second
	client.config = cfg

	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  time.Minute,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	session, err := client.OpenTerminal(ctx, handle, RemoteTerminalOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	require.Eventually(t, func() bool {
		return mock.timeoutPOSTs.Load() >= 1
	}, 2*time.Second, 20*time.Millisecond, "expected SetTimeout while the PTY is open")
}

func TestCubeOpenTerminalReattachesByPID(t *testing.T) {
	mock := newCubeMockServer(t)
	client := pooledCubeClient(t, mock, 5*time.Second)

	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  time.Minute,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	session, err := client.OpenTerminal(ctx, handle, RemoteTerminalOptions{AttachPID: 99})
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	require.Equal(t, int32(1), mock.ptyConnectCount.Load())
	require.Equal(t, int32(0), mock.ptyStartCount.Load())
	require.Equal(t, uint32(4321), session.PID())
}

// RemoteTerminalSession documents Close as safe to call more than once, and
// the bridge can reach it from several goroutines. A check-then-close probe
// on the teardown channel would panic here.
func TestCubeTerminalCloseIsIdempotentUnderConcurrency(t *testing.T) {
	mock := newCubeMockServer(t)
	mock.ptyHoldAfterStart = time.Second
	client := pooledCubeClient(t, mock, 5*time.Second)

	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:   RemoteTimeoutExplicit,
			Value:  time.Minute,
			Action: RemoteOnTimeoutKill,
		},
	})
	require.NoError(t, err)

	session, err := client.OpenTerminal(ctx, handle, RemoteTerminalOptions{})
	require.NoError(t, err)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Output() {
		}
	}()

	// A shared start barrier: the racy version's window between "channel
	// looks open" and close() is only a few nanoseconds, so staggered
	// goroutines would sail past it.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_ = session.Close()
		}()
	}
	close(start)
	wg.Wait()

	// The pump must also terminate, closing Output exactly once.
	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("Output channel never closed after Close")
	}
}
