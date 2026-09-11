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

// SandboxLiveFileEntry is one relative entry under a session sandbox's
// /workspace/output root. Type is "file", "directory", or "other".
type SandboxLiveFileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	ModTime string `json:"mod_time"`
}

type sandboxLiveFileListResponse struct {
	Success bool                   `json:"success"`
	Data    []SandboxLiveFileEntry `json:"data"`
}

func sandboxLiveFilesPath(sessionID string) string {
	return "/api/v1/sessions/" + url.PathEscape(sessionID) + "/sandbox/files"
}

// ListSandboxLiveFiles lists one directory level below /workspace/output.
// An empty relativeDir lists the output root. This call does not create or
// resume a paused sandbox.
func (c *Client) ListSandboxLiveFiles(
	ctx context.Context, sessionID, relativeDir string,
) ([]SandboxLiveFileEntry, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session ID is required")
	}
	query := url.Values{}
	if relativeDir != "" {
		query.Set("path", relativeDir)
	}
	resp, err := c.doRequest(ctx, http.MethodGet, sandboxLiveFilesPath(sessionID), nil, query)
	if err != nil {
		return nil, err
	}
	var response sandboxLiveFileListResponse
	if err := parseResponse(resp, &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return []SandboxLiveFileEntry{}, nil
	}
	return response.Data, nil
}

// OpenSandboxLiveFile starts a download of one live output file and returns
// the server-suggested filename plus a streaming reader. Callers MUST Close
// the reader.
func (c *Client) OpenSandboxLiveFile(
	ctx context.Context, sessionID, relativePath string,
) (string, io.ReadCloser, error) {
	if sessionID == "" {
		return "", nil, fmt.Errorf("session ID is required")
	}
	if relativePath == "" {
		return "", nil, fmt.Errorf("path is required")
	}
	query := url.Values{"path": {relativePath}}
	resp, err := c.doRequest(ctx, http.MethodGet, sandboxLiveFilesPath(sessionID)+"/content", nil, query)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return "", nil, newAPIError(resp.StatusCode, body)
	}
	filename := filenameFromContentDisposition(resp.Header.Get("Content-Disposition"))
	return filename, resp.Body, nil
}

// UploadSandboxLiveFile writes a new file under /workspace/output without
// overwriting an existing entry. relativePath is POSIX-relative to that root.
func (c *Client) UploadSandboxLiveFile(
	ctx context.Context, sessionID, relativePath, filename string, content []byte,
) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	if relativePath == "" {
		return fmt.Errorf("path is required")
	}
	if filename == "" {
		filename = relativePath
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("path", relativePath); err != nil {
		return fmt.Errorf("write sandbox upload path: %w", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create sandbox upload part: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return fmt.Errorf("write sandbox upload part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close sandbox upload form: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+sandboxLiveFilesPath(sessionID), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthHeaders(ctx, req)
	req.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	req.ContentLength = int64(body.Len())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RenameSandboxLiveFile atomically renames a live-file entry without replacing
// an existing target.
func (c *Client) RenameSandboxLiveFile(ctx context.Context, sessionID, source, target string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	resp, err := c.doRequest(ctx, http.MethodPatch, sandboxLiveFilesPath(sessionID), map[string]string{
		"source": source,
		"target": target,
	}, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// DeleteSandboxLiveFile removes a live-file entry or a prevalidated directory tree.
func (c *Client) DeleteSandboxLiveFile(ctx context.Context, sessionID, relativePath string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID is required")
	}
	if relativePath == "" {
		return fmt.Errorf("path is required")
	}
	query := url.Values{"path": {relativePath}}
	resp, err := c.doRequest(ctx, http.MethodDelete, sandboxLiveFilesPath(sessionID), nil, query)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}
