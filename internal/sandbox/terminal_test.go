package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A pump that emits after the consumer walked away must not block. The
// stranded goroutine would never reach its `defer close(out)`, holding the
// provider's PTY stream open for the life of the process.
func TestEmitTerminalEventGivesUpWhenClosed(t *testing.T) {
	t.Parallel()
	out := make(chan RemoteTerminalEvent) // unbuffered: nobody is draining
	closed := make(chan struct{})
	close(closed)

	done := make(chan struct{})
	go func() {
		defer close(done)
		emitTerminalEvent(out, closed, RemoteTerminalEvent{Exited: true})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emitTerminalEvent blocked on a channel nobody drains")
	}
}

func TestEmitTerminalEventDeliversToLiveConsumer(t *testing.T) {
	t.Parallel()
	out := make(chan RemoteTerminalEvent, 1)

	emitTerminalEvent(out, make(chan struct{}), RemoteTerminalEvent{Data: []byte("hi")})

	require.Equal(t, []byte("hi"), (<-out).Data)
}
