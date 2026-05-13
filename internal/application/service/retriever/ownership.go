package retriever

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// vectorStoreRepoOwnership adapts a VectorStoreRepository to the
// TenantStoreOwnership interface consumed by the factory functions.
//
// The repository's GetByID already scopes to the given tenantID
// (WHERE id = ? AND tenant_id = ?), so "exists under this tenant"
// is equivalent to "owned by this tenant" — no additional tenant
// comparison is needed on top of the returned row.
type vectorStoreRepoOwnership struct {
	repo interfaces.VectorStoreRepository
}

// NewVectorStoreRepoOwnership returns the production
// TenantStoreOwnership implementation backed by VectorStoreRepository.
func NewVectorStoreRepoOwnership(repo interfaces.VectorStoreRepository) TenantStoreOwnership {
	return &vectorStoreRepoOwnership{repo: repo}
}

// StoreOwnedBy returns true iff a vector store with the given ID exists
// under the given tenant. Errors are reserved for infrastructure failures;
// a non-existent (but well-formed) store ID returns (false, nil).
func (o *vectorStoreRepoOwnership) StoreOwnedBy(ctx context.Context, storeID string, tenantID uint64) (bool, error) {
	store, err := o.repo.GetByID(ctx, tenantID, storeID)
	if err != nil {
		return false, err
	}
	return store != nil, nil
}
