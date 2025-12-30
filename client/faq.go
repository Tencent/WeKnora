package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// FAQEntry represents a FAQ item stored under a knowledge base.
type FAQEntry struct {
	ID                string    `json:"id"`
	ChunkID           string    `json:"chunk_id"`
	KnowledgeID       string    `json:"knowledge_id"`
	KnowledgeBaseID   string    `json:"knowledge_base_id"`
	TagID             string    `json:"tag_id"`
	TagName           string    `json:"tag_name"`
	IsEnabled         bool      `json:"is_enabled"`
	IsRecommended     bool      `json:"is_recommended"`
	StandardQuestion  string    `json:"standard_question"`
	SimilarQuestions  []string  `json:"similar_questions"`
	NegativeQuestions []string  `json:"negative_questions"`
	Answers           []string  `json:"answers"`
	AnswerStrategy    string    `json:"answer_strategy"`
	IndexMode         string    `json:"index_mode"`
	UpdatedAt         time.Time `json:"updated_at"`
	CreatedAt         time.Time `json:"created_at"`
	Score             float64   `json:"score,omitempty"`
	MatchType         string    `json:"match_type,omitempty"`
	ChunkType         string    `json:"chunk_type"`
}

// FAQEntryPayload is used to create or update a FAQ entry.
type FAQEntryPayload struct {
	StandardQuestion  string   `json:"standard_question"`
	SimilarQuestions  []string `json:"similar_questions,omitempty"`
	NegativeQuestions []string `json:"negative_questions,omitempty"`
	Answers           []string `json:"answers"`
	AnswerStrategy    *string  `json:"answer_strategy,omitempty"`
	TagID             string   `json:"tag_id,omitempty"`
	TagName           string   `json:"tag_name,omitempty"`
	IsEnabled         *bool    `json:"is_enabled,omitempty"`
	IsRecommended     *bool    `json:"is_recommended,omitempty"`
}

// FAQBatchUpsertPayload represents the request body for batch import (append/replace).
type FAQBatchUpsertPayload struct {
	Entries     []FAQEntryPayload `json:"entries"`
	Mode        string            `json:"mode"`
	KnowledgeID string            `json:"knowledge_id,omitempty"`
}

// FAQEntryFieldsUpdate represents the fields that can be updated for a single FAQ entry.
type FAQEntryFieldsUpdate struct {
	IsEnabled     *bool   `json:"is_enabled,omitempty"`
	IsRecommended *bool   `json:"is_recommended,omitempty"`
	TagID         *string `json:"tag_id,omitempty"`
}

// FAQEntryFieldsBatchRequest updates multiple fields for FAQ entries in bulk.
// Supports two modes:
// 1. By entry ID: use ByID field
// 2. By Tag: use ByTag field to apply the same update to all entries under a tag
type FAQEntryFieldsBatchRequest struct {
	// ByID updates by entry ID, key is entry ID
	ByID map[string]FAQEntryFieldsUpdate `json:"by_id,omitempty"`
	// ByTag updates all entries under a tag, key is tag ID (empty string for uncategorized)
	ByTag map[string]FAQEntryFieldsUpdate `json:"by_tag,omitempty"`
	// ExcludeIDs IDs to exclude from the ByTag update
	ExcludeIDs []string `json:"exclude_ids,omitempty"`
}

// FAQEntryTagBatchRequest updates tags in bulk.
type FAQEntryTagBatchRequest struct {
	Updates map[string]*string `json:"updates"`
}

// FAQDeleteRequest deletes entries in bulk.
type FAQDeleteRequest struct {
	IDs []string `json:"ids"`
}

// FAQSearchRequest represents the hybrid FAQ search request.
type FAQSearchRequest struct {
	QueryText            string   `json:"query_text"`
	VectorThreshold      float64  `json:"vector_threshold"`
	MatchCount           int      `json:"match_count"`
	FirstPriorityTagIDs  []string `json:"first_priority_tag_ids"`  // First priority tag IDs, highest priority
	SecondPriorityTagIDs []string `json:"second_priority_tag_ids"` // Second priority tag IDs, lower than first
}

