package handler

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// resolveAPIKeySearchScopes binds persisted KB IDs to their actual owner, never
// to the caller's workspace. Revoked/deleted shares are omitted; infrastructure
// errors abort the search rather than presenting misleading partial results.
func resolveAPIKeySearchScopes(
	ctx context.Context,
	lookup func(context.Context, string) (*types.KnowledgeBase, error),
	shares access.KBShareLookup,
) ([]types.KnowledgeSearchScope, bool, error) {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || scope.KnowledgeBasePermissions == nil {
		legacy, restricted := tenantAPIKeySearchScopes(ctx)
		return legacy, restricted, nil
	}
	permissions := access.NewKBPermissions(ctx, shares)
	result := make([]types.KnowledgeSearchScope, 0, len(scope.KnowledgeBaseIDs))
	for _, id := range scope.KnowledgeBaseIDs {
		kb, err := lookup(ctx, id)
		if errors.Is(err, repository.ErrKnowledgeBaseNotFound) || err == nil && kb == nil {
			continue
		}
		if err != nil {
			return nil, true, err
		}
		allowed, err := permissions.Check(id, kb.TenantID, types.OrgRoleViewer)
		if err != nil {
			return nil, true, err
		}
		if allowed {
			result = append(result, types.KnowledgeSearchScope{TenantID: kb.TenantID, KBID: id})
		}
	}
	return result, true, nil
}
