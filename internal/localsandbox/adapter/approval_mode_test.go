package adapter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/localsandbox"
)

func TestAdapterSwitchesTheSessionApprovalMode(t *testing.T) {
	a := newTestAdapter(t)
	ctx := context.Background()

	mode, def := a.SessionApprovalMode(ctx, "s1")
	require.Equal(t, "ask", mode)
	require.Equal(t, "ask", def)

	require.NoError(t, a.SetSessionApprovalMode(ctx, "s1", "auto"))
	mode, def = a.SessionApprovalMode(ctx, "s1")
	require.Equal(t, "auto", mode)
	require.Equal(t, "ask", def)

	require.ErrorIs(t, a.SetSessionApprovalMode(ctx, "s1", "full"), localsandbox.ErrApprovalModeNotShipped)
}

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	return New(testService(t, &stubBackend{}))
}
