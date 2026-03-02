package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

const (
	defaultPollInterval = 3 * time.Second
	defaultCloudTimeout = 600 * time.Second
	defaultBaseURL      = "https://mineru.net/api/v4"
)

// MinerUCloudReader calls the MinerU Cloud API (mineru.net) to read/convert documents.
// Flow: POST /file-urls/batch → PUT file → poll GET /extract-results/batch/{batch_id}.
type MinerUCloudReader struct {
	apiKey       string
	baseURL      string
	model        string
	formulaEnable bool
	tableEnable   bool
	ocrEnable     bool
	language      string
}

// NewMinerUCloudReader creates a reader from ParserEngineOverrides.
func NewMinerUCloudReader(overrides map[string]string) *MinerUCloudReader {
	baseURL := strings.TrimRight(stringOr(overrides["mineru_api_base_url"], defaultBaseURL), "/")
	if safe, reason := utils.IsSSRFSafeURL(baseURL); !safe {
		logger.Printf("WARN: [MinerUCloud] baseURL rejected by SSRF check (%s), falling back to default", reason)
		baseURL = defaultBaseURL
	}
	return &MinerUCloudReader{
		apiKey:        strings.TrimSpace(overrides["mineru_api_key"]),
		baseURL:       baseURL,
		model:         stringOr(overrides["mineru_cloud_model"], "pipeline"),
		formulaEnable: parseBoolOr(overrides["mineru_cloud_enable_formula"], true),
		tableEnable:   parseBoolOr(overrides["mineru_cloud_enable_table"], true),
		ocrEnable:     parseBoolOr(overrides["mineru_cloud_enable_ocr"], true),
		language:      stringOr(overrides["mineru_cloud_language"], "ch"),
	}
}

func (c *MinerUCloudReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if c.apiKey == "" {
		return &types.ReadResult{Error: "MinerU Cloud API key is not configured"}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Printf("INFO: [MinerUCloud] Parsing file=%s size=%d via %s", req.FileName, len(content), c.baseURL)

	ext := filepath.Ext(req.FileName)
	if ext == "" && req.FileType != "" {
		ext = "." + req.FileType
	}
	if ext == "" {
		ext = ".pdf"
	}
	fileName := strings.TrimSuffix(req.FileName, ext) + ext
	if fileName == ext {
		fileName = "document" + ext
	}

	batchID, uploadURL, err := c.applyUploadURLs(ctx, fileName, ext)
	if err != nil {
		return nil, fmt.Errorf("MinerU Cloud apply upload URLs: %w", err)
	}

	if err := c.uploadFile(ctx, uploadURL, content); err != nil {
		return nil, fmt.Errorf("MinerU Cloud file upload: %w", err)
	}

	mdContent, imageRefs, err := c.pollBatchResult(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("MinerU Cloud poll: %w", err)
	}

	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
	}, nil
}

// --- batch upload API ---

type batchApplyResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		BatchID  string   `json:"batch_id"`
		FileURLs []string `json:"file_urls"`
	} `json:"data"`
}

func (c *MinerUCloudReader) applyUploadURLs(ctx context.Context, fileName, ext string) (string, string, error) {
	modelVersion := c.model
	if strings.ToLower(ext) == ".html" {
		modelVersion = "MinerU-HTML"
	}

	payload := map[string]interface{}{
		"files":          []map[string]string{{"name": fileName, "data_id": uuid.New().String()}},
		"model_version":  modelVersion,
		"is_ocr":         c.ocrEnable,
		"enable_formula": c.formulaEnable,
		"enable_table":   c.tableEnable,
		"language":       c.language,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/file-urls/batch", bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("API status %d: %s", resp.StatusCode, string(respBody))
	}

	var result batchApplyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decode response: %w", err)
	}
	if result.Code != 0 {
		return "", "", fmt.Errorf("API error: %s", result.Msg)
	}
	if len(result.Data.FileURLs) == 0 {
		return "", "", fmt.Errorf("API returned no file_urls")
	}

	logger.Printf("INFO: [MinerUCloud] batch apply ok: batch_id=%s, urls=%d", result.Data.BatchID, len(result.Data.FileURLs))
	return result.Data.BatchID, result.Data.FileURLs[0], nil
}

func (c *MinerUCloudReader) uploadFile(ctx context.Context, uploadURL string, content []byte) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 120 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("PUT upload: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("PUT upload status %d", resp.StatusCode)
	}
	logger.Printf("INFO: [MinerUCloud] file uploaded, status=%d", resp.StatusCode)
	return nil
}

// --- polling ---

type batchPollResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		ExtractResult json.RawMessage `json:"extract_result"` // can be object or array
	} `json:"data"`
}

