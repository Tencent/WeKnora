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
func (c *Client) CreateKnowledgeFromFile(ctx context.Context,
	knowledgeBaseID string, filePath string, metadata map[string]string, enableMultimodel *bool, customFileName string,
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

	// Close the multipart writer
	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close writer: %w", err)
	}

	// Set request headers
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.token != "" {
		req.Header.Set("X-API-Key", c.token)
	}
	if requestID := ctx.Value("RequestID"); requestID != nil {
		req.Header.Set("X-Request-ID", requestID.(string))
	}

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

// CreateKnowledgeFromURL creates a knowledge entry from a web URL
func (c *Client) CreateKnowledgeFromURL(
	ctx context.Context,
	knowledgeBaseID string,
	url string,
	enableMultimodel *bool,
	title string,
) (*Knowledge, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge/url", knowledgeBaseID)

	reqBody := struct {
		URL              string `json:"url"`
		EnableMultimodel *bool  `json:"enable_multimodel"`
		Title            string `json:"title"`
	}{
		URL:              url,
		EnableMultimodel: enableMultimodel,
		Title:            title,
	}

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

// ListKnowledge lists knowledge entries in a knowledge base with pagination
func (c *Client) ListKnowledge(ctx context.Context,
	knowledgeBaseID string,
	page int,
	pageSize int,
	tagID string,
) ([]Knowledge, int64, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/knowledge", knowledgeBaseID)

	queryParams := url.Values{}
	queryParams.Add("page", strconv.Itoa(page))
	queryParams.Add("page_size", strconv.Itoa(pageSize))
	if tagID != "" {
		queryParams.Add("tag_id", tagID)
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
