// Package client provides the implementation for interacting with the WeKnora API
// The Knowledge related interfaces are used to manage knowledge entries in the knowledge base
// Knowledge entries can be created from local files, web URLs, or directly from text content
// They can also be retrieved, deleted, and downloaded as files
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

// Knowledge represents knowledge information
type Knowledge struct {
	ID               string          `json:"id"`
	TenantID         uint64          `json:"tenant_id"`
	KnowledgeBaseID  string          `json:"knowledge_base_id"`
	TagID            string          `json:"tag_id"`
	Type             string          `json:"type"`
	Title            string          `json:"title"`
	Description      string          `json:"description"`
	Source           string          `json:"source"`
	Channel          string          `json:"channel"`
	ParseStatus      string          `json:"parse_status"`
	SummaryStatus    string          `json:"summary_status"`
	EnableStatus     string          `json:"enable_status"`
	EmbeddingModelID string          `json:"embedding_model_id"`
	FileName         string          `json:"file_name"`
	FileType         string          `json:"file_type"`
	FileSize         int64           `json:"file_size"`
	FileHash         string          `json:"file_hash"`
	FilePath         string          `json:"file_path"`
	StorageSize      int64           `json:"storage_size"`
	Metadata         json.RawMessage `json:"metadata"` // Extensible metadata for storing machine information, paths, etc.
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	ProcessedAt      *time.Time      `json:"processed_at"`
	ErrorMessage     string          `json:"error_message"`
}

// KnowledgeResponse represents the API response containing a single knowledge entry
type KnowledgeResponse struct {
	Success bool      `json:"success"`
	Data    Knowledge `json:"data"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
}

// KnowledgeListResponse represents the API response containing a list of knowledge entries with pagination
type KnowledgeListResponse struct {
	Success  bool        `json:"success"`
	Data     []Knowledge `json:"data"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// KnowledgeBatchResponse represents the API response for batch knowledge retrieval
type KnowledgeBatchResponse struct {
	Success bool        `json:"success"`
	Data    []Knowledge `json:"data"`
}

// UpdateImageInfoRequest represents the request structure for updating a chunk
// Used for requesting chunk information updates
type UpdateImageInfoRequest struct {
	ImageInfo string `json:"image_info"` // Image information in JSON format
}

// ErrDuplicateFile is returned when attempting to create a knowledge entry with a file that already exists
var ErrDuplicateFile = errors.New("file already exists")

// ErrDuplicateURL is returned when attempting to create a knowledge entry with a URL that already exists
var ErrDuplicateURL = errors.New("URL already exists")

// CreateKnowledgeFromFile creates a knowledge entry from a local file path
// Parameters:
//   - knowledgeBaseID: The ID of the knowledge base
//   - filePath: The local file path
//   - metadata: Optional metadata for the knowledge entry
//   - enableMultimodel: Optional flag to enable multimodal processing
//   - customFileName: Optional custom file name (useful for folder uploads with path)
//   - channel: Optional ingestion channel (e.g. "web", "api", "wechat"); empty defaults to "web"
func (c *Client) CreateKnowledgeFromFile(ctx context.Context,
	knowledgeBaseID string, filePath string, metadata map[string]string, enableMultimodel *bool, customFileName string, channel string,
) (*Knowledge, error) {
	// Open the local file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	// Get file information
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file information: %w", err)
	}

	// Create the HTTP request
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge/file", knowledgeBaseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Create a multipart form writer
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", fileInfo.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	// Copy file contents
	_, err = io.Copy(part, file)
	if err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	// Add enable_multimodel field
	if enableMultimodel != nil {
		if err := writer.WriteField("enable_multimodel", strconv.FormatBool(*enableMultimodel)); err != nil {
			return nil, fmt.Errorf("failed to write enable_multimodel field: %w", err)
		}
	}

	// Add metadata to the request if provided
	if metadata != nil {
		metadataBytes, err := json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("failed to serialize metadata: %w", err)
		}
		if err := writer.WriteField("metadata", string(metadataBytes)); err != nil {
			return nil, fmt.Errorf("failed to write metadata field: %w", err)
		}
	}

	// Add custom file name if provided
	if customFileName != "" {
		if err := writer.WriteField("fileName", customFileName); err != nil {
			return nil, fmt.Errorf("failed to write fileName field: %w", err)
		}
	}

	if channel != "" {
		if err := writer.WriteField("channel", channel); err != nil {
			return nil, fmt.Errorf("failed to write channel field: %w", err)
		}
	}

	// Close the multipart writer
	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthHeaders(ctx, req)

	// Set the request body
	req.Body = io.NopCloser(body)

	// Send the request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Parse the response
	var response KnowledgeResponse
	if resp.StatusCode == http.StatusConflict {
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		return &response.Data, ErrDuplicateFile
	} else if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// CreateKnowledgeFromURLRequest contains the parameters for creating a knowledge entry from a URL.
