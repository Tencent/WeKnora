package interfaces

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// DocumentTagRepository handles document tag persistence.
type DocumentTagRepository interface {
	AssignTag(ctx context.Context, documentTag *types.DocumentTag) error
	UpdateTagAssignment(ctx context.Context, documentTag *types.DocumentTag) error
	RemoveTag(ctx context.Context, documentID string, tagID int64) error
	ListTagsByDocument(ctx context.Context, documentID string) ([]*types.Tag, error)
	ListDocumentsByTag(ctx context.Context, tagID int64) ([]*types.DocumentTag, error)
}
