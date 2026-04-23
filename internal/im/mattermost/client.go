package mattermost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client calls Mattermost REST API v4.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// NewClient builds an API client. siteURL is the Mattermost server root (e.g. https://mm.example.com).
func NewClient(siteURL, botToken string) (*Client, error) {
	siteURL = strings.TrimSpace(siteURL)
	siteURL = strings.TrimRight(siteURL, "/")
	if siteURL == "" {
		return nil, fmt.Errorf("site_url is required")
	}
	if strings.TrimSpace(botToken) == "" {
		return nil, fmt.Errorf("bot_token is required")
	}
	return &Client{
		baseURL: siteURL + "/api/v4",
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
		token: strings.TrimSpace(botToken),
	}, nil
}

func (c *Client) authHeader(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
}

// CreatePost creates a channel post. rootID is the thread root post id (empty for new top-level post).
func (c *Client) CreatePost(ctx context.Context, channelID, rootID, message string) (postID string, err error) {
	body := map[string]string{
		"channel_id": channelID,
		"message":    message,
	}
	if rootID != "" {
		body["root_id"] = rootID
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/posts", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	c.authHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read mattermost create post response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == http.StatusForbidden {
			return "", fmt.Errorf("mattermost create post: 403 forbidden — add the bot user to this Mattermost channel (Channel menu → Members → Add); body=%s", truncateForErr(respBody))
		}
		return "", fmt.Errorf("mattermost create post: status=%d body=%s", resp.StatusCode, truncateForErr(respBody))
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return "", fmt.Errorf("decode create post: %w", err)
	}
	if created.ID == "" {
		return "", fmt.Errorf("mattermost create post: empty id")
	}
	return created.ID, nil
}

// GetPost fetches a post by ID, returning its root_id (empty if top-level).
func (c *Client) GetPost(ctx context.Context, postID string) (rootID string, err error) {
	url := fmt.Sprintf("%s/posts/%s", c.baseURL, postID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	c.authHeader(req)
	req.Header.Del("Content-Type")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read mattermost get post response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("mattermost get post: status=%d body=%s", resp.StatusCode, truncateForErr(respBody))
	}

	var post struct {
		ID     string `json:"id"`
		RootID string `json:"root_id"`
	}
	if err := json.Unmarshal(respBody, &post); err != nil {
		return "", fmt.Errorf("decode get post: %w", err)
	}
	return post.RootID, nil
}

// PatchPostMessage updates a post's message field.
func (c *Client) PatchPostMessage(ctx context.Context, postID, message string) error {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/posts/%s/patch", c.baseURL, postID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	c.authHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read mattermost patch post response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mattermost patch post: status=%d body=%s", resp.StatusCode, truncateForErr(respBody))
	}
	return nil
}

// FileInfo holds metadata from GET /files/{id}/info.
type FileInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// GetFileInfo fetches file metadata.
func (c *Client) GetFileInfo(ctx context.Context, fileID string) (*FileInfo, error) {
	url := fmt.Sprintf("%s/files/%s/info", c.baseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.authHeader(req)
	req.Header.Del("Content-Type")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read mattermost file info response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mattermost file info: status=%d body=%s", resp.StatusCode, truncateForErr(respBody))
	}

	var info FileInfo
	if err := json.Unmarshal(respBody, &info); err != nil {
		return nil, fmt.Errorf("decode file info: %w", err)
	}
	return &info, nil
}

// GetFileReader opens a download stream for file content.
func (c *Client) GetFileReader(ctx context.Context, fileID string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/files/%s", c.baseURL, fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.authHeader(req)
	req.Header.Del("Content-Type")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read mattermost get file error response (status=%d): %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("mattermost get file: status=%d body=%s", resp.StatusCode, truncateForErr(body))
	}
	return resp.Body, nil
}

func truncateForErr(b []byte) string {
	const max = 512
	s := string(b)
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
