package dto

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

func adminContext() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleAdmin)
}

func viewerContext() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
}

func ownerContext() context.Context {
	ctx := context.Background()
	return context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
}