type extractResultItem struct {
	State    string `json:"state"`
	FileName string `json:"file_name"`
	Markdown string `json:"markdown"`
	Content  string `json:"content"`
	Text     string `json:"text"`
	ErrMsg   string `json:"err_msg"`
	Progress struct {
		ExtractedPages int `json:"extracted_pages"`
		TotalPages     int `json:"total_pages"`
	} `json:"extract_progress"`
	FullZipURL string `json:"full_zip_url"`
}

func (c *MinerUCloudReader) pollBatchResult(ctx context.Context, batchID string) (string, []types.ImageRef, error) {
	deadline := time.Now().Add(defaultCloudTimeout)
	pollCount := 0
	headers := map[string]string{
		"Authorization": "Bearer " + c.apiKey,
	}

	for time.Now().Before(deadline) {
		pollCount++

		items, err := c.fetchBatchStatus(ctx, batchID, headers)
		if err != nil {
			logger.Printf("WARN: [MinerUCloud] poll #%d failed: %v", pollCount, err)
			sleepCtx(ctx, defaultPollInterval)
			continue
		}

		if len(items) == 0 {
			if pollCount <= 3 || pollCount%10 == 0 {
				logger.Printf("INFO: [MinerUCloud] poll #%d: extract_result empty, retrying", pollCount)
			}
			sleepCtx(ctx, defaultPollInterval)
			continue
		}

		item := items[0]
		state := strings.ToLower(item.State)

		if pollCount == 1 || pollCount%10 == 0 || state == "done" || state == "failed" {
			logger.Printf("INFO: [MinerUCloud] poll #%d: file=%s state=%s pages=%d/%d",
				pollCount, item.FileName, state, item.Progress.ExtractedPages, item.Progress.TotalPages)
		}

		if state == "failed" {
			return "", nil, fmt.Errorf("MinerU Cloud task failed: %s", item.ErrMsg)
		}

		if state == "done" {
			return c.extractDoneResult(ctx, &item)
		}

		sleepCtx(ctx, defaultPollInterval)
	}

	return "", nil, fmt.Errorf("MinerU Cloud task timed out after %d polls", pollCount)
}

func (c *MinerUCloudReader) fetchBatchStatus(ctx context.Context, batchID string, headers map[string]string) ([]extractResultItem, error) {
	url := fmt.Sprintf("%s/extract-results/batch/%s", c.baseURL, batchID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read poll response body: %w", err)
	}

	var pollResp batchPollResponse
	if err := json.Unmarshal(respBody, &pollResp); err != nil {
		return nil, fmt.Errorf("decode poll response: %w", err)
	}
	if pollResp.Code != 0 {
		return nil, fmt.Errorf("poll error code=%d msg=%s", pollResp.Code, pollResp.Msg)
	}

	if len(pollResp.Data.ExtractResult) == 0 {
		return nil, nil
	}

	// Dump the raw extract_result JSON for debugging
	rawExtract := string(pollResp.Data.ExtractResult)
	if len(rawExtract) > 4000 {
		logger.Printf("DEBUG: [MinerUCloud] Raw extract_result (truncated to 4000 chars): %s ...", rawExtract[:4000])
	} else {
		logger.Printf("DEBUG: [MinerUCloud] Raw extract_result: %s", rawExtract)
	}

	// Pretty-print the structure to reveal all available fields
	var rawObj interface{}
	if err := json.Unmarshal(pollResp.Data.ExtractResult, &rawObj); err == nil {
		logResponseStructure("MinerUCloud", rawObj, "extract_result")
	}

	// The extract_result can be either a single object or an array
	var items []extractResultItem
	if pollResp.Data.ExtractResult[0] == '[' {
		if err := json.Unmarshal(pollResp.Data.ExtractResult, &items); err != nil {
			return nil, fmt.Errorf("decode extract_result array: %w", err)
		}
	} else {
		var single extractResultItem
		if err := json.Unmarshal(pollResp.Data.ExtractResult, &single); err != nil {
			return nil, fmt.Errorf("decode extract_result object: %w", err)
		}
		items = []extractResultItem{single}
	}

	return items, nil
}

// extractDoneResult extracts markdown and images from a completed batch item.
// Prefers inline markdown/content fields; falls back to downloading full_zip_url.
func (c *MinerUCloudReader) extractDoneResult(_ context.Context, item *extractResultItem) (string, []types.ImageRef, error) {
	text := firstNonEmpty(item.Markdown, item.Content, item.Text)
	if text != "" {
		logger.Printf("INFO: [MinerUCloud] parsed (inline), length=%d", len(text))
		return text, nil, nil
	}

	if item.FullZipURL == "" {
		return "", nil, fmt.Errorf("MinerU Cloud state=done but no markdown/content or full_zip_url")
	}

	md, imageRefs, err := downloadAndExtractZip(item.FullZipURL)
	if err != nil {
		return "", nil, fmt.Errorf("extract zip: %w", err)
	}

	logger.Printf("INFO: [MinerUCloud] parsed (zip), markdown=%d chars, images=%d", len(md), len(imageRefs))
	return md, imageRefs, nil
}

