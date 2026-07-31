package dto

import (
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// KnowledgeFolderResponse is the public folder shape used by create and update responses.
type KnowledgeFolderResponse struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id"`
	Name      string    `json:"name"`
	Depth     int       `json:"depth"`
	SortOrder int       `json:"sort_order"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// KnowledgeFolderWithStatsResponse is the public folder shape used by list and get responses.
type KnowledgeFolderWithStatsResponse struct {
	ID             string    `json:"id"`
	ParentID       string    `json:"parent_id"`
	Name           string    `json:"name"`
	Depth          int       `json:"depth"`
	SortOrder      int       `json:"sort_order"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	KnowledgeCount int64     `json:"knowledge_count"`
	HasChildren    bool      `json:"has_children"`
}

// KnowledgeFolderBreadcrumbItemResponse is the public folder shape used by breadcrumb responses.
type KnowledgeFolderBreadcrumbItemResponse struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	Name     string `json:"name"`
	Depth    int    `json:"depth"`
}

// NewKnowledgeFolderResponse maps a persisted folder to its public response shape.
func NewKnowledgeFolderResponse(folder *types.KnowledgeFolder) *KnowledgeFolderResponse {
	if folder == nil {
		return nil
	}
	return &KnowledgeFolderResponse{
		ID:        folder.ID,
		ParentID:  folder.ParentID,
		Name:      folder.Name,
		Depth:     folder.Depth,
		SortOrder: folder.SortOrder,
		CreatedAt: folder.CreatedAt,
		UpdatedAt: folder.UpdatedAt,
	}
}

// NewKnowledgeFolderWithStatsResponse maps an enriched folder to its public response shape.
func NewKnowledgeFolderWithStatsResponse(folder *types.KnowledgeFolderWithStats) *KnowledgeFolderWithStatsResponse {
	if folder == nil {
		return nil
	}
	return &KnowledgeFolderWithStatsResponse{
		ID:             folder.ID,
		ParentID:       folder.ParentID,
		Name:           folder.Name,
		Depth:          folder.Depth,
		SortOrder:      folder.SortOrder,
		CreatedAt:      folder.CreatedAt,
		UpdatedAt:      folder.UpdatedAt,
		KnowledgeCount: folder.KnowledgeCount,
		HasChildren:    folder.HasChildren,
	}
}

// NewKnowledgeFolderBreadcrumbItemResponse maps a folder to its public breadcrumb shape.
func NewKnowledgeFolderBreadcrumbItemResponse(folder *types.KnowledgeFolder) *KnowledgeFolderBreadcrumbItemResponse {
	if folder == nil {
		return nil
	}
	return &KnowledgeFolderBreadcrumbItemResponse{
		ID:       folder.ID,
		ParentID: folder.ParentID,
		Name:     folder.Name,
		Depth:    folder.Depth,
	}
}

// NewKnowledgeFolderWithStatsResponses maps folders while preserving order and an empty-array contract.
func NewKnowledgeFolderWithStatsResponses(
	folders []*types.KnowledgeFolderWithStats,
) []*KnowledgeFolderWithStatsResponse {
	responses := make([]*KnowledgeFolderWithStatsResponse, 0, len(folders))
	for _, folder := range folders {
		responses = append(responses, NewKnowledgeFolderWithStatsResponse(folder))
	}
	return responses
}

// NewKnowledgeFolderBreadcrumbItemResponses maps breadcrumbs while preserving order and an empty-array contract.
func NewKnowledgeFolderBreadcrumbItemResponses(
	folders []*types.KnowledgeFolder,
) []*KnowledgeFolderBreadcrumbItemResponse {
	responses := make([]*KnowledgeFolderBreadcrumbItemResponse, 0, len(folders))
	for _, folder := range folders {
		responses = append(responses, NewKnowledgeFolderBreadcrumbItemResponse(folder))
	}
	return responses
}
