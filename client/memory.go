package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Memory settings, items, topics and document affinity all operate on the
// caller's own memory space. There is no subject id on the wire: the server
// derives identity from the credentials, so a scoped API key cannot inherit
// another person's memories. Full-access API keys or a Bearer session are
// required.

// MemorySettings is the merged workspace + personal memory switch.
type MemorySettings struct {
	WorkspaceEnabled bool   `json:"workspace_enabled"`
	UserEnabled      bool   `json:"user_enabled"`
	Effective        bool   `json:"effective"`
	WriteMode        string `json:"write_mode"`
	ItemCount        int    `json:"item_count"`
	MaxItems         int    `json:"max_items"`
}

// MemoryItem is one long-term memory row as returned by the API.
type MemoryItem struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Content         string     `json:"content"`
	Topic           string     `json:"topic,omitempty"`
	Importance      int        `json:"importance"`
	Origin          string     `json:"origin"`
	Status          string     `json:"status"`
	SourceSessionID string     `json:"source_session_id,omitempty"`
	SourceMessageID string     `json:"source_message_id,omitempty"`
	ValidFrom       time.Time  `json:"valid_from"`
	InvalidAt       *time.Time `json:"invalid_at,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	SupersededBy    string     `json:"superseded_by,omitempty"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	UseCount        int        `json:"use_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// MemoryTopic is a subject the extractor is still counting before promoting
// it to a long-term interest.
type MemoryTopic struct {
	ID         string    `json:"id"`
	Topic      string    `json:"topic"`
	Aliases    []string  `json:"aliases"`
	Hits       int       `json:"hits"`
	Threshold  int       `json:"threshold"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// MemoryDocument is a knowledge entry this person keeps drawing answers from.
