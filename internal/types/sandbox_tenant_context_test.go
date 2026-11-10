package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Every path that does not borrow another workspace's tenant must keep working
// without setting the key at all: there the request tenant already is the
// session owner. Session deletion relies on exactly this fallback.
func TestSandboxTenantIDFallsBackToRequestTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantIDContextKey, uint64(7))

	got, ok := SandboxTenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, uint64(7), got)
}

// A shared agent runs under the agent owner's tenant so models, KBs and named
// sandbox configs resolve in the sharing workspace. The sandbox binding must
// stay on the session owner regardless, because DeleteSession tears it down
// from a request that only ever knows the session's own tenant.
func TestSandboxTenantIDOverridesBorrowedTenant(t *testing.T) {
	const sessionOwner, agentOwner = uint64(7), uint64(99)

	ctx := context.WithValue(context.Background(), TenantIDContextKey, agentOwner)
	ctx = WithSandboxTenantID(ctx, sessionOwner)

	got, ok := SandboxTenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, sessionOwner, got,
		"binding tenant must be the session owner, not the borrowed agent tenant")

	// The borrowed tenant must survive untouched: it is what makes the shared
	// agent's own config resolve.
	borrowed, ok := TenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, agentOwner, borrowed)
}

// A zero tenant would silently poison the binding key, so it is ignored rather
// than recorded.
func TestWithSandboxTenantIDIgnoresZero(t *testing.T) {
	ctx := context.WithValue(context.Background(), TenantIDContextKey, uint64(7))
	ctx = WithSandboxTenantID(ctx, 0)

	got, ok := SandboxTenantIDFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, uint64(7), got)
}
