package dto

import "github.com/Tencent/WeKnora/internal/types"

// KnowledgeFolderMoveRequest keeps target_folder_id presence distinct from the
// explicit empty string used for the virtual knowledge-base root.
type KnowledgeFolderMoveRequest struct {
	KnowledgeIDs   []string `json:"knowledge_ids"    binding:"required,min=1,max=200"`
	TargetFolderID *string  `json:"target_folder_id" binding:"required"`
}

// KnowledgeFolderMoveResponse is the public counts-only move result.
type KnowledgeFolderMoveResponse struct {
	ChangedCount   int `json:"changed_count"`
	UnchangedCount int `json:"unchanged_count"`
}

// NewKnowledgeFolderMoveResponse maps a move result without exposing resource IDs.
func NewKnowledgeFolderMoveResponse(
	result *types.KnowledgeFolderMoveResult,
) *KnowledgeFolderMoveResponse {
	if result == nil {
		return nil
	}
	return &KnowledgeFolderMoveResponse{
		ChangedCount:   result.ChangedCount,
		UnchangedCount: result.UnchangedCount,
	}
}
