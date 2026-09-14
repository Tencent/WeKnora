package browserskill

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHumanWaitBudgets(t *testing.T) {
	for _, method := range []string{"request_help", "tab_borrow"} {
		require.Equal(t, HumanStepTimeout, clusterTimeout("call", method))
	}
	require.Equal(t, 2*time.Minute, clusterTimeout("call", "click"))
	require.Equal(t, 2*time.Minute, clusterTimeout("control", "resume"))
	for _, value := range []any{nil, 600001, float64(600001), json.Number("600001"), -1} {
		require.Equal(t, int64(300000), humanTimeoutMS(value))
	}
	for _, value := range []any{1500, float64(1500), int64(1500), json.Number("1500")} {
		require.Equal(t, int64(1500), humanTimeoutMS(value))
	}
}

func TestNativeTimeoutAndCancellationPauseTask(t *testing.T) {
	for _, code := range []string{"timeout", "cancelled", "user_aborted"} {
		require.True(t, interruptedCommand(&RPCError{Code: code, Message: "tool RPC timed out after 1.5s"}))
	}
	require.False(t, interruptedCommand(nil))
	require.False(t, interruptedCommand(&RPCError{Code: "not_found", Message: "ref missing"}))
}

func TestWindowInterruptIsConsumedOnlyAtGateway(t *testing.T) {
	m := NewManager()
	s := Scope{1, "alice"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	current := &task{
		id: "task", selected: true, helpCalls: 1, helpPrompt: "Complete login",
		calls: map[uint64]context.CancelFunc{1: cancel},
	}
	d := &device{tasks: map[string]*task{"chat": current}}
	m.devices[s.key()] = d
	require.Equal(t, "Complete login", m.Status(s, "chat").HelpPrompt)
	require.True(t, m.observe(d, []byte(`{"event":"session.user_interrupt","payload":{"session_id":"foreign"}}`)))
	require.False(t, current.paused)
	require.NoError(t, ctx.Err())
	require.True(t, m.observe(d, []byte(`{"event":"session.user_interrupt","payload":{"session_id":"task"}}`)))
	require.True(t, current.paused)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.Empty(t, m.Status(s, "chat").HelpPrompt)
	require.False(t, m.Status(s, "chat").NeedsHelp)
	require.False(t, m.observe(d, []byte(`{"event":"session.window_closed","payload":{"session_id":"task"}}`)))
	require.Empty(t, current.id, "window-close still reaches the daemon to clean up its session")
}