// When FileName or FileType is provided (or the URL path has a known file extension such as .pdf/.docx/.doc/.txt/.md),
// the server automatically switches to file-download mode instead of web-page crawling.
type CreateKnowledgeFromURLRequest struct {
	// URL is the target URL (required)
	URL string `json:"url"`
	// FileName is the optional file name; used to hint file-download mode when URL has no extension
	FileName string `json:"file_name,omitempty"`
	// FileType is the optional file type (e.g. "pdf"); used to hint file-download mode
	FileType string `json:"file_type,omitempty"`
	// EnableMultimodel is the optional flag to enable multimodal processing
	EnableMultimodel *bool `json:"enable_multimodel,omitempty"`
	// Title is the optional title for the knowledge entry
	Title string `json:"title,omitempty"`
	// TagID is the optional tag ID to associate with the knowledge entry
	TagID string `json:"tag_id,omitempty"`
	// Channel identifies the ingestion channel (e.g. "web", "browser_extension", "api")
	Channel string `json:"channel,omitempty"`
}

// CreateKnowledgeFromURL creates a knowledge entry from a URL.
// When req.FileName or req.FileType is provided (or the URL path has a known file extension),
// the server automatically switches to file-download mode instead of web-page crawling.
func (c *Client) CreateKnowledgeFromURL(
	ctx context.Context,
	knowledgeBaseID string,
	req CreateKnowledgeFromURLRequest,
) (*Knowledge, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge/url", knowledgeBaseID)

	reqBody := req

	resp, err := c.doRequest(ctx, http.MethodPost, path, reqBody, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeResponse
	if resp.StatusCode == http.StatusConflict {
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return nil, fmt.Errorf("failed to parse response: %w", err)
		}
		return &response.Data, ErrDuplicateURL
	} else if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetKnowledge retrieves a knowledge entry by its ID
func (c *Client) GetKnowledge(ctx context.Context, knowledgeID string) (*Knowledge, error) {
	path := fmt.Sprintf("/api/v1/knowledge/%s", knowledgeID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// GetKnowledgeBatch retrieves multiple knowledge entries by their IDs
func (c *Client) GetKnowledgeBatch(ctx context.Context, knowledgeIDs []string) ([]Knowledge, error) {
	path := "/api/v1/knowledge/batch"

	queryParams := url.Values{}
	for _, id := range knowledgeIDs {
		queryParams.Add("ids", id)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, queryParams)
	if err != nil {
		return nil, err
	}

	var response KnowledgeBatchResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// ListKnowledge lists knowledge entries in a knowledge base with pagination.
// For richer filtering (keyword, file type, parse status, source, time range)
// use ListKnowledgeWithFilter.
func (c *Client) ListKnowledge(ctx context.Context,
	knowledgeBaseID string,
	page int,
	pageSize int,
	tagID string,
) ([]Knowledge, int64, error) {
	return c.ListKnowledgeWithFilter(ctx, knowledgeBaseID, page, pageSize, KnowledgeListFilter{TagID: tagID})
}

// KnowledgeListFilter mirrors the server-side filters accepted by GET
// /api/v1/knowledge-bases/{id}/knowledge. Empty / zero fields are omitted from
// the request.
type KnowledgeListFilter struct {
	TagID       string
	Keyword     string
	FileType    string
	ParseStatus string
	Source      string
	// StartTime / EndTime filter on knowledge updated_at. Zero values are skipped.
	// They are serialized in RFC3339 format.
	StartTime time.Time
	EndTime   time.Time
}

// ListKnowledgeWithFilter lists knowledge entries with the full filter surface.
func (c *Client) ListKnowledgeWithFilter(ctx context.Context,
	knowledgeBaseID string,
	page int,
	pageSize int,
	filter KnowledgeListFilter,
) ([]Knowledge, int64, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge", knowledgeBaseID)

	queryParams := url.Values{}
	queryParams.Add("page", strconv.Itoa(page))
	queryParams.Add("page_size", strconv.Itoa(pageSize))
	if filter.TagID != "" {
		queryParams.Add("tag_id", filter.TagID)
	}
	if filter.Keyword != "" {
		queryParams.Add("keyword", filter.Keyword)
	}
	if filter.FileType != "" {
		queryParams.Add("file_type", filter.FileType)
	}
	if filter.ParseStatus != "" {
		queryParams.Add("parse_status", filter.ParseStatus)
	}
	if filter.Source != "" {
		queryParams.Add("source", filter.Source)
	}
	if !filter.StartTime.IsZero() {
		queryParams.Add("start_time", filter.StartTime.Format(time.RFC3339))
	}
	if !filter.EndTime.IsZero() {
		queryParams.Add("end_time", filter.EndTime.Format(time.RFC3339))
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, queryParams)
	if err != nil {
		return nil, 0, err
	}

	var response KnowledgeListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, 0, err
	}

	return response.Data, response.Total, nil
}

// DeleteKnowledge deletes a knowledge entry by its ID
func (c *Client) DeleteKnowledge(ctx context.Context, knowledgeID string) error {
	path := fmt.Sprintf("/api/v1/knowledge/%s", knowledgeID)
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

// DownloadKnowledgeFile downloads a knowledge file to the specified local path
func (c *Client) DownloadKnowledgeFile(ctx context.Context, knowledgeID string, destPath string) error {
	path := fmt.Sprintf("/api/v1/knowledge/%s/download", knowledgeID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for HTTP errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	// Create destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer out.Close()

	// Copy response body to file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func (c *Client) UpdateKnowledge(ctx context.Context, knowledge *Knowledge) error {
	path := fmt.Sprintf("/api/v1/knowledge/%s", knowledge.ID)

	resp, err := c.doRequest(ctx, http.MethodPut, path, knowledge, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// ReparseKnowledge triggers re-parsing of a knowledge entry
// This method deletes existing document content and re-parses the knowledge asynchronously.
// It's useful when you want to refresh the knowledge content with updated parsing configurations
// or when the original parsing failed and you want to retry.
//
// Parameters:
//   - ctx: Context for the request
//   - knowledgeID: The ID of the knowledge entry to reparse
//
// Returns:
//   - *Knowledge: The updated knowledge entry with status set to "pending"
//   - error: Error information if the request fails
//
// Example:
//
//	knowledge, err := client.ReparseKnowledge(ctx, "knowledge-id-123")
//	if err != nil {
//	    log.Fatalf("Failed to reparse knowledge: %v", err)
//	}
//	fmt.Printf("Knowledge reparse task submitted, status: %s\n", knowledge.ParseStatus)
func (c *Client) ReparseKnowledge(ctx context.Context, knowledgeID string) (*Knowledge, error) {
	if knowledgeID == "" {
		return nil, fmt.Errorf("knowledge ID cannot be empty")
	}

	path := fmt.Sprintf("/api/v1/knowledge/%s/reparse", knowledgeID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
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
func (c *Client) UpdateImageInfo(ctx context.Context,
	knowledgeID string, chunkID string, request *UpdateImageInfoRequest,
) error {
	path := fmt.Sprintf("/api/v1/knowledge/image/%s/%s", knowledgeID, chunkID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return err
	}

	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &response)
}

// CreateManualKnowledgeRequest contains the parameters for creating a manual Markdown knowledge entry.
type CreateManualKnowledgeRequest struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	TagID   string `json:"tag_id,omitempty"`
	Channel string `json:"channel,omitempty"`
}

// UpdateManualKnowledgeRequest contains the parameters for updating a manual Markdown knowledge entry.
type UpdateManualKnowledgeRequest struct {
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
}

// BatchUpdateKnowledgeTagsRequest contains the mapping of knowledge IDs to tag IDs.
type BatchUpdateKnowledgeTagsRequest struct {
	Updates map[string]*string `json:"updates"` // knowledge_id -> tag_id (nil to clear)
}

// CreateManualKnowledge creates a knowledge entry from manual Markdown content.
func (c *Client) CreateManualKnowledge(ctx context.Context, knowledgeBaseID string, request *CreateManualKnowledgeRequest) (*Knowledge, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge/manual", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// UpdateManualKnowledge updates a manual Markdown knowledge entry.
func (c *Client) UpdateManualKnowledge(ctx context.Context, knowledgeID string, request *UpdateManualKnowledgeRequest) (*Knowledge, error) {
	path := fmt.Sprintf("/api/v1/knowledge/manual/%s", knowledgeID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, request, nil)
	if err != nil {
		return nil, err
	}

	var response KnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return &response.Data, nil
}

// FilterKnowledgeResponse represents the response from filter knowledge API
type FilterKnowledgeResponse struct {
	Success bool        `json:"success"`
	Data    []Knowledge `json:"data"`
	HasMore bool        `json:"has_more"`
}

// FilterKnowledge searches/filters knowledge entries across knowledge bases
func (c *Client) FilterKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string, agentID string) ([]Knowledge, bool, error) {
	queryParams := url.Values{}
	if keyword != "" {
		queryParams.Set("keyword", keyword)
	}
	queryParams.Set("offset", strconv.Itoa(offset))
	queryParams.Set("limit", strconv.Itoa(limit))
	if len(fileTypes) > 0 {
		for _, ft := range fileTypes {
			queryParams.Add("file_types", ft)
		}
	}
	if agentID != "" {
		queryParams.Set("agent_id", agentID)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/knowledge/search", nil, queryParams)
	if err != nil {
		return nil, false, err
	}

	var response FilterKnowledgeResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, false, err
	}

	return response.Data, response.HasMore, nil
}

// MoveKnowledgeRequest contains the parameters for moving knowledge between KBs
type MoveKnowledgeRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids"`
	SourceKBID   string   `json:"source_kb_id"`
	TargetKBID   string   `json:"target_kb_id"`
	Mode         string   `json:"mode"` // "reuse_vectors" or "reparse"
}

// MoveKnowledgeResponse represents the response from move knowledge API
type MoveKnowledgeResponse struct {
	TaskID         string `json:"task_id"`
	SourceKBID     string `json:"source_kb_id"`
	TargetKBID     string `json:"target_kb_id"`
	KnowledgeCount int    `json:"knowledge_count"`
	Message        string `json:"message"`
}

// MoveKnowledge moves knowledge items from one knowledge base to another (async task)
func (c *Client) MoveKnowledge(ctx context.Context, req *MoveKnowledgeRequest) (*MoveKnowledgeResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/knowledge/move", req, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool                   `json:"success"`
		Data    *MoveKnowledgeResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// KnowledgeMoveProgress represents the progress of a knowledge move task
type KnowledgeMoveProgress struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	Total     int    `json:"total"`
	Processed int    `json:"processed"`
	Message   string `json:"message"`
	Error     string `json:"error,omitempty"`
}

// GetKnowledgeMoveProgress gets the progress of a knowledge move task
func (c *Client) GetKnowledgeMoveProgress(ctx context.Context, taskID string) (*KnowledgeMoveProgress, error) {
	path := fmt.Sprintf("/api/v1/knowledge/move/progress/%s", taskID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Success bool                   `json:"success"`
		Data    *KnowledgeMoveProgress `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// PreviewKnowledgeFile returns the file content for inline preview.
// The caller is responsible for reading and closing the response body.
func (c *Client) PreviewKnowledgeFile(ctx context.Context, knowledgeID string) (*http.Response, error) {
	path := fmt.Sprintf("/api/v1/knowledge/%s/preview", knowledgeID)
	return c.doRequest(ctx, http.MethodGet, path, nil, nil)
}

// BatchUpdateKnowledgeTags batch updates knowledge tags.
// The updates map contains knowledge_id -> tag_id mappings. Set tag_id to nil to clear the tag.
func (c *Client) BatchUpdateKnowledgeTags(ctx context.Context, updates map[string]*string) error {
	request := &BatchUpdateKnowledgeTagsRequest{Updates: updates}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/knowledge/tags", request, nil)
	if err != nil {
		return err
	}

	var batchResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message,omitempty"`
	}

	return parseResponse(resp, &batchResponse)
}