// FAQEntriesPage contains paginated FAQ results.
type FAQEntriesPage struct {
	Total    int64      `json:"total"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	Entries  []FAQEntry `json:"data"`
}

// FAQEntriesResponse wraps the paginated FAQ response.
type FAQEntriesResponse struct {
	Success bool            `json:"success"`
	Data    *FAQEntriesPage `json:"data"`
	Message string          `json:"message,omitempty"`
	Code    string          `json:"code,omitempty"`
}

// FAQUpsertResponse wraps the asynchronous import response.
type FAQUpsertResponse struct {
	Success bool            `json:"success"`
	Data    *FAQTaskPayload `json:"data"`
	Message string          `json:"message,omitempty"`
	Code    string          `json:"code,omitempty"`
}

// FAQTaskPayload carries the task identifier for async imports.
type FAQTaskPayload struct {
	TaskID string `json:"task_id"`
}

// FAQSearchResponse wraps the hybrid FAQ search results.
type FAQSearchResponse struct {
	Success bool       `json:"success"`
	Data    []FAQEntry `json:"data"`
	Message string     `json:"message,omitempty"`
	Code    string     `json:"code,omitempty"`
}

// FAQEntryResponse wraps the single FAQ entry creation response.
type FAQEntryResponse struct {
	Success bool      `json:"success"`
	Data    *FAQEntry `json:"data"`
	Message string    `json:"message,omitempty"`
	Code    string    `json:"code,omitempty"`
}

type faqSimpleResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// ListFAQEntries returns paginated FAQ entries under a knowledge base.
// searchField: specifies which field to search in ("standard_question", "similar_questions", "answers", "" for all)
// sortOrder: "asc" for time ascending (updated_at ASC), default is time descending (updated_at DESC)
func (c *Client) ListFAQEntries(ctx context.Context,
	knowledgeBaseID string, page, pageSize int, tagID string, keyword string, searchField string, sortOrder string,
) (*FAQEntriesPage, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries", knowledgeBaseID)
	query := url.Values{}
	if page > 0 {
		query.Add("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Add("page_size", strconv.Itoa(pageSize))
	}
	if tagID != "" {
		query.Add("tag_id", tagID)
	}
	if keyword != "" {
		query.Add("keyword", keyword)
	}
	if searchField != "" {
		query.Add("search_field", searchField)
	}
	if sortOrder != "" {
		query.Add("sort_order", sortOrder)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, query)
	if err != nil {
		return nil, err
	}

	var response FAQEntriesResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return &FAQEntriesPage{}, nil
	}
	return response.Data, nil
}

// UpsertFAQEntries imports or appends FAQ entries asynchronously and returns the task ID.
func (c *Client) UpsertFAQEntries(ctx context.Context,
	knowledgeBaseID string, payload *FAQBatchUpsertPayload,
) (string, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return "", err
	}

	var response FAQUpsertResponse
	if err := parseResponse(resp, &response); err != nil {
		return "", err
	}
	if response.Data == nil {
		return "", fmt.Errorf("missing task information in response")
	}
	return response.Data.TaskID, nil
}

// CreateFAQEntry creates a single FAQ entry synchronously.
func (c *Client) CreateFAQEntry(ctx context.Context,
	knowledgeBaseID string, payload *FAQEntryPayload,
) (*FAQEntry, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entry", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return nil, err
	}

	var response FAQEntryResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// GetFAQEntry retrieves a single FAQ entry by ID.
func (c *Client) GetFAQEntry(ctx context.Context,
	knowledgeBaseID, entryID string,
) (*FAQEntry, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries/%s", knowledgeBaseID, entryID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response FAQEntryResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// UpdateFAQEntry updates a single FAQ entry.
func (c *Client) UpdateFAQEntry(ctx context.Context,
	knowledgeBaseID, entryID string, payload *FAQEntryPayload,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries/%s", knowledgeBaseID, entryID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, payload, nil)
	if err != nil {
		return err
	}

	var response faqSimpleResponse
	return parseResponse(resp, &response)
}

// UpdateFAQEntryFieldsBatch updates multiple fields for FAQ entries in bulk.
// Supports updating is_enabled, is_recommended, tag_id in a single call.
// Supports two modes:
//   - byID: update by entry ID, key is entry ID
//   - byTag: update all entries under a tag, key is tag ID (empty string for uncategorized)
func (c *Client) UpdateFAQEntryFieldsBatch(ctx context.Context,
	knowledgeBaseID string, byID map[string]FAQEntryFieldsUpdate, byTag map[string]FAQEntryFieldsUpdate, excludeIDs []string,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries/fields", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, &FAQEntryFieldsBatchRequest{ByID: byID, ByTag: byTag, ExcludeIDs: excludeIDs}, nil)
	if err != nil {
		return err
	}

	var response faqSimpleResponse
	return parseResponse(resp, &response)
}

// UpdateFAQEntryTagBatch updates FAQ entry tags in bulk.
func (c *Client) UpdateFAQEntryTagBatch(ctx context.Context,
	knowledgeBaseID string, updates map[string]*string,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries/tags", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, &FAQEntryTagBatchRequest{Updates: updates}, nil)
	if err != nil {
		return err
	}

	var response faqSimpleResponse
	return parseResponse(resp, &response)
}

// DeleteFAQEntries deletes FAQ entries in bulk.
func (c *Client) DeleteFAQEntries(ctx context.Context,
	knowledgeBaseID string, ids []string,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, &FAQDeleteRequest{IDs: ids}, nil)
	if err != nil {
		return err
	}

	var response faqSimpleResponse
	return parseResponse(resp, &response)
}

// SearchFAQEntries performs hybrid FAQ search inside a knowledge base.
func (c *Client) SearchFAQEntries(ctx context.Context,
	knowledgeBaseID string, payload *FAQSearchRequest,
) ([]FAQEntry, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/search", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return nil, err
	}

	var response FAQSearchResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// ExportFAQEntries exports all FAQ entries from a knowledge base as CSV data.
// The CSV format matches the import example format with 8 columns:
// 分类(必填), 问题(必填), 相似问题(选填-多个用##分隔), 反例问题(选填-多个用##分隔),
// 机器人回答(必填-多个用##分隔), 是否全部回复(选填-默认FALSE), 是否停用(选填-默认FALSE),
// 是否禁止被推荐(选填-默认False 可被推荐)
func (c *Client) ExportFAQEntries(ctx context.Context, knowledgeBaseID string) ([]byte, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/faq/entries/export", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Read the raw CSV data from response body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read export response: %w", err)
	}

	return data, nil
}

// FAQImportProgress represents the progress of an async FAQ import task.
type FAQImportProgress struct {
	TaskID      string `json:"task_id"`
	KBID        string `json:"kb_id"`
	KnowledgeID string `json:"knowledge_id"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Total       int    `json:"total"`
	Processed   int    `json:"processed"`
	Message     string `json:"message"`
	Error       string `json:"error,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// FAQImportProgressResponse wraps the FAQ import progress response.
type FAQImportProgressResponse struct {
	Success bool               `json:"success"`
	Data    *FAQImportProgress `json:"data"`
	Message string             `json:"message,omitempty"`
	Code    string             `json:"code,omitempty"`
}

// GetFAQImportProgress retrieves the progress of an async FAQ import task.
func (c *Client) GetFAQImportProgress(ctx context.Context, taskID string) (*FAQImportProgress, error) {
	path := fmt.Sprintf("/api/v1/faq/import/progress/%s", taskID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var response FAQImportProgressResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}
