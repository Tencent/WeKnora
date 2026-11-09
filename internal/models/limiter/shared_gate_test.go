package limiter

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func sharedFixture(t *testing.T, limit, queue, burst int) {
	t.Helper()
	require.NoError(t, ConfigureSharedGate(SharedGateConfig{Enabled: true, Concurrency: limit, MaxWaiting: queue, InteractiveBurst: burst, MaxWait: time.Second}))
	require.NoError(t, RegisterModelEndpoint("chat-a", "http://127.0.0.1:8083/v1"))
	require.NoError(t, RegisterModelEndpoint("chat-alias", "http://127.0.0.1:8083/other"))
	require.NoError(t, RegisterModelEndpoint("embed", "http://127.0.0.1:8084/v1"))
	t.Cleanup(func() { require.NoError(t, ConfigureSharedGate(SharedGateConfig{})) })
}
func waitSharedWaiting(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for SharedRuntimeStats().Waiting != n {
		if time.Now().After(deadline) {
			t.Fatalf("queue did not reach %d: %+v", n, SharedRuntimeStats())
		}
		runtime.Gosched()
	}
}
func receiveShared[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("controlled worker deadline")
		var zero T
		return zero
	}
}

func TestSharedGateEndpointIdentity(t *testing.T) {
	a, err := EndpointGroup("HTTP://EXAMPLE.COM:80/v1")
	require.NoError(t, err)
	b, err := EndpointGroup("http://example.com/alias")
	require.NoError(t, err)
	require.Equal(t, a, b)
	c, err := EndpointGroup("http://example.com:8084/v1")
	require.NoError(t, err)
	require.NotEqual(t, a, c)
	for _, bad := range []string{"http://name:password@example.com", "http://example.com?api_key=secret", "http://example.com#secret", "file:///secret", "missing"} {
		_, err := EndpointGroup(bad)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		require.NotContains(t, err.Error(), "password")
	}
}
func TestSharedGateGlobalInflightAndSingleRelease(t *testing.T) {
	sharedFixture(t, 1, 3, 2)
	hold, err := AcquireCall(context.Background(), "chat-a", "", 0)
	require.NoError(t, err)
	entered := make(chan string, 2)
	releaseWorker := make(chan struct{})
	done := make(chan error, 2)
	var active, maxActive atomic.Int32
	// Holder represents a real provider call that remains in flight until release.
	active.Store(1)
	maxActive.Store(1)
	for _, id := range []string{"chat-alias", "embed"} {
		go func(id string) {
			rel, err := AcquireCall(context.Background(), id, "", 0)
			if err != nil {
				done <- err
				return
			}
			n := active.Add(1)
			for old := maxActive.Load(); n > old; old = maxActive.Load() {
				if maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			entered <- id
			<-releaseWorker
			active.Add(-1)
			rel()
			rel()
			done <- nil
		}(id)
	}
	waitSharedWaiting(t, 2)
	require.Equal(t, 1, SharedRuntimeStats().Active)
	select {
	case <-entered:
		t.Fatal("provider executed before grant")
	default:
	}
	active.Add(-1)
	hold()
	hold()
	receiveShared(t, entered)
	require.Equal(t, int32(1), maxActive.Load())
	releaseWorker <- struct{}{}
	receiveShared(t, entered)
	releaseWorker <- struct{}{}
	require.NoError(t, receiveShared(t, done))
	require.NoError(t, receiveShared(t, done))
	require.Zero(t, SharedRuntimeStats().Active)
	require.Zero(t, SharedRuntimeStats().Waiting)
}
func TestSharedGateCancellationOverflowAndBusyConfig(t *testing.T) {
	sharedFixture(t, 1, 1, 2)
	hold, err := AcquireCall(context.Background(), "chat-a", "", 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		rel, err := AcquireCall(ctx, "embed", "", 0)
		if err == nil {
			calls.Add(1)
			rel()
		}
		done <- err
	}()
	waitSharedWaiting(t, 1)
	rel, err := AcquireCall(context.Background(), "chat-alias", "", 0)
	require.Nil(t, rel)
	require.ErrorIs(t, err, ErrSharedQueueFull)
	require.ErrorIs(t, ConfigureSharedGate(SharedGateConfig{}), ErrSharedGateBusy)
	require.ErrorIs(t, RegisterModelEndpoint("chat-a", "http://127.0.0.1:9000"), ErrSharedGateBusy)
	cancel()
	hold()
	require.ErrorIs(t, receiveShared(t, done), context.Canceled)
	require.Zero(t, calls.Load())
	require.Zero(t, SharedRuntimeStats().Active)
	require.Zero(t, SharedRuntimeStats().Waiting)
	canceled, c := context.WithCancel(context.Background())
	c()
	rel, err = AcquireCall(canceled, "chat-a", "", 0)
	require.Nil(t, rel)
	require.ErrorIs(t, err, context.Canceled)
}
func TestSharedGateInteractivePriorityAndBackgroundFairness(t *testing.T) {
	sharedFixture(t, 1, 8, 2)
	hold, err := AcquireCall(context.Background(), "chat-a", "", 0)
	require.NoError(t, err)
	type grant struct {
		name    string
		release func()
	}
	granted := make(chan grant, 5)
	enqueue := func(name string, ctx context.Context, n int) {
		go func() {
			rel, err := AcquireCall(ctx, "embed", "", 0)
			if err != nil {
				granted <- grant{name: "ERROR"}
				return
			}
			granted <- grant{name, rel}
		}()
		waitSharedWaiting(t, n)
	}
	enqueue("background", types.WithBackgroundTask(context.Background()), 1)
	for i, name := range []string{"interactive1", "interactive2", "interactive3", "interactive4"} {
		enqueue(name, context.Background(), i+2)
	}
	hold()
	for _, expected := range []string{"interactive1", "interactive2", "background", "interactive3", "interactive4"} {
		g := receiveShared(t, granted)
		require.Equal(t, expected, g.name)
		require.NotNil(t, g.release)
		g.release()
	}
	require.Zero(t, SharedRuntimeStats().Active)
}
func TestSharedGateWaitDeadlineUnknownAndConfigValidation(t *testing.T) {
	sharedFixture(t, 1, 1, 2)
	require.NoError(t, ConfigureSharedGate(SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 1, InteractiveBurst: 2, MaxWait: 10 * time.Millisecond}))
	rel, err := AcquireCall(context.Background(), "unknown", "", 0)
	require.Nil(t, rel)
	require.ErrorIs(t, err, ErrSharedModelUnregistered)
	hold, err := AcquireCall(context.Background(), "chat-a", "", 0)
	require.NoError(t, err)
	rel, err = AcquireCall(context.Background(), "embed", "", 0)
	require.Nil(t, rel)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, SharedRuntimeStats().Waiting)
	hold()
	require.Error(t, ConfigureSharedGate(SharedGateConfig{Enabled: true}))
	require.Error(t, RegisterModelEndpoint("", "http://example.com"))
}
func TestSharedGateDefaultOffKeepsLegacy(t *testing.T) {
	require.NoError(t, ConfigureSharedGate(SharedGateConfig{}))
	SetGovernor(nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rel, err := AcquireCall(ctx, "unregistered", "", 0)
	require.NoError(t, err)
	require.NotNil(t, rel)
	rel()
}
func TestSharedGateCancelGrantRaceNeverExecutes(t *testing.T) {
	sharedFixture(t, 1, 1, 2)
	var calls atomic.Int32
	for i := 0; i < 50; i++ {
		hold, err := AcquireCall(context.Background(), "chat-a", "", 0)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			rel, err := AcquireCall(ctx, "embed", "", 0)
			if err == nil {
				calls.Add(1)
				rel()
			}
			done <- err
		}()
		waitSharedWaiting(t, 1)
		cancel()
		hold()
		require.True(t, errors.Is(receiveShared(t, done), context.Canceled))
		require.Zero(t, SharedRuntimeStats().Active)
	}
	require.Zero(t, calls.Load())
}

func TestSharedGateIndependentEndpointProgress(t *testing.T) {
	sharedFixture(t, 2, 4, 2)
	hold, err := AcquireCall(context.Background(), "chat-a", "", 1)
	require.NoError(t, err)
	defer hold()
	alias := make(chan func(), 1)
	go func() {
		rel, e := AcquireCall(context.Background(), "chat-alias", "", 1)
		if e == nil {
			alias <- rel
		}
	}()
	waitSharedWaiting(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	embed, err := AcquireCall(ctx, "embed", "", 1)
	require.NoError(t, err)
	embed()
	select {
	case rel := <-alias:
		rel()
		t.Fatal("alias exceeded physical endpoint limit")
	default:
	}
	hold()
	rel := receiveShared(t, alias)
	rel()
	require.Equal(t, 0, SharedRuntimeStats().Active)
}
