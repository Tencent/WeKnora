package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// Opening the terminal panel issues a GET, and a GET must not create billable
// infrastructure. OpenSessionTerminal is the path a page load and a background
// reconnect take, so it must stop at "no live sandbox". A confirmed click
// without a resolved sandbox config ID stays lookup-only as well.
func TestOpenSessionTerminalNeverReachesProvisioning(t *testing.T) {
	svc := NewSandboxTerminalService(nil, nil, nil, nil)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))

	_, err := svc.OpenSessionTerminal(ctx, "sess-1", sandbox.RemoteTerminalOptions{})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)

	_, err = svc.EnsureSessionTerminal(ctx, "sess-1", "", sandbox.RemoteTerminalOptions{})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
}
