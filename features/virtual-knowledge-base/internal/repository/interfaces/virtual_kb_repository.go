package interfaces

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// VirtualKBRepository handles persistence for virtual knowledge bases.
type VirtualKBRepository interface {
	Create(ctx context.Context, vkb *types.VirtualKB) error
	Update(ctx context.Context, vkb *types.VirtualKB) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*types.VirtualKB, error)
	List(ctx context.Context) ([]*types.VirtualKB, error)
}
