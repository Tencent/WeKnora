package interfaces

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// EnhancedSearchService executes permission-aware and tag-aware searches.
type EnhancedSearchService interface {
	Search(ctx context.Context, request *types.EnhancedSearchRequest) (*types.EnhancedSearchResponse, error)
}
