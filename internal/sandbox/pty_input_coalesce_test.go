package sandbox

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPtyInputCoalescerWriteDoesNotWaitForSendRTT(t *testing.T) {
	var mu sync.Mutex
	var got []byte
	var calls int

	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })

	c := newPtyInputCoalescer(context.Background(), closed, func(_ context.Context, data []byte) error {
		time.Sleep(40 * time.Millisecond)
		mu.Lock()
		calls++
		got = append(got, data...)
		mu.Unlock()
		return nil
	})

	ctx := context.Background()
	start := time.Now()
	for _, b := range []byte("hello") {
		require.NoError(t, c.Write(ctx, []byte{b}))
	}
	require.Less(t, time.Since(start), 25*time.Millisecond,
		"Write must enqueue instead of waiting for each SendInput round-trip")

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return string(got) == "hello"
	}, time.Second, 5*time.Millisecond)

	mu.Lock()
	n := calls
	mu.Unlock()
	require.Less(t, n, 5, "burst keystrokes should share SendInput RPCs, got %d", n)
}

func TestPtyInputCoalescerSendsFirstChunkWithoutWaiting(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	t.Cleanup(func() { close(closed) })

	c := newPtyInputCoalescer(context.Background(), closed, func(_ context.Context, _ []byte) error {
		select {
		case <-started:
		default:
			close(started)
		}
		time.Sleep(50 * time.Millisecond)
		return nil
	})

	require.NoError(t, c.Write(context.Background(), []byte("x")))
	select {
	case <-started:
	case <-time.After(20 * time.Millisecond):
		t.Fatal("first keystroke must start SendInput immediately, not after a coalesce timer")
	}
}
