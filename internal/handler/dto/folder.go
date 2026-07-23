package dto

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// FolderResponse is the public HTTP representation of a knowledge-base folder.
// Tenant and soft-delete fields are intentionally omitted because the route is
// already tenant scoped and deleted rows are not API resources.
type FolderResponse struct {
	ID              string    `json:"id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	ParentID        *string   `json:"parent_id"`
	Name            string    `json:"name"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NewFolderResponse converts a folder model to its public response shape.
func NewFolderResponse(folder *types.Folder) *FolderResponse {
	if folder == nil {
		return nil
	}
	return &FolderResponse{
		ID:              folder.ID,
		KnowledgeBaseID: folder.KnowledgeBaseID,
		ParentID:        folder.ParentID,
		Name:            folder.Name,
		CreatedAt:       folder.CreatedAt,
		UpdatedAt:       folder.UpdatedAt,
	}
}

// NewFolderResponses converts a folder slice while preserving an empty JSON
// array for empty results.
func NewFolderResponses(folders []*types.Folder) []*FolderResponse {
	responses := make([]*FolderResponse, 0, len(folders))
	for _, folder := range folders {
		responses = append(responses, NewFolderResponse(folder))
	}
	return responses
}
