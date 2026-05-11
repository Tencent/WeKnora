// Package client provides the implementation for interacting with the WeKnora API
// This package encapsulates CRUD operations for server resources and provides a friendly interface for callers
// The Chunk related interfaces are used to manage document chunks in the knowledge base
package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Chunk represents the information about a document chunk
// Chunks are the basic units of storage and indexing in the knowledge base
type Chunk struct {
	ID                     string `json:"id"`                        // Unique identifier of the chunk
	SeqID                  int64  `json:"seq_id"`                    // Auto-increment integer ID for external API usage
	KnowledgeID            string `json:"knowledge_id"`              // Identifier of the parent knowledge
	KnowledgeBaseID        string `json:"knowledge_base_id"`         // ID of the knowledge base
	TenantID               uint64 `json:"tenant_id"`                 // Tenant ID
	TagID                  string `json:"tag_id"`                    // Optional tag ID for categorization
	Content                string `json:"content"`                   // Text content of the chunk
	ChunkIndex             int    `json:"chunk_index"`               // Index position of chunk in the document
	IsEnabled              bool   `json:"is_enabled"`                // Whether this chunk is enabled
	Status                 int    `json:"status"`                    // Status of the chunk
	StartAt                int    `json:"start_at"`                  // Starting position in original text
	EndAt                  int    `json:"end_at"`                    // Ending position in original text
	PreChunkID             string `json:"pre_chunk_id"`              // Previous chunk ID
	NextChunkID            string `json:"next_chunk_id"`             // Next chunk ID
	ChunkType              string `json:"chunk_type"`                // Chunk type (text, image_ocr, etc.)
	ParentChunkID          string `json:"parent_chunk_id"`           // Parent chunk ID
	RelationChunks         any    `json:"relation_chunks"`           // Relation chunk IDs
	IndirectRelationChunks any    `json:"indirect_relation_chunks"`  // Indirect relation chunk IDs
	Metadata               any    `json:"metadata"`                  // Metadata for the chunk
	ContentHash            string `json:"content_hash"`              // Content hash for quick matching
	ImageInfo              string `json:"image_info"`                // Image information
	CreatedAt              string `json:"created_at"`                // Creation time
	UpdatedAt              string `json:"updated_at"`                // Last update time
}

// ChunkResponse represents the response for a single chunk
// API response structure containing a single chunk information
type ChunkResponse struct {
	Success bool  `json:"success"` // Whether operation was successful
	Data    Chunk `json:"data"`    // Chunk data
}

// ChunkListResponse represents the response for a list of chunks
// API response structure for returning a list of chunks
type ChunkListResponse struct {
	Success  bool    `json:"success"`   // Whether operation was successful
	Data     []Chunk `json:"data"`      // List of chunks
	Total    int64   `json:"total"`     // Total count
	Page     int     `json:"page"`      // Current page
	PageSize int     `json:"page_size"` // Items per page
}

// UpdateChunkRequest represents the request structure for updating a chunk
// Used for requesting chunk information updates
type UpdateChunkRequest struct {
	Content    string    `json:"content"`     // Chunk content
	Embedding  []float32 `json:"embedding"`   // Vector embedding
	ChunkIndex int       `json:"chunk_index"` // Chunk index
	IsEnabled  bool      `json:"is_enabled"`  // Whether enabled
	StartAt    int       `json:"start_at"`    // Start position
	EndAt      int       `json:"end_at"`      // End position
	ImageInfo  string    `json:"image_info"`  // Image information
}

// ListKnowledgeChunks lists all chunks under a knowledge document
// Queries all chunks by knowledge ID with pagination support
// Parameters:
//   - ctx: Context
//   - knowledgeID: Knowledge ID
//   - page: Page number, starts from 1
//   - pageSize: Number of items per page
//
// Returns:
//   - []Chunk: List of chunks
//   - int64: Total count
//   - error: Error information
func (c *Client) ListKnowledgeChunks(ctx context.Context,
	knowledgeID string, page int, pageSize int,
) ([]Chunk, int64, error) {
	path := fmt.Sprintf("/api/v1/chunks/%s", knowledgeID)

	queryParams := url.Values{}
	queryParams.Add("page", strconv.Itoa(page))
	queryParams.Add("page_size", strconv.Itoa(pageSize))

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, queryParams)
	if err != nil {
		return nil, 0, err
	}

	var response ChunkListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, 0, err
	}

	return response.Data, response.Total, nil
}

// UpdateChunk updates a chunk's information
// Updates information for a specific chunk under a knowledge document
// Parameters:
//   - ctx: Context
//   - knowledgeID: Knowledge ID
//   - chunkID: Chunk ID
//   - request: Update request
//
// Returns:
//   - *Chunk: Updated chunk
//   - error: Error information
func (c *Client) UpdateChunk(ctx context.Context,
	knowledgeID string, chunkID string, request *UpdateChunkRequest,
) (*Chunk, error) {
	path := fmt.Sprintf("/api/v1/chunks/%s/%s", knowledgeID, chunkID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response ChunkResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteChunk deletes a specific chunk
// Deletes a specific chunk under a knowledge document
// Parameters:
//   - ctx: Context
//   - knowledgeID: Knowledge ID
//   - chunkID: Chunk ID
//
// Returns:
//   - error: Error information
func (c *Client) DeleteChunk(ctx context.Context, knowledgeID string, chunkID string) error {
	path := fmt.Sprintf("/api/v1/chunks/%s/%s", knowledgeID, chunkID)
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

// GetChunkByIDOnly retrieves a chunk by its ID without requiring knowledge ID
func (c *Client) GetChunkByIDOnly(ctx context.Context, chunkID string) (*Chunk, error) {
	path := fmt.Sprintf("/api/v1/chunks/by-id/%s", chunkID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response ChunkResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// DeleteGeneratedQuestion deletes a generated question from a chunk
func (c *Client) DeleteGeneratedQuestion(ctx context.Context, chunkID string, questionID string) error {
	path := fmt.Sprintf("/api/v1/chunks/%s/delete-question", chunkID)
	req := map[string]string{"question_id": questionID}
	resp, err := c.doRequest(ctx, http.MethodDelete, path, req, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// DeleteChunksByKnowledgeID deletes all chunks under a knowledge document
// Batch deletes all chunks under the specified knowledge document
// Parameters:
//   - ctx: Context
//   - knowledgeID: Knowledge ID
//
// Returns:
//   - error: Error information
func (c *Client) DeleteChunksByKnowledgeID(ctx context.Context, knowledgeID string) error {
	path := fmt.Sprintf("/api/v1/chunks/%s", knowledgeID)
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
