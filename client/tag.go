package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Tag represents a knowledge base tag.
type Tag struct {
	ID              string    `json:"id"`
	SeqID           int64     `json:"seq_id"`
	TenantID        uint64    `json:"tenant_id"`
	KnowledgeBaseID string    `json:"knowledge_base_id"`
	Name            string    `json:"name"`
	Color           string    `json:"color"`
	SortOrder       int       `json:"sort_order"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// TagWithStats represents tag information along with usage statistics.
type TagWithStats struct {
	Tag
	KnowledgeCount int64 `json:"knowledge_count"`
	ChunkCount     int64 `json:"chunk_count"`
}

// CreateTagPayload is used to create a new tag.
type CreateTagPayload struct {
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	SortOrder int    `json:"sort_order,omitempty"`
}

// UpdateTagPayload is used to update an existing tag.
type UpdateTagPayload struct {
	Name      *string `json:"name,omitempty"`
	Color     *string `json:"color,omitempty"`
	SortOrder *int    `json:"sort_order,omitempty"`
}

// TagsPage contains paginated tag results.
type TagsPage struct {
	Total    int64          `json:"total"`
	Page     int            `json:"page"`
	PageSize int            `json:"page_size"`
	Tags     []TagWithStats `json:"data"`
}

// TagsResponse wraps the paginated tags response.
type TagsResponse struct {
	Success bool      `json:"success"`
	Data    *TagsPage `json:"data"`
	Message string    `json:"message,omitempty"`
	Code    string    `json:"code,omitempty"`
}

// TagResponse wraps a single tag response.
type TagResponse struct {
	Success bool   `json:"success"`
	Data    *Tag   `json:"data"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

type tagSimpleResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// ListTags returns paginated tags under a knowledge base.
func (c *Client) ListTags(ctx context.Context,
	knowledgeBaseID string, page, pageSize int, keyword string,
) (*TagsPage, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/tags", knowledgeBaseID)
	query := url.Values{}
	if page > 0 {
		query.Add("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Add("page_size", strconv.Itoa(pageSize))
	}
	if keyword != "" {
		query.Add("keyword", keyword)
	}

	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, query)
	if err != nil {
		return nil, err
	}

	var response TagsResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return &TagsPage{}, nil
	}
	return response.Data, nil
}

// CreateTag creates a new tag under a knowledge base.
func (c *Client) CreateTag(ctx context.Context,
	knowledgeBaseID string, payload *CreateTagPayload,
) (*Tag, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/tags", knowledgeBaseID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, payload, nil)
	if err != nil {
		return nil, err
	}

	var response TagResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// UpdateTag updates an existing tag.
// tagID can be either UUID or seq_id (as string).
func (c *Client) UpdateTag(ctx context.Context,
	knowledgeBaseID, tagID string, payload *UpdateTagPayload,
) (*Tag, error) {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/tags/%s", knowledgeBaseID, tagID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, payload, nil)
	if err != nil {
		return nil, err
	}

	var response TagResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// UpdateTagBySeqID updates an existing tag by seq_id.
func (c *Client) UpdateTagBySeqID(ctx context.Context,
	knowledgeBaseID string, tagSeqID int64, payload *UpdateTagPayload,
) (*Tag, error) {
	return c.UpdateTag(ctx, knowledgeBaseID, strconv.FormatInt(tagSeqID, 10), payload)
}

// DeleteTag deletes a tag.
// tagID can be either UUID or seq_id (as string).
// Set force to true to delete even if the tag is referenced.
// Set contentOnly to true to only delete the content under the tag but keep the tag itself.
// excludeIDs: seq_ids of chunks to exclude from deletion.
func (c *Client) DeleteTag(ctx context.Context,
	knowledgeBaseID, tagID string, force bool, contentOnly bool, excludeIDs []int64,
) error {
	path := fmt.Sprintf("/api/v1/knowledge-bases/%s/tags/%s", knowledgeBaseID, tagID)
	query := url.Values{}
	if force {
		query.Add("force", "true")
	}
	if contentOnly {
		query.Add("content_only", "true")
	}

	var body interface{}
	if len(excludeIDs) > 0 {
		body = map[string]interface{}{
			"exclude_ids": excludeIDs,
		}
	}

	resp, err := c.doRequest(ctx, http.MethodDelete, path, body, query)
	if err != nil {
		return err
	}

	var response tagSimpleResponse
	return parseResponse(resp, &response)
}

// DeleteTagBySeqID deletes a tag by seq_id.
func (c *Client) DeleteTagBySeqID(ctx context.Context,
	knowledgeBaseID string, tagSeqID int64, force bool, contentOnly bool, excludeIDs []int64,
) error {
	return c.DeleteTag(ctx, knowledgeBaseID, strconv.FormatInt(tagSeqID, 10), force, contentOnly, excludeIDs)
}
