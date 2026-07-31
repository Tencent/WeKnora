package dto

import "github.com/Tencent/WeKnora/internal/types"

// KnowledgeFolderEnsurePathsResponse is the public ensure-paths result.
type KnowledgeFolderEnsurePathsResponse struct {
	Items []KnowledgeFolderEnsurePathResponse `json:"items"`
}

// KnowledgeFolderEnsurePathResponse maps one client path to its terminal folder.
type KnowledgeFolderEnsurePathResponse struct {
	ClientKey string `json:"client_key"`
	FolderID  string `json:"folder_id"`
}

// NewKnowledgeFolderEnsurePathsResponse maps results while preserving request order.
func NewKnowledgeFolderEnsurePathsResponse(
	results []types.KnowledgeFolderEnsurePathResult,
) *KnowledgeFolderEnsurePathsResponse {
	items := make([]KnowledgeFolderEnsurePathResponse, 0, len(results))
	for _, result := range results {
		items = append(items, KnowledgeFolderEnsurePathResponse{
			ClientKey: result.ClientKey,
			FolderID:  result.FolderID,
		})
	}
	return &KnowledgeFolderEnsurePathsResponse{Items: items}
}
