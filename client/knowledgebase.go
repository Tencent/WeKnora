// Package client provides the implementation for interacting with the WeKnora API
// The KnowledgeBase related interfaces are used to manage knowledge bases
// Knowledge bases are collections of knowledge entries that can be used for question-answering
// They can also be searched and queried using hybrid search
package client

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// KnowledgeBase represents a knowledge base
type KnowledgeBase struct {
	ID                    string                `json:"id"`
	Name                  string                `json:"name"` // Name must be unique within the same tenant
	Type                  string                `json:"type"`
	IsTemporary           bool                  `json:"is_temporary"`
	Description           string                `json:"description"`
	TenantID              uint64                `json:"tenant_id"`
	ChunkingConfig        ChunkingConfig        `json:"chunking_config"`
	ImageProcessingConfig ImageProcessingConfig `json:"image_processing_config"`
	FAQConfig             *FAQConfig            `json:"faq_config"`
	EmbeddingModelID      string                `json:"embedding_model_id"`
	SummaryModelID        string                `json:"summary_model_id"`
	VLMConfig             VLMConfig             `json:"vlm_config"`
	StorageConfig         StorageConfig         `json:"cos_config"`
	ExtractConfig         *ExtractConfig        `json:"extract_config"`
	CreatedAt             time.Time             `json:"created_at"`
	UpdatedAt             time.Time             `json:"updated_at"`
	// Computed fields (not stored in database)
	KnowledgeCount  int64 `json:"knowledge_count"`
	ChunkCount      int64 `json:"chunk_count"`
	IsProcessing    bool  `json:"is_processing"`
	ProcessingCount int64 `json:"processing_count"`
}

// KnowledgeBaseConfig represents knowledge base configuration
type KnowledgeBaseConfig struct {
	ChunkingConfig        ChunkingConfig        `json:"chunking_config"`
	ImageProcessingConfig ImageProcessingConfig `json:"image_processing_config"`
	FAQConfig             *FAQConfig            `json:"faq_config"`
}

// ChunkingConfig represents document chunking configuration
type ChunkingConfig struct {
	ChunkSize    int      `json:"chunk_size"`    // Chunk size
	ChunkOverlap int      `json:"chunk_overlap"` // Overlap size
	Separators   []string `json:"separators"`    // Separators
}

// FAQConfig represents faq-specific configuration
type FAQConfig struct {
	IndexMode         string `json:"index_mode"`
	QuestionIndexMode string `json:"question_index_mode"`
}

// ImageProcessingConfig represents image processing configuration
type ImageProcessingConfig struct {
	ModelID string `json:"model_id"` // Multimodal model ID
}

// VLMConfig represents the VLM configuration
type VLMConfig struct {
	Enabled bool   `json:"enabled"`
	ModelID string `json:"model_id"`
}

// StorageConfig represents the storage configuration
type StorageConfig struct {
	SecretID   string `json:"secret_id"`
	SecretKey  string `json:"secret_key"`
	Region     string `json:"region"`
	BucketName string `json:"bucket_name"`
	AppID      string `json:"app_id"`
	PathPrefix string `json:"path_prefix"`
	Provider   string `json:"provider"`
}

// ExtractConfig represents the extract configuration for a knowledge base
type ExtractConfig struct {
	Enabled   bool             `json:"enabled"`
	Text      string           `json:"text,omitempty"`
	Tags      []string         `json:"tags,omitempty"`
	Nodes     []*GraphNode     `json:"nodes,omitempty"`
	Relations []*GraphRelation `json:"relations,omitempty"`
}

// GraphNode represents a node in the graph extraction configuration
type GraphNode struct {
	Name string `json:"name"`
}

// GraphRelation represents a relation in the graph extraction configuration
type GraphRelation struct {
	Node1 string `json:"node1"`
	Node2 string `json:"node2"`
	Type  string `json:"type"`
}

// KnowledgeBaseResponse knowledge base response
type KnowledgeBaseResponse struct {
	Success bool          `json:"success"`
	Data    KnowledgeBase `json:"data"`
}

// KnowledgeBaseListResponse knowledge base list response
type KnowledgeBaseListResponse struct {
	Success bool            `json:"success"`
	Data    []KnowledgeBase `json:"data"`
}

