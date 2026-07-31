package dto

import "github.com/Tencent/WeKnora/internal/types"

// CreateURLKnowledgeRequest contains client-controlled URL creation fields.
type CreateURLKnowledgeRequest struct {
	URL              string                           `json:"url" binding:"required"`
	FileName         string                           `json:"file_name"`
	FileType         string                           `json:"file_type"`
	EnableMultimodel *bool                            `json:"enable_multimodel"`
	Title            string                           `json:"title"`
	TagIDs           []string                         `json:"tag_ids"`
	Channel          string                           `json:"channel"`
	ProcessConfig    *types.KnowledgeProcessOverrides `json:"process_config"`
	FolderID         string                           `json:"folder_id"`
}

// CreateManualKnowledgeRequest contains client-controlled manual creation fields.
type CreateManualKnowledgeRequest struct {
	Title         string                           `json:"title"`
	Content       string                           `json:"content"`
	Status        string                           `json:"status"`
	TagIDs        []string                         `json:"tag_ids"`
	Channel       string                           `json:"channel"`
	ProcessConfig *types.KnowledgeProcessOverrides `json:"process_config,omitempty"`
	FolderID      string                           `json:"folder_id"`
}

// ToManualKnowledgePayload maps create-only fields to the existing service payload.
func (r CreateManualKnowledgeRequest) ToManualKnowledgePayload() *types.ManualKnowledgePayload {
	return &types.ManualKnowledgePayload{
		Title:         r.Title,
		Content:       r.Content,
		Status:        r.Status,
		TagIDs:        append([]string(nil), r.TagIDs...),
		Channel:       r.Channel,
		ProcessConfig: r.ProcessConfig,
	}
}
