package docparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// MinerU >= 4.1 replaced the synchronous /file_parse endpoint with the V1
// task API: create task → upload bytes → poll → download artifacts. See
// https://opendatalab.github.io/MinerU/usage/http_api/.

const (
	mineruV1PollInitial  = 2 * time.Second
	mineruV1PollMax      = 30 * time.Second
	mineruV1APITimeout   = 30 * time.Second
	mineruV1MaxZipBytes  = 1 << 30
	mineruV1CancelBudget = 10 * time.Second
)

// MinerU V1 tiers accepted by POST /v1/tasks. An empty tier means the
// server's default selection policy (standard → basic for PDF/image).
var mineruV1Tiers = map[string]struct{}{
	"flash":    {},
	"basic":    {},
	"standard": {},
	"advanced": {},
}

type minerUProtocol int

const (
	minerUProtocolLegacy minerUProtocol = iota // MinerU <= 3.x: POST /file_parse
	minerUProtocolV1                           // MinerU >= 4.1: /v1/*
)

// minerUV1Client talks to any MinerU V1 deployment (self-hosted api-server or
// the official mineru.net/api).
type minerUV1Client struct {
	baseURL      string // without the /v1 suffix
	apiKey       string
	apiClient    *http.Client // small JSON calls
	bulkClient   *http.Client // byte upload and artifact download
	pollInitial  time.Duration
	pollMax      time.Duration
	jobTimeout   time.Duration
	maxZipBytes  int64
	logLabel     string
	cancelBudget time.Duration
}

type minerUV1ParseOptions struct {
	Tier    string // "" → server default
	OCRMode string // auto, txt, ocr
}

func newMinerUV1Client(baseURL, apiKey string, jobTimeout time.Duration) *minerUV1Client {
	return &minerUV1Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		apiClient: utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
			Timeout:      mineruV1APITimeout,
			MaxRedirects: 5,
		}),
		bulkClient: utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
			Timeout:      jobTimeout,
			MaxRedirects: 5,
		}),
		pollInitial:  mineruV1PollInitial,
		pollMax:      mineruV1PollMax,
		jobTimeout:   jobTimeout,
		maxZipBytes:  mineruV1MaxZipBytes,
		logLabel:     "MinerU",
		cancelBudget: mineruV1CancelBudget,
	}
}

// --- wire types ---

type minerUV1ErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

type minerUV1File struct {
	ID string `json:"id"`
}

