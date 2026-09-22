package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/utils"
)

// MinerU V1 API (mineru-kit api-server / MinerU >= 4) client.
//
// The legacy self-hosted API exposed a single synchronous endpoint
// (POST /file_parse). MinerU 4 replaced it with the async V1 API:
//
//	POST /v1/uploads            → create upload session (returns upload_url)
//	PUT  {upload_url}           → raw bytes
//	POST /v1/uploads/{id}/complete → {file:{id}}
//	POST /v1/parse/jobs         → 202 {job_id}
//	GET  /v1/parse/jobs/{id}    → {status, files[].output_files}
//	GET  /v1/files/{id}/content → artifact bytes
//	GET  /v1/health             → liveness
//
// Both flavors are auto-detected per request (see detectMinerUAPI); the V1
// client materializes the exact same (markdown, images) shape as the legacy
// /file_parse response so all downstream processing is shared.

const (
	minerUV1PollInterval = 3 * time.Second
	// Job states from the V1 API. "partial" means at least one file in the
	// job succeeded; single-file jobs treat it like completed.
	minerUV1ZipFormat = "zip"
)

type mineruV1Client struct {
	endpoint string
	apiKey   string
	tier     string // flash / basic / standard / advanced when derivable
	client   *http.Client
}

func newMinerUV1Client(endpoint, apiKey, backend string, base *http.Client) *mineruV1Client {
	tier := ""
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case "flash", "basic", "standard", "advanced":
		// MinerU 4 tiers happen to share names with some legacy backend
		// values; anything else (pipeline, vlm-*, hybrid-*) is legacy-only
		// and simply omitted so the server picks its default.
		tier = strings.ToLower(strings.TrimSpace(backend))
	}
	httpClient := base
	if httpClient == nil {
		httpClient = utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
			Timeout:      mineruTimeout,
			MaxRedirects: 5,
		})
	}
	return &mineruV1Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		tier:     tier,
		client:   httpClient,
	}
}

func (c *mineruV1Client) authHeader() http.Header {
	h := http.Header{}
	if c.apiKey != "" {
		h.Set("Authorization", "Bearer "+c.apiKey)
	}
	return h
}

// detectMinerUAPI reports whether endpoint speaks the V1 API, probed via
// GET /v1/health with a short timeout. The probe requires the V1 health
// payload ({"status":"ok",...}) — a plain 200 is not enough, catch-all
// handlers on legacy servers would otherwise be mistaken for V1.
func detectMinerUAPI(ctx context.Context, endpoint, apiKey string) bool {
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      5 * time.Second,
		MaxRedirects: 5,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(endpoint, "/")+"/v1/health", nil)
	if err != nil {
		return false
	}
	probe := mineruV1Client{apiKey: apiKey}
	for k, vs := range probe.authHeader() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false
	}
	var health struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(body, &health) == nil && health.Status == "ok"
}

// v1Upload runs the three-step upload and returns the server-side file id.
func (c *mineruV1Client) v1Upload(ctx context.Context, content []byte, uploadFileName string) (string, error) {
	sum := sha256.Sum256(content)
	payload := map[string]any{
		"filename":  uploadFileName,
		"bytes":     len(content),
		"mime_type": mime.TypeByExtension("." + strings.TrimPrefix(fileExtOf(uploadFileName), ".")),
		"purpose":   "parse",
		"sha256sum": hex.EncodeToString(sum[:]),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/uploads", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range c.authHeader() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("create upload: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create upload status %d: %s", resp.StatusCode, truncateForLog(respBody))
	}

	var created struct {
		ID           string            `json:"id"`
		Status       string            `json:"status"`
		UploadURL    string            `json:"upload_url"`
		UploadHeader map[string]string `json:"upload_headers"`
		File         *struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	// Identical content uploaded before: server already has the bytes.
	if created.Status == "completed" && created.File != nil && created.File.ID != "" {
		return created.File.ID, nil
	}
	if created.UploadURL == "" {
		return "", fmt.Errorf("upload session has no upload_url")
	}

	uploadURL := created.UploadURL
	if strings.HasPrefix(uploadURL, "/") {
		uploadURL = c.endpoint + uploadURL
	}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	for k, v := range created.UploadHeader {
		putReq.Header.Set(k, v)
	}
	for k, vs := range c.authHeader() {
		for _, v := range vs {
			putReq.Header.Add(k, v)
		}
	}
	putResp, err := c.client.Do(putReq)
	if err != nil {
		return "", fmt.Errorf("upload bytes: %w", err)
	}
	defer putResp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(putResp.Body, 1<<20))
	if putResp.StatusCode >= 300 {
		return "", fmt.Errorf("upload bytes status %d", putResp.StatusCode)
	}

	compReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v1/uploads/%s/complete", c.endpoint, created.ID), nil)
	if err != nil {
		return "", err
	}
	for k, vs := range c.authHeader() {
		for _, v := range vs {
			compReq.Header.Add(k, v)
		}
	}
	compResp, err := c.client.Do(compReq)
	if err != nil {
		return "", fmt.Errorf("complete upload: %w", err)
	}
	defer compResp.Body.Close()
	compBody, _ := io.ReadAll(compResp.Body)
	if compResp.StatusCode >= 300 {
		return "", fmt.Errorf("complete upload status %d: %s", compResp.StatusCode, truncateForLog(compBody))
	}
	var done struct {
		File *struct {
			ID string `json:"id"`
		} `json:"file"`
	}
	if err := json.Unmarshal(compBody, &done); err == nil && done.File != nil && done.File.ID != "" {
		return done.File.ID, nil
	}
	// Fall back to the creation-time file id when complete omits it.
	if created.File != nil && created.File.ID != "" {
		return created.File.ID, nil
	}
	return "", fmt.Errorf("complete upload returned no file id")
}

