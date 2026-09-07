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

// BackupManifest describes an archive's provenance.
type BackupManifest struct {
	SchemaVersion  int    `json:"schema_version"`
	CreatedAt      string `json:"created_at"`
	WeKnoraVersion string `json:"weknora_version"`
	Edition        string `json:"edition,omitempty"`
	DBDriver       string `json:"db_driver"`
	StorageType    string `json:"storage_type"`
	FilesBaseDir   string `json:"files_base_dir,omitempty"`
	Includes       struct {
		Database bool `json:"database"`
		Files    bool `json:"files"`
	} `json:"includes"`
}

// BackupSnapshot is a server-side snapshot listed by ListBackupSnapshots.
type BackupSnapshot struct {
	ID        string         `json:"id"`
	Note      string         `json:"note"`
	CreatedAt string         `json:"created_at"`
	SizeBytes int64          `json:"size_bytes"`
	Manifest  BackupManifest `json:"manifest"`
}

// RestoreSummary is returned after a successful full-instance restore.
type RestoreSummary struct {
	RestoredAt         string `json:"restored_at"`
	SourceVersion      string `json:"source_version"`
	SourceCreatedAt    string `json:"source_created_at"`
	FilesRestored      bool   `json:"files_restored"`
	PreRestoreSnapshot string `json:"pre_restore_snapshot"`
	RestartRequired    bool   `json:"restart_required"`
	Note               string `json:"note"`
}

// ExportBackup streams a freshly built full-instance archive.
// The caller must Close the returned reader. Uses the streaming HTTP client
// so the default 30s timeout does not abort pg_dump.
func (c *Client) ExportBackup(ctx context.Context) (string, io.ReadCloser, error) {
	resp, err := c.doRequestStream(ctx, http.MethodGet, "/api/v1/backups/export", nil, nil)
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

// CreateBackupSnapshot stores a full-instance archive on the server.
func (c *Client) CreateBackupSnapshot(ctx context.Context, note string) (*BackupSnapshot, error) {
	resp, err := c.doRequestStream(ctx, http.MethodPost, "/api/v1/backups", map[string]string{"note": note}, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool           `json:"success"`
		Data    BackupSnapshot `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}

// ListBackupSnapshots lists server-side snapshots, newest first.
func (c *Client) ListBackupSnapshots(ctx context.Context) ([]BackupSnapshot, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/backups", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool             `json:"success"`
		Data    []BackupSnapshot `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DownloadBackupSnapshot streams a stored snapshot. The caller must Close the reader.
func (c *Client) DownloadBackupSnapshot(ctx context.Context, id string) (string, io.ReadCloser, error) {
	path := fmt.Sprintf("/api/v1/backups/%s/download", url.PathEscape(id))
	resp, err := c.doRequestStream(ctx, http.MethodGet, path, nil, nil)
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

// DeleteBackupSnapshot deletes a stored snapshot and its sidecar.
func (c *Client) DeleteBackupSnapshot(ctx context.Context, id string) error {
	path := fmt.Sprintf("/api/v1/backups/%s", url.PathEscape(id))
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, nil)
	if err != nil {
		return err
	}
	var result struct {
		Success bool `json:"success"`
	}
	return parseResponse(resp, &result)
}

// RestoreBackupFromSnapshot replaces the instance from a stored snapshot id.
// confirm=true is sent automatically. Restart the process after success.
func (c *Client) RestoreBackupFromSnapshot(ctx context.Context, snapshotID string) (*RestoreSummary, error) {
	return c.restoreBackup(ctx, snapshotID, nil, "")
}

// RestoreBackupFromReader replaces the instance from an uploaded archive.
func (c *Client) RestoreBackupFromReader(ctx context.Context, r io.Reader, filename string) (*RestoreSummary, error) {
	return c.restoreBackup(ctx, "", r, filename)
}

func (c *Client) restoreBackup(
	ctx context.Context,
	snapshotID string,
	file io.Reader,
	filename string,
) (*RestoreSummary, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("confirm", "true"); err != nil {
		return nil, err
	}
	if snapshotID != "" {
		if err := writer.WriteField("snapshot_id", snapshotID); err != nil {
			return nil, err
		}
	}
	if file != nil {
		if filename == "" {
			filename = "weknora-backup.tar.gz"
		}
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, file); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/backups/restore", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuthHeaders(ctx, req)

	sc := *c.httpClient
	sc.Timeout = c.streamTimeout
	resp, err := sc.Do(req)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool           `json:"success"`
		Data    RestoreSummary `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result.Data, nil
}
