package interfaces

import "context"

// KnowledgeFolderPlacementResolver validates folder placement for knowledge creation.
type KnowledgeFolderPlacementResolver interface {
	ResolveForCreate(
		ctx context.Context,
		knowledgeBaseID string,
		rawFolderID string,
	) (string, error)
}
