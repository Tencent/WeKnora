package service

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type stubPluginLookup struct {
	info *types.PluginInfoForResource
}

func (s stubPluginLookup) LookupPluginByResource(
	context.Context, uint64, string, string,
) (*types.PluginInfoForResource, error) {
	return s.info, nil
}

func (s stubPluginLookup) ListPluginResourcesByKind(
	context.Context, uint64, string,
) (map[string]*types.PluginInfoForResource, error) {
	return nil, nil
}

func TestPluginOwnedSkillCannotBeDeletedDirectly(t *testing.T) {
	ctx := context.Background()
	owned := &TenantSkillService{pluginLookup: stubPluginLookup{
		info: &types.PluginInfoForResource{PluginName: "neon"},
	}}
	err := owned.refusePluginOwnedCatalog(ctx, 1, "cat-neon")
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, apperrors.ErrConflict, appErr.Code)

	plain := &TenantSkillService{pluginLookup: stubPluginLookup{}}
	require.NoError(t, plain.refusePluginOwnedCatalog(ctx, 1, "cat-local"))
	require.NoError(t, (&TenantSkillService{}).refusePluginOwnedCatalog(ctx, 1, "cat-local"))
}
