package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

type recordingAgentLookup struct {
	calls atomic.Int32
}

func (l *recordingAgentLookup) GetAgentByIDAndTenant(
	context.Context, string, uint64,
) (*types.CustomAgent, error) {
	l.calls.Add(1)
	return nil, errors.New("agent not found")
}

// Opening the terminal panel issues a GET, and a GET must not create billable
// infrastructure. OpenSessionTerminal is the path a page load and a background
// reconnect take, so it must stop at "no live sandbox" without ever reaching
// the provisioning machinery — only a confirmed click goes through
// EnsureSessionTerminal.
func TestOpenSessionTerminalNeverReachesProvisioning(t *testing.T) {
	lookup := &recordingAgentLookup{}
	svc := NewSandboxTerminalService(nil, nil, nil, nil, lookup)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	_, err := svc.OpenSessionTerminal(ctx, "sess-1", sandbox.RemoteTerminalOptions{})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	require.Zero(t, lookup.calls.Load(),
		"a lookup-only open must not consult the agent's sandbox config")

	_, err = svc.EnsureSessionTerminal(ctx, "sess-1", "agent-1", sandbox.RemoteTerminalOptions{})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	require.Equal(t, int32(1), lookup.calls.Load(),
		"a confirmed open must resolve the agent config to know where to build")
}
