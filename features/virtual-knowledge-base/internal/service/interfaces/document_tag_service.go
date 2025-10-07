package interfaces

import (
	"context"

	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// DocumentTagService manages document-tag assignments.
type DocumentTagService interface {
	AssignTag(ctx context.Context, assignment *types.DocumentTag) error
	UpdateTag(ctx context.Context, assignment *types.DocumentTag) error
	RemoveTag(ctx context.Context, documentID string, tagID int64) error
	ListTags(ctx context.Context, documentID string) ([]*types.Tag, error)
	ListDocuments(ctx context.Context, tagID int64) ([]*types.DocumentTag, error)
}
