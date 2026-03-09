package client

import (
	"context"
	"net/http"
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

// ListSkills lists all preloaded agent skills
func (c *Client) ListSkills(ctx context.Context) ([]SkillInfo, bool, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/skills", nil, nil)
	if err != nil {
		return nil, false, err
	}

	var response SkillListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, false, err
	}

	return response.Data, response.SkillsAvailable, nil
}