// --- ZIP handling ---

var imgRefPattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

func downloadAndExtractZip(zipURL string) (string, []types.ImageRef, error) {
	if safe, reason := utils.IsSSRFSafeURL(zipURL); !safe {
		return "", nil, fmt.Errorf("zip URL blocked by SSRF check: %s", reason)
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 120 * time.Second, MaxRedirects: 5})
	resp, err := client.Get(zipURL)
	if err != nil {
		return "", nil, fmt.Errorf("download zip: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download zip status %d", resp.StatusCode)
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read zip body: %w", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", nil, fmt.Errorf("open zip: %w", err)
	}

	// Find .md files
	var mdFiles []string
	entries := make(map[string]*zip.File)
	for _, f := range zr.File {
		entries[f.Name] = f
		if strings.HasSuffix(f.Name, ".md") {
			mdFiles = append(mdFiles, f.Name)
		}
	}
	if len(mdFiles) == 0 {
		return "", nil, fmt.Errorf("no .md file found in zip")
	}
	sort.Slice(mdFiles, func(i, j int) bool {
		di, dj := strings.Count(mdFiles[i], "/"), strings.Count(mdFiles[j], "/")
		if di != dj {
			return di < dj
		}
		return mdFiles[i] < mdFiles[j]
	})

	mdText, err := readZipEntry(entries[mdFiles[0]])
	if err != nil {
		return "", nil, fmt.Errorf("read md file: %w", err)
	}

	mdDir := filepath.Dir(mdFiles[0])

	// Extract referenced images
	var imageRefs []types.ImageRef
	seen := map[string]bool{}
	for _, match := range imgRefPattern.FindAllStringSubmatch(mdText, -1) {
		imgPath := match[1]
		if strings.HasPrefix(imgPath, "http://") || strings.HasPrefix(imgPath, "https://") || strings.HasPrefix(imgPath, "data:") {
			continue
		}
		if seen[imgPath] {
			continue
		}
		seen[imgPath] = true

		resolved := resolveInZip(imgPath, mdDir, entries)
		if resolved == nil {
			logger.Printf("WARN: [MinerUCloud] image not found in zip: %s", imgPath)
			continue
		}

		imgData, err := readZipEntryBytes(resolved)
		if err != nil {
			logger.Printf("WARN: [MinerUCloud] failed to read zip image %s: %v", imgPath, err)
			continue
		}

		ext := strings.ToLower(filepath.Ext(resolved.Name))
		if ext == "" {
			ext = ".png"
		}
		mimeType := mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "image/png"
		}

		imageRefs = append(imageRefs, types.ImageRef{
			Filename:    filepath.Base(resolved.Name),
			OriginalRef: imgPath,
			MimeType:    mimeType,
			ImageData:   imgData,
		})
	}

	return mdText, imageRefs, nil
}

func resolveInZip(imgPath, mdDir string, entries map[string]*zip.File) *zip.File {
	normalized := strings.ReplaceAll(imgPath, "\\", "/")
	if f, ok := entries[normalized]; ok {
		return f
	}
	if mdDir != "" && mdDir != "." {
		joined := mdDir + "/" + normalized
		if f, ok := entries[joined]; ok {
			return f
		}
	}
	return nil
}

func readZipEntry(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func readZipEntryBytes(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// logCloudResponseStructure is now unified in helpers.go as logResponseStructure("MinerUCloud", ...)

// PingMinerUCloud checks if the MinerU Cloud API is reachable with the given API key.
func PingMinerUCloud(apiKey, baseURL string) (bool, string) {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if apiKey == "" {
		return false, "未配置 MinerU Cloud API Key"
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	targetURL := baseURL + "/file-urls/batch"
	if safe, reason := utils.IsSSRFSafeURL(targetURL); !safe {
		return false, fmt.Sprintf("baseURL 不安全: %s", reason)
	}

	payload := []byte(`{"files":[],"model_version":"pipeline"}`)
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return false, fmt.Sprintf("构建请求失败: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      10 * time.Second,
		MaxRedirects: 5,
	})
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("MinerU Cloud 不可达: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return false, "MinerU Cloud API Key 无效"
	}
	return true, ""
}

// firstNonEmpty, sleepCtx are defined in helpers.go