type minerUV1TaskFile struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	Status   string `json:"status"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// minerUV1Task is a task-mode session (MinerU >= 4.1). The upload file id
// doubles as the artifact id: once the task completes, GET
// /v1/files/{file_id}/content serves the zip bundle.
type minerUV1Task struct {
	TaskID string             `json:"task_id"`
	Status string             `json:"status"`
	Files  []minerUV1TaskFile `json:"files"`
}

func minerUV1TaskTerminal(status string) bool {
	switch status {
	case "completed", "partial", "failed", "canceled":
		return true
	}
	return false
}

func minerUV1JobTerminal(status string) bool {
	switch status {
	case "completed", "partial", "failed", "canceled":
		return true
	}
	return false
}

// --- protocol detection ---

// detectMinerUProtocol probes GET /v1/health. MinerU 4.0 answers 200 (or 503
// while models are still preloading); older servers have no such route and
// answer 404/405. When the service sits behind an authenticating API gateway
// or reverse proxy, the configured apiKey is attached as Authorization: Bearer.
func detectMinerUProtocol(
	ctx context.Context, client *http.Client, endpoint, apiKey string,
) (minerUProtocol, error) {
	endpoint = strings.TrimRight(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/health", nil)
	if err != nil {
		return minerUProtocolLegacy, fmt.Errorf("create health request: %w", err)
	}
	if apiKey = strings.TrimSpace(apiKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return minerUProtocolLegacy, fmt.Errorf("MinerU service unreachable: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	switch resp.StatusCode {
	case http.StatusOK:
		var health struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(body, &health); err != nil || health.Status != "ok" {
			return minerUProtocolLegacy, fmt.Errorf("unexpected /v1/health response: %s",
				truncateForLog(string(body), 300))
		}
		return minerUProtocolV1, nil
	case http.StatusUnauthorized:
		if apiKey == "" {
			return minerUProtocolV1, fmt.Errorf("MinerU service requires authentication, please configure API key")
		}
		if msg := minerUV1ErrorMessage(body); msg != "" {
			return minerUProtocolV1, fmt.Errorf("MinerU authentication failed (status 401): %s", msg)
		}
		return minerUProtocolV1, fmt.Errorf("MinerU /v1/health status 401: %s",
			truncateForLog(string(body), 300))
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return minerUProtocolLegacy, nil
	default:
		if msg := minerUV1ErrorMessage(body); msg != "" {
			return minerUProtocolV1, fmt.Errorf("MinerU service not ready (status %d): %s", resp.StatusCode, msg)
		}
		return minerUProtocolLegacy, fmt.Errorf("MinerU /v1/health status %d: %s",
			resp.StatusCode, truncateForLog(string(body), 300))
	}
}

func minerUV1ErrorMessage(body []byte) string {
	var env minerUV1ErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if env.Error.Code != "" && env.Error.Message != "" {
		return env.Error.Code + ": " + env.Error.Message
	}
	return firstNonEmpty(env.Error.Message, env.Error.Code)
}

// truncateForLog cuts s to at most n bytes, backing up to a rune boundary
// so a multibyte character in the body is never split.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// rawResponseLogBytes caps the debug dump of a provider response.
const rawResponseLogBytes = 4000

// rawResponsePreview returns the debug dump of a provider response within
// rawResponseLogBytes. The response is JSON that usually carries CJK document
// titles and OCR text, so the cut must land on a rune boundary; a byte cut
// leaves invalid UTF-8 in the log sink.
func rawResponsePreview(raw string) string {
	return truncateForLog(raw, rawResponseLogBytes)
}

// --- parse flow ---

// Parse uploads content, runs a parse task and returns the markdown and
// images extracted from the job's zip artifact.
func (c *minerUV1Client) Parse(
	ctx context.Context,
	content []byte,
	fileName string,
	opts minerUV1ParseOptions,
) (string, []types.ImageRef, error) {
	md, refs, _, err := c.ParseWithLayout(ctx, content, fileName, opts)
	return md, refs, err
}

// ParseWithLayout is Parse plus the package's content_list, the per-block
// page and box layout (nil when the package carried none).
func (c *minerUV1Client) ParseWithLayout(
	ctx context.Context,
	content []byte,
	fileName string,
	opts minerUV1ParseOptions,
) (string, []types.ImageRef, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.jobTimeout)
	defer cancel()

	task, err := c.createTask(ctx, content, fileName, opts)
	if err != nil {
		return "", nil, nil, fmt.Errorf("create parse task: %w", err)
	}
	logger.Infof(ctx, "[%s] parse task created: task_id=%s file_id=%s tier=%q ocr_mode=%s",
		c.logLabel, task.TaskID, task.Files[0].FileID, opts.Tier, opts.OCRMode)

	final, err := c.waitTask(ctx, task.TaskID)
	if err != nil {
		c.cancelTask(task.TaskID)
		return "", nil, nil, err
	}

	artifactID, err := minerUV1TaskArtifact(final)
	if err != nil {
		return "", nil, nil, err
	}

	zipData, err := c.downloadFile(ctx, artifactID)
	if err != nil {
		return "", nil, nil, fmt.Errorf("download zip artifact: %w", err)
	}

	md, imageRefs, err := extractMarkdownZip(zipData, c.logLabel)
	if err != nil {
		return "", nil, nil, fmt.Errorf("extract zip artifact: %w", err)
	}
	return md, imageRefs, minerUContentListFromZip(zipData), nil
}

// createTask opens a task-mode session (MinerU >= 4.1): one POST creates the
// task with inline file metadata, then the bytes go up on the task's own
// upload slot. MinerU 4.0 answered 404 here — its upload/parse-job split is
// gone with it.
func (c *minerUV1Client) createTask(
	ctx context.Context,
	content []byte,
	fileName string,
	opts minerUV1ParseOptions,
) (*minerUV1Task, error) {
	payload := map[string]interface{}{
		"files": []map[string]interface{}{
			{"file_name": fileName, "bytes": len(content)},
		},
		"output_formats": []string{"default"},
	}
	if opts.Tier != "" {
		payload["tier"] = opts.Tier
	}
	if opts.OCRMode != "" {
		payload["ocr_mode"] = opts.OCRMode
	}

	var task minerUV1Task
	if err := c.doJSON(ctx, http.MethodPost, "/v1/tasks", payload, &task); err != nil {
		return nil, err
	}
	if task.TaskID == "" {
		return nil, fmt.Errorf("response has no task_id")
	}
	if len(task.Files) == 0 || task.Files[0].FileID == "" {
		return nil, fmt.Errorf("task %s created without an upload slot", task.TaskID)
	}

	if err := c.putTaskFile(ctx, task.TaskID, task.Files[0].FileID, content); err != nil {
		return nil, err
	}
	return &task, nil
}

// putTaskFile uploads the document bytes to the task's inline upload slot.
// The URL is derived from the configured endpoint rather than taken from a
// server response, so a compromised server cannot redirect the upload
// elsewhere.
func (c *minerUV1Client) putTaskFile(ctx context.Context, taskID, fileID string, content []byte) error {
	path := "/v1/tasks/" + url.PathEscape(taskID) + "/uploads/" + url.PathEscape(fileID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("create upload request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	c.setAuth(req)

	resp, err := c.bulkClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload bytes: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("upload bytes status %d: %s", resp.StatusCode, truncateForLog(string(body), 300))
	}
	return nil
}

func (c *minerUV1Client) waitTask(ctx context.Context, taskID string) (*minerUV1Task, error) {
	interval := c.pollInitial
	for poll := 1; ; poll++ {
		var task minerUV1Task
		err := c.doJSON(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), nil, &task)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("parse task %s: %w", taskID, ctxErr)
			}
			var apiErr *minerUV1APIError
			if errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound {
				// Task state lives in process memory; a restarted server forgets it.
				return nil, fmt.Errorf("parse task %s no longer exists (MinerU restarted?): %w", taskID, err)
			}
			logger.Warnf(ctx, "[%s] poll #%d for task %s failed: %v", c.logLabel, poll, taskID, err)
		} else {
			if poll == 1 || poll%10 == 0 || minerUV1TaskTerminal(task.Status) {
				logger.Infof(ctx, "[%s] poll #%d: task=%s status=%s", c.logLabel, poll, taskID, task.Status)
			}
			if minerUV1TaskTerminal(task.Status) {
				return &task, nil
			}
		}

		sleepCtx(ctx, interval)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("parse task %s: %w", taskID, ctxErr)
		}
		interval *= 2
		if interval > c.pollMax {
			interval = c.pollMax
		}
	}
}

// cancelTask best-effort cancels a task we stopped waiting for so the server
// does not keep burning GPU time on it. It runs on a fresh context because
// the caller's context is usually already done.
func (c *minerUV1Client) cancelTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), c.cancelBudget)
	defer cancel()
	if err := c.doJSON(ctx, http.MethodDelete, "/v1/tasks/"+url.PathEscape(taskID), nil, nil); err != nil {
		logger.Warnf(ctx, "[%s] cancel task %s: %v", c.logLabel, taskID, err)
	}
}

// minerUV1TaskArtifact validates the terminal task and returns the file id
// whose /v1/files/{id}/content download serves the zip artifact. Task mode
// reuses the upload file id as the artifact id.
func minerUV1TaskArtifact(task *minerUV1Task) (string, error) {
	if len(task.Files) == 0 {
		return "", fmt.Errorf("parse task %s ended %s without file results", task.TaskID, task.Status)
	}
	f := task.Files[0]
	if f.Status != "completed" {
		reason := task.Status
		if f.Error != nil {
			reason = firstNonEmpty(strings.TrimSpace(f.Error.Code+": "+f.Error.Message), task.Status)
		}
		return "", fmt.Errorf("parse task %s %s: %s", task.TaskID, task.Status, reason)
	}
	if f.FileID == "" {
		return "", fmt.Errorf("parse task %s completed without artifact file id", task.TaskID)
	}
	return f.FileID, nil
}

func (c *minerUV1Client) downloadFile(ctx context.Context, fileID string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/files/"+url.PathEscape(fileID)+"/content", nil)
	if err != nil {
		return nil, err
	}
	// Go drops Authorization when following a redirect to another host, so a
	// 302 to a CDN (official API) does not leak the key.
	c.setAuth(req)

	resp, err := c.bulkClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, newMinerUV1APIError(resp.StatusCode, body)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxZipBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > c.maxZipBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", c.maxZipBytes)
	}
	return data, nil
}

// --- HTTP helpers ---

type minerUV1APIError struct {
	status int
	msg    string
}

func (e *minerUV1APIError) Error() string {
	return fmt.Sprintf("MinerU API status %d: %s", e.status, e.msg)
}

func newMinerUV1APIError(status int, body []byte) *minerUV1APIError {
	msg := minerUV1ErrorMessage(body)
	if msg == "" {
		msg = truncateForLog(string(body), 500)
	}
	return &minerUV1APIError{status: status, msg: msg}
}

func (c *minerUV1Client) doJSON(ctx context.Context, method, path string, in, out interface{}) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setAuth(req)

	resp, err := c.apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newMinerUV1APIError(resp.StatusCode, respBody)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

func (c *minerUV1Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *minerUV1Client) resolveURL(raw string) (*url.URL, error) {
	base, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return nil, err
	}
	ref, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme %q", resolved.Scheme)
	}
	return resolved, nil
}

// sameOrigin compares scheme, host and effective port with the base URL.
func (c *minerUV1Client) sameOrigin(u *url.URL) bool {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(base.Scheme, u.Scheme) &&
		strings.EqualFold(base.Hostname(), u.Hostname()) &&
		effectivePort(base) == effectivePort(u)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// Slim container images often ship without /etc/mime.types, where Go only
// knows a handful of extensions; cover the formats MinerU accepts explicitly.
var minerUV1MimeTypes = map[string]string{
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".bmp":  "image/bmp",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	".html": "text/html",
	".htm":  "text/html",
}

func minerUV1MimeType(fileName string) string {
	ext := strings.ToLower(filepath.Ext(fileName))
	if t, ok := minerUV1MimeTypes[ext]; ok {
		return t
	}
	if ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			if mediaType, _, err := mime.ParseMediaType(t); err == nil {
				return mediaType
			}
			return t
		}
	}
	return "application/octet-stream"
}
