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
// public locator. Use "@owner/slug" (ClawHub), a github.com / gitlab.com /
// skills.sh / skillhub URL, or a direct zip/SKILL.md URL. Bare "owner/slug"
// is rejected as ambiguous. The call is accepted asynchronously; follow
// progress on the skill ID.
func (c *Client) InstallSandboxSkillFromSource(
	ctx context.Context, configID, source string,
) (string, error) {
	if configID == "" {
		return "", fmt.Errorf("sandbox config ID is required")
	}
	body := map[string]string{"source": source}
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

// ReinstallSandboxSkill runs the install of one skill again from the archive
// the server already stores, so a failure that had nothing to do with the
// bundle can be retried without re-uploading it. Like the install it is
// accepted asynchronously. A skill already serving the current image is left
// alone rather than rebuilt.
func (c *Client) ReinstallSandboxSkill(
	ctx context.Context, configID, skillID string,
) (string, error) {
	if configID == "" {
		return "", fmt.Errorf("sandbox config ID is required")
	}
	if skillID == "" {
		return "", fmt.Errorf("skill ID is required")
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) +
		"/skills/" + url.PathEscape(skillID) + "/reinstall"
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return "", err
	}
	var response SandboxSkillInstallResponse
	if err := parseResponse(resp, &response); err != nil {
		return "", err
	}
	return response.Data.SkillID, nil
}

// StopSandboxSkill aborts an in-flight install so the operator can retry or
// uninstall. After a process restart the row may still say installing with no
// live process; this rewrites it immediately instead of waiting for the
// stuck-run reaper. Removal is not stopped.
func (c *Client) StopSandboxSkill(
	ctx context.Context, configID, skillID string,
) (*SandboxSkill, error) {
	if configID == "" {
		return nil, fmt.Errorf("sandbox config ID is required")
	}
	if skillID == "" {
		return nil, fmt.Errorf("skill ID is required")
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) +
		"/skills/" + url.PathEscape(skillID) + "/stop"
	resp, err := c.doRequest(ctx, http.MethodPost, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var response sandboxSkillResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// SandboxSkillEnv is one environment variable an installed skill declared. It
// reports whether a workspace-wide value exists and never what it is.
type SandboxSkillEnv struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	IsSet       bool   `json:"is_set"`
}

// SandboxSkill is one installed skill of a sandbox config.
type SandboxSkill struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Version             string            `json:"version,omitempty"`
	Description         string            `json:"description,omitempty"`
	Enabled             bool              `json:"enabled"`
	Status              string            `json:"status"`
	Error               string            `json:"error,omitempty"`
	BundleSHA256        string            `json:"bundle_sha256,omitempty"`
	InstalledSnapshotID string            `json:"installed_snapshot_id,omitempty"`
	InstallSessionID    string            `json:"install_session_id,omitempty"`
	InstallMessageID    string            `json:"install_message_id,omitempty"`
	Envs                []SandboxSkillEnv `json:"envs,omitempty"`
}

type sandboxSkillResponse struct {
	Success bool         `json:"success"`
	Data    SandboxSkill `json:"data"`
}

// SandboxSkillUpdate is what one PATCH may change. Both fields are optional,
// but a request carrying neither is refused: a nil Enabled is not a request to
// hide the skill, and a nil Envs must leave every stored value alone.
type SandboxSkillUpdate struct {
	Enabled *bool              `json:"enabled,omitempty"`
	Envs    *map[string]string `json:"envs,omitempty"`
}

// UpdateSandboxSkill shows or hides an installed skill and sets the
// workspace-wide values of the environment variables it declared. Visibility is
// metadata only: the files stay in the image either way.
//
// Only declared names are written; a name outside the declaration is ignored
// rather than refused, so a stale form cannot fail an otherwise valid save. An
// empty string clears a value and keeps the declaration, because "nobody filled
// this in" and "this is not needed" are different states.
//
// A stored value is never read back: the returned Envs report IsSet only.
func (c *Client) UpdateSandboxSkill(
	ctx context.Context, configID, skillID string, update SandboxSkillUpdate,
) (*SandboxSkill, error) {
	if configID == "" {
		return nil, fmt.Errorf("sandbox config ID is required")
	}
	if skillID == "" {
		return nil, fmt.Errorf("skill ID is required")
	}
	if update.Enabled == nil && update.Envs == nil {
		return nil, fmt.Errorf("enabled or envs is required")
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) +
		"/skills/" + url.PathEscape(skillID)
	resp, err := c.doRequest(ctx, http.MethodPatch, path, update, nil)
	if err != nil {
		return nil, err
	}
	var response sandboxSkillResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}

// SetSandboxSkillEnabled is the visibility half of UpdateSandboxSkill.
func (c *Client) SetSandboxSkillEnabled(
	ctx context.Context, configID, skillID string, enabled bool,
) (*SandboxSkill, error) {
	return c.UpdateSandboxSkill(ctx, configID, skillID,
		SandboxSkillUpdate{Enabled: &enabled})
}

// SetSandboxSkillEnvValues is the credentials half of UpdateSandboxSkill: it
// stores values that apply to everybody in the workspace. For a value that
// applies to the calling identity alone, use SetMySkillEnvVar.
func (c *Client) SetSandboxSkillEnvValues(
	ctx context.Context, configID, skillID string, values map[string]string,
) (*SandboxSkill, error) {
	return c.UpdateSandboxSkill(ctx, configID, skillID,
		SandboxSkillUpdate{Envs: &values})
}

// SandboxSkillFile is one path in an installed skill's stored archive.
type SandboxSkillFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// SandboxSkillFileContent is one file opened from an installed skill.
type SandboxSkillFileContent struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Encoding  string `json:"encoding"`
	Content   string `json:"content,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

type sandboxSkillFilesResponse struct {
	Success bool               `json:"success"`
	Data    []SandboxSkillFile `json:"data"`
}

type sandboxSkillFileContentResponse struct {
	Success bool                    `json:"success"`
	Data    SandboxSkillFileContent `json:"data"`
}

// ListSandboxSkillFiles lists the stored archive of one installed skill.
func (c *Client) ListSandboxSkillFiles(
	ctx context.Context, configID, skillID string,
) ([]SandboxSkillFile, error) {
	if configID == "" {
		return nil, fmt.Errorf("sandbox config ID is required")
	}
	if skillID == "" {
		return nil, fmt.Errorf("skill ID is required")
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) +
		"/skills/" + url.PathEscape(skillID) + "/files"
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var response sandboxSkillFilesResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}

// GetSandboxSkillFile reads one skill-root-relative file from an installed skill.
func (c *Client) GetSandboxSkillFile(
	ctx context.Context, configID, skillID, filePath string,
) (*SandboxSkillFileContent, error) {
	if configID == "" {
		return nil, fmt.Errorf("sandbox config ID is required")
	}
	if skillID == "" {
		return nil, fmt.Errorf("skill ID is required")
	}
	if filePath == "" {
		return nil, fmt.Errorf("skill file path is required")
	}
	path := "/api/v1/sandbox-configs/" + url.PathEscape(configID) +
		"/skills/" + url.PathEscape(skillID) + "/files/content"
	query := url.Values{}
	query.Set("path", filePath)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, query)
	if err != nil {
		return nil, err
	}
	var response sandboxSkillFileContentResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	return &response.Data, nil
}
