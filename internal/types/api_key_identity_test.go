package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplicationIdentitySessionAndOAuthOwnership(t *testing.T) {
	for _, kind := range []string{PrincipalAPIApplication, PrincipalAPIApplicationUser} {
		p := Principal{Type: kind, ID: "7:app-a:direct_header:user"}
		ctx := WithPrincipal(context.Background(), p)
		ctx = WithTenantAPIKeyScope(ctx, TenantAPIKeyScope{KeyID: 1})
		require.Equal(t, p.StorageID(), SessionOwnerIDFromContext(ctx))
		require.Equal(t, p, MCPOAuthPrincipalFromContext(ctx))
		require.True(t, IsAPISessionOwnerID(SessionOwnerIDFromContext(ctx)))
		ctx = WithTenantAPIKeyScope(ctx, TenantAPIKeyScope{KeyID: 2})
		require.Equal(t, p.StorageID(), SessionOwnerIDFromContext(ctx), "key rotation preserves the application owner")
	}
	ctx := context.WithValue(context.Background(), TenantIDContextKey, uint64(7))
	ctx = WithPrincipal(ctx, Principal{Type: PrincipalAPITenant, ID: "7"})
	ctx = WithTenantAPIKeyScope(ctx, TenantAPIKeyScope{KeyID: 99, LegacySessionKeyID: 12})
	require.Equal(t, "api_tenant_key:7:12", SessionOwnerIDFromContext(ctx))
}