// v1CreateJob submits a parse job for one uploaded file, requesting a
// self-contained zip (markdown + images) so downstream stays identical to
// the legacy /file_parse contract.
func (c *mineruV1Client) v1CreateJob(ctx context.Context, fileID string) (string, error) {
	payload := map[string]any{
		"files": []map[string]any{
			{"source": map[string]any{"type": "file_id", "file_id": fileID}},
		},
		"output_formats": []string{minerUV1ZipFormat},
	}
	if c.tier != "" {
		payload["tier"] = c.tier
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/parse/jobs", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range c.authHeader() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("create parse job: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("create parse job status %d: %s", resp.StatusCode, truncateForLog(respBody))
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil || created.JobID == "" {
		return "", fmt.Errorf("decode parse job response: %w", err)
	}
	return created.JobID, nil
}

// v1Poll waits until the job finishes and returns the artifact file id of
// the requested format (zip preferred, markdown as fallback).
func (c *mineruV1Client) v1Poll(ctx context.Context, jobID, format string) (string, error) {
	ticker := time.NewTicker(minerUV1PollInterval)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			fmt.Sprintf("%s/v1/parse/jobs/%s", c.endpoint, jobID), nil)
		if err != nil {
			return "", err
		}
		for k, vs := range c.authHeader() {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("poll parse job: %w", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("poll parse job status %d: %s", resp.StatusCode, truncateForLog(respBody))
		}

		var job struct {
			Status string `json:"status"`
			Files  []struct {
				OutputFiles map[string]struct {
					FileID string `json:"file_id"`
				} `json:"output_files"`
			} `json:"files"`
		}
		if err := json.Unmarshal(respBody, &job); err != nil {
			return "", fmt.Errorf("decode job status: %w", err)
		}

		switch job.Status {
		case "completed", "partial":
			// Prefer the requested format, fall back to the other one so a
			// server that ignored output_formats still yields content.
			for _, want := range []string{format, "markdown", "zip"} {
				for _, f := range job.Files {
					if ref, ok := f.OutputFiles[want]; ok && ref.FileID != "" {
						return ref.FileID, nil
					}
				}
			}
			return "", fmt.Errorf("job %s finished without %s output", jobID, format)
		case "failed", "canceled":
			return "", fmt.Errorf("job %s ended with status %s", jobID, job.Status)
		}
		// queued / running → wait for next tick
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// v1Download fetches an artifact's bytes.
func (c *mineruV1Client) v1Download(ctx context.Context, fileID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/files/%s/content", c.endpoint, fileID), nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range c.authHeader() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download artifact: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("download artifact status %d: %s", resp.StatusCode, truncateForLog(body))
	}
	return io.ReadAll(resp.Body)
}

// v1Parse runs the full V1 flow and returns the legacy-shaped result
// (markdown + base64 image map keyed by relative path, e.g. "images/x.jpg").
func (c *mineruV1Client) v1Parse(
	ctx context.Context, content []byte, uploadFileName string,
) (string, map[string]string, error) {
	fileID, err := c.v1Upload(ctx, content, uploadFileName)
	if err != nil {
		return "", nil, err
	}
	jobID, err := c.v1CreateJob(ctx, fileID)
	if err != nil {
		return "", nil, err
	}
	logger.Infof(context.Background(), "[MinerU] V1 job=%s submitted (file=%s)", jobID, fileID)

	artifactID, err := c.v1Poll(ctx, jobID, minerUV1ZipFormat)
	if err != nil {
		return "", nil, err
	}
	artifact, err := c.v1Download(ctx, artifactID)
	if err != nil {
		return "", nil, err
	}

	mdContent, images, err := minerUV1ExtractZip(artifact)
	if err != nil {
		return "", nil, err
	}
	logger.Infof(context.Background(), "[MinerU] V1 done: markdown=%d chars, images=%d", len(mdContent), len(images))
	return mdContent, images, nil
}

// minerUV1ExtractZip unpacks the self-contained result zip: markdown.md at
// the root plus images under their relative paths. Image bytes are returned
// as raw base64 — the shared downstream processor accepts both data URIs and
// raw base64 and derives the extension from the path.
func minerUV1ExtractZip(artifact []byte) (string, map[string]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(artifact), int64(len(artifact)))
	if err != nil {
		return "", nil, fmt.Errorf("open result zip: %w", err)
	}
	var mdContent string
	images := make(map[string]string)
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.TrimPrefix(filepath.ToSlash(f.Name), "./")
		rc, err := f.Open()
		if err != nil {
			return "", nil, fmt.Errorf("open zip entry %s: %w", name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return "", nil, fmt.Errorf("read zip entry %s: %w", name, err)
		}
		switch {
		case name == "markdown.md" || (mdContent == "" && strings.HasSuffix(name, ".md")):
			if name == "markdown.md" || mdContent == "" {
				mdContent = string(data)
			}
		case strings.HasPrefix(name, "images/"):
			images[name] = base64.StdEncoding.EncodeToString(data)
		}
	}
	if mdContent == "" {
		return "", nil, fmt.Errorf("result zip has no markdown.md")
	}
	return mdContent, images, nil
}

func fileExtOf(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i:]
	}
	return ""
}

func truncateForLog(b []byte) string {
	s := string(b)
	if len(s) > 512 {
		return s[:512]
	}
	return s
}
