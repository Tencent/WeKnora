package client

import (
	"context"
	"net/http"
	"net/url"
)

// SkillInfo represents skill metadata
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SkillListResponse represents the response from listing skills
type SkillListResponse struct {
	Success         bool        `json:"success"`
	Data            []SkillInfo `json:"data"`
	SkillsAvailable bool       `json:"skills_available"`
}

// ListSkills lists the installed skills a chat turn can invoke on one sandbox
// config. An empty sandboxConfigID returns an empty list.
func (c *Client) ListSkills(ctx context.Context, sandboxConfigID string) ([]SkillInfo, bool, error) {
	query := url.Values{}
	if sandboxConfigID != "" {
		query.Set("sandbox_config_id", sandboxConfigID)
	}
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/skills", nil, query)
	if err != nil {
		return nil, false, err
	}

	var response SkillListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, false, err
	}

	return response.Data, response.SkillsAvailable, nil
}
