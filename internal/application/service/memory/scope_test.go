package memory

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Memory follows the person who is chatting, not the tenant the turn executes
// in: a shared agent borrows its owner's workspace for models and knowledge,
// while the caller's memories stay in the caller's own workspace.
func TestResolveScopeUsesCallerWorkspaceAcrossExecutionTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "alice"})
	ctx = types.WithExecutionTenant(ctx, 42)

	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(7), scope.TenantID)
	require.Equal(t, "web_user:alice", scope.SubjectID)
}

// Contexts that never carried a Caller (embed visitors, background jobs built
// from a bare tenant) keep resolving against the execution tenant.
func TestResolveScopeFallsBackToExecutionTenantWithoutCaller(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalEmbedVisitor, ID: "visitor-1"})

	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(42), scope.TenantID)
	require.Equal(t, "embed_visitor:visitor-1", scope.SubjectID)
}

func TestResolveScopeRequiresTenantAndPrincipal(t *testing.T) {
	_, err := ResolveScope(context.Background())
	require.ErrorIs(t, err, ErrNoMemoryScope)
}
