package client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
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
	SkillsAvailable bool        `json:"skills_available"`
}

// SandboxSkillInstallResponse is returned when a skill install is accepted.
type SandboxSkillInstallResponse struct {
	Success bool `json:"success"`
	Data    struct {
		SkillID string `json:"skill_id"`
	} `json:"data"`
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

// InstallSandboxSkillFromSource installs a skill onto a sandbox config from a
// ClawHub / SkillHub / skills.sh / GitHub / GitLab locator or a direct archive
// URL. The call is accepted asynchronously; follow progress on the skill ID.
func (c *Client) InstallSandboxSkillFromSource(
	ctx context.Context, configID, source, token string,
) (string, error) {
	if configID == "" {
		return "", fmt.Errorf("sandbox config ID is required")
	}
	body := map[string]string{"source": source}
	if token != "" {
		body["token"] = token
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) + "/skills"
	resp, err := c.doRequest(ctx, http.MethodPost, path, body, nil)
	if err != nil {
		return "", err
	}
	var response SandboxSkillInstallResponse
	if err := parseResponse(resp, &response); err != nil {
		return "", err
	}
	return response.Data.SkillID, nil
}

// UploadSandboxSkill installs a skill onto a sandbox config from a zip archive.
func (c *Client) UploadSandboxSkill(
	ctx context.Context, configID, filename string, archive []byte,
) (string, error) {
	if configID == "" {
		return "", fmt.Errorf("sandbox config ID is required")
	}
	if filename == "" {
		filename = "skill.zip"
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("create skill upload part: %w", err)
	}
	if _, err := part.Write(archive); err != nil {
		return "", fmt.Errorf("write skill upload part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("close skill upload form: %w", err)
	}

	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) + "/skills"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthHeaders(ctx, req)
	req.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	req.ContentLength = int64(body.Len())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	var response SandboxSkillInstallResponse
	if err := parseResponse(resp, &response); err != nil {
		return "", err
	}
	return response.Data.SkillID, nil
}
