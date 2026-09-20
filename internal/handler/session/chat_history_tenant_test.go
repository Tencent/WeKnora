package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type chatHistoryTenantStub struct {
	interfaces.TenantService
	tenants map[uint64]*types.Tenant
}

func (s *chatHistoryTenantStub) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	if tenant, ok := s.tenants[id]; ok {
		return tenant, nil
	}
	return nil, errors.New("tenant not found")
}

// A shared agent's run carries the agent workspace's tenant info; the chat
// history must still be indexed with the session owner's configuration.
func TestSessionTenantInfoContextUsesTheSessionOwner(t *testing.T) {
	owner := &types.Tenant{ID: 84}
	session := &types.Tenant{ID: 7}
	h := &Handler{tenantService: &chatHistoryTenantStub{tenants: map[uint64]*types.Tenant{7: session}}}

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, owner)
	got, ok := h.sessionTenantInfoContext(ctx)
	require.True(t, ok)
	info, _ := types.TenantInfoFromContext(got)
	require.Same(t, session, info)

	// Already the session owner's: unchanged.
	own := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	own = context.WithValue(own, types.TenantInfoContextKey, session)
	got, ok = h.sessionTenantInfoContext(own)
	require.True(t, ok)
	require.Equal(t, own, got)

	// Unknown session tenant: skip indexing rather than use the owner's KB.
	missing := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(9))
	missing = context.WithValue(missing, types.TenantInfoContextKey, owner)
	_, ok = h.sessionTenantInfoContext(missing)
	require.False(t, ok)
}