// SearchResult represents search result
type SearchResult struct {
	ID                string            `json:"id"`
	Content           string            `json:"content"`
	KnowledgeID       string            `json:"knowledge_id"`
	ChunkIndex        int               `json:"chunk_index"`
	KnowledgeTitle    string            `json:"knowledge_title"`
	StartAt           int               `json:"start_at"`
	EndAt             int               `json:"end_at"`
	Seq               int               `json:"seq"`
	Score             float64           `json:"score"`
	ChunkType         string            `json:"chunk_type"`
	ImageInfo         string            `json:"image_info"`
	Metadata          map[string]string `json:"metadata"`
	KnowledgeFilename string            `json:"knowledge_filename"`
	KnowledgeSource   string            `json:"knowledge_source"`
	// MatchedContent is the actual content that was matched in vector search
	// For FAQ: this is the matched question text (standard or similar question)
	MatchedContent string `json:"matched_content,omitempty"`
}

// HybridSearchResponse hybrid search response
type HybridSearchResponse struct {
	Success bool            `json:"success"`
	Data    []*SearchResult `json:"data"`
}

type CopyKnowledgeBaseRequest struct {
	TaskID   string `json:"task_id,omitempty"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
}

// CopyKnowledgeBaseResponse represents the response from copy knowledge base API
type CopyKnowledgeBaseResponse struct {
	TaskID   string `json:"task_id"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Message  string `json:"message"`
}

// KBCloneProgress represents the progress of a knowledge base clone task
type KBCloneProgress struct {
	TaskID    string `json:"task_id"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Status    string `json:"status"`    // pending, processing, completed, failed
	Progress  int    `json:"progress"`  // 0-100
	Total     int    `json:"total"`     // Total operations count
	Processed int    `json:"processed"` // Processed operations count
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// CreateKnowledgeBase creates a knowledge base
func (c *Client) CreateKnowledgeBase(ctx context.Context, knowledgeBase *KnowledgeBase) (*KnowledgeBase, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/knowledge-bases", knowledgeBase, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeBaseResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetKnowledgeBase gets a knowledge base
func (c *Client) GetKnowledgeBase(ctx context.Context, knowledgeBaseID string) (*KnowledgeBase, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeBaseResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// ListKnowledgeBases lists knowledge bases
func (c *Client) ListKnowledgeBases(ctx context.Context) ([]KnowledgeBase, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/knowledge-bases", nil, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeBaseListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// UpdateKnowledgeBaseRequest update knowledge base request
type UpdateKnowledgeBaseRequest struct {
	Name        string               `json:"name"`
	Description string               `json:"description"`
	Config      *KnowledgeBaseConfig `json:"config"`
}

// UpdateKnowledgeBase updates a knowledge base
func (c *Client) UpdateKnowledgeBase(ctx context.Context,
	knowledgeBaseID string,
	request *UpdateKnowledgeBaseRequest,
) (*KnowledgeBase, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeBaseResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteKnowledgeBase deletes a knowledge base
func (c *Client) DeleteKnowledgeBase(ctx context.Context, knowledgeBaseID string) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// SearchParams represents the search parameters for hybrid search
type SearchParams struct {
	QueryText            string  `json:"query_text"`
	VectorThreshold      float64 `json:"vector_threshold"`
	KeywordThreshold     float64 `json:"keyword_threshold"`
	MatchCount           int     `json:"match_count"`
	DisableKeywordsMatch bool    `json:"disable_keywords_match"`
	DisableVectorMatch   bool    `json:"disable_vector_match"`
}

// HybridSearch performs hybrid search
// Note: The backend route is GET but expects JSON body, which is non-standard.
// This client uses POST with JSON body for better compatibility.
func (c *Client) HybridSearch(ctx context.Context, knowledgeBaseID string, params *SearchParams) ([]*SearchResult, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/hybrid-search", knowledgeBaseID)

	resp, err := c.doRequest(ctx, http.MethodGet, path, params, nil)
	if err != nil {
		return nil, err
	}

	var response HybridSearchResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// CopyKnowledgeBase copies a knowledge base asynchronously and returns task info
func (c *Client) CopyKnowledgeBase(ctx context.Context, request *CopyKnowledgeBaseRequest) (*CopyKnowledgeBaseResponse, error) {
	path := "/api/v1/knowledge-bases/copy"

	resp, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Success bool                      `json:"success"`
		Data    CopyKnowledgeBaseResponse `json:"data"`
	}

	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetKBCloneProgress gets the progress of a knowledge base clone task
func (c *Client) GetKBCloneProgress(ctx context.Context, taskID string) (*KBCloneProgress, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/copy/progress/%s", taskID)

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response struct {
		Success bool            `json:"success"`
		Data    KBCloneProgress `json:"data"`
	}

	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}