type MemoryDocument struct {
	ID              string    `json:"id"`
	KnowledgeID     string    `json:"knowledge_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Title           string    `json:"title"`
	Hits            int       `json:"hits"`
	LastUsedAt      time.Time `json:"last_used_at"`
}

// MemoryConsolidationResult is what POST /memory/consolidate returns.
type MemoryConsolidationResult struct {
	Merged     int    `json:"merged"`
	Demoted    int    `json:"demoted"`
	Expired    int    `json:"expired"`
	Reviewed   int    `json:"reviewed"`
	Candidates int    `json:"candidates"`
	Skipped    string `json:"skipped,omitempty"`
}

// MemoryExport is the JSON snapshot from GET /memory/export.
type MemoryExport struct {
	Total     int64         `json:"total"`
	Truncated bool          `json:"truncated"`
	Items     []*MemoryItem `json:"data"`
}

type memorySettingsResponse struct {
	Success bool            `json:"success"`
	Data    *MemorySettings `json:"data"`
}

type memoryItemResponse struct {
	Success bool        `json:"success"`
	Data    *MemoryItem `json:"data"`
}

type memoryListResponse struct {
	Success bool          `json:"success"`
	Data    []*MemoryItem `json:"data"`
	Total   int64         `json:"total"`
}

type memoryTopicListResponse struct {
	Success bool           `json:"success"`
	Data    []*MemoryTopic `json:"data"`
	Total   int64          `json:"total"`
}

type memoryDocumentListResponse struct {
	Success bool              `json:"success"`
	Data    []*MemoryDocument `json:"data"`
	Total   int64             `json:"total"`
}

type memoryClearResponse struct {
	Success bool  `json:"success"`
	Removed int64 `json:"removed"`
}

type memoryConsolidateResponse struct {
	Success bool                       `json:"success"`
	Data    *MemoryConsolidationResult `json:"data"`
}

func memoryListQuery(status string, limit, offset int) url.Values {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	return q
}

// GetMemorySettings returns the merged workspace + personal memory switch.
func (c *Client) GetMemorySettings(ctx context.Context) (*MemorySettings, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/settings", nil, nil)
	if err != nil {
		return nil, err
	}
	var out memorySettingsResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// UpdateMemorySettings turns the caller's own long-term memory on or off.
func (c *Client) UpdateMemorySettings(ctx context.Context, enabled bool) (*MemorySettings, error) {
	body := map[string]bool{"enabled": enabled}
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/memory/settings", body, nil)
	if err != nil {
		return nil, err
	}
	var out memorySettingsResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ListMemoryItems pages through the caller's memories. status may be empty
// (all) or one of active / superseded / archived / pending.
func (c *Client) ListMemoryItems(ctx context.Context, status string, limit, offset int) ([]*MemoryItem, int64, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/items", nil, memoryListQuery(status, limit, offset))
	if err != nil {
		return nil, 0, err
	}
	var out memoryListResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Total, nil
}

// CreateMemoryItem manually adds a long-term memory. kind is one of
// profile / preference / fact / task / interest.
func (c *Client) CreateMemoryItem(ctx context.Context, kind, content string, importance int) (*MemoryItem, error) {
	body := map[string]interface{}{
		"kind":       kind,
		"content":    content,
		"importance": importance,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/memory/items", body, nil)
	if err != nil {
		return nil, err
	}
	var out memoryItemResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// UpdateMemoryItem changes content and importance. After an edit the extractor
// will not overwrite the row.
func (c *Client) UpdateMemoryItem(ctx context.Context, id, content string, importance int) (*MemoryItem, error) {
	if id == "" {
		return nil, fmt.Errorf("memory id is required")
	}
	body := map[string]interface{}{
		"content":    content,
		"importance": importance,
	}
	path := "/api/v1/memory/items/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodPut, path, body, nil)
	if err != nil {
		return nil, err
	}
	var out memoryItemResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// DeleteMemoryItem permanently removes one memory.
func (c *Client) DeleteMemoryItem(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("memory id is required")
	}
	path := "/api/v1/memory/items/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ConfirmMemoryItem accepts an inferred (pending) memory so it starts taking
// effect.
func (c *Client) ConfirmMemoryItem(ctx context.Context, id string) (*MemoryItem, error) {
	if id == "" {
		return nil, fmt.Errorf("memory id is required")
	}
	path := "/api/v1/memory/items/" + url.PathEscape(id) + "/confirm"
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryItemResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// RejectMemoryItem declines an inferred memory and records the rejection so
// the extractor does not silently re-add it.
func (c *Client) RejectMemoryItem(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("memory id is required")
	}
	path := "/api/v1/memory/items/" + url.PathEscape(id) + "/reject"
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ClearMemoryItems permanently deletes every memory belonging to the caller.
func (c *Client) ClearMemoryItems(ctx context.Context) (int64, error) {
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/memory/items", nil, nil)
	if err != nil {
		return 0, err
	}
	var out memoryClearResponse
	if err := parseResponse(resp, &out); err != nil {
		return 0, err
	}
	return out.Removed, nil
}

// ListMemoryTopics pages through subjects the extractor is still counting.
func (c *Client) ListMemoryTopics(ctx context.Context, limit, offset int) ([]*MemoryTopic, int64, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/topics", nil, memoryListQuery("", limit, offset))
	if err != nil {
		return nil, 0, err
	}
	var out memoryTopicListResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Total, nil
}

// PromoteMemoryTopic turns a counted topic into a long-term interest without
// waiting for the remaining hits.
func (c *Client) PromoteMemoryTopic(ctx context.Context, id string) (*MemoryItem, error) {
	if id == "" {
		return nil, fmt.Errorf("topic id is required")
	}
	path := "/api/v1/memory/topics/" + url.PathEscape(id) + "/promote"
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryItemResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// DeleteMemoryTopic stops tracking a topic that has not been promoted yet.
func (c *Client) DeleteMemoryTopic(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("topic id is required")
	}
	path := "/api/v1/memory/topics/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ListMemoryDocuments pages through documents this person keeps citing.
func (c *Client) ListMemoryDocuments(ctx context.Context, limit, offset int) ([]*MemoryDocument, int64, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/documents", nil, memoryListQuery("", limit, offset))
	if err != nil {
		return nil, 0, err
	}
	var out memoryDocumentListResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Total, nil
}

// DeleteMemoryDocument stops using one document for personalized retrieval.
func (c *Client) DeleteMemoryDocument(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("document affinity id is required")
	}
	path := "/api/v1/memory/documents/" + url.PathEscape(id)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ExportMemory downloads a JSON snapshot of every memory belonging to the
// caller. Truncated is true only if the safety ceiling clipped the file.
func (c *Client) ExportMemory(ctx context.Context) (*MemoryExport, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/memory/export", nil, nil)
	if err != nil {
		return nil, err
	}
	var out MemoryExport
	out.Items = nil
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ConsolidateMemory merges near-duplicate items and archives expired ones
// without waiting for the daily background pass.
func (c *Client) ConsolidateMemory(ctx context.Context) (*MemoryConsolidationResult, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/memory/consolidate", nil, nil)
	if err != nil {
		return nil, err
	}
	var out memoryConsolidateResponse
	if err := parseResponse(resp, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}
