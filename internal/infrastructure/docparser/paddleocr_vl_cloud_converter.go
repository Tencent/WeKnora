package docparser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	paddleOCRVLCloudDefaultBaseURL = "https://paddleocr.aistudio-app.com/api/v2/ocr/jobs"
	paddleOCRVLCloudDefaultModel   = "PaddleOCR-VL-1.6"
	paddleOCRVLCloudPollInterval   = 5 * time.Second
	paddleOCRVLCloudTimeout        = 600 * time.Second
)

// PaddleOCRVLCloudReader calls the PaddleOCR-VL AI Studio cloud API.
// Flow: POST /jobs (multipart) → poll GET /jobs/{id} → download result JSONL,
// then fetch each referenced image URL.
type PaddleOCRVLCloudReader struct {
	token    string
	baseURL  string
	model    string
	useSeal  bool
	useChart bool
}

// NewPaddleOCRVLCloudReader creates a reader from ParserEngineOverrides.
func NewPaddleOCRVLCloudReader(overrides map[string]string) *PaddleOCRVLCloudReader {
	return &PaddleOCRVLCloudReader{
		token:    strings.TrimSpace(overrides["paddleocr_vl_cloud_token"]),
		baseURL:  strings.TrimRight(stringOr(overrides["paddleocr_vl_cloud_base_url"], paddleOCRVLCloudDefaultBaseURL), "/"),
		model:    stringOr(overrides["paddleocr_vl_cloud_model"], paddleOCRVLCloudDefaultModel),
		useSeal:  parseBoolOr(overrides["paddleocr_vl_cloud_use_seal_recognition"], true),
		useChart: parseBoolOr(overrides["paddleocr_vl_cloud_use_chart_recognition"], false),
	}
}

func (c *PaddleOCRVLCloudReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if c.token == "" {
		return &types.ReadResult{Error: "PaddleOCR-VL Cloud token is not configured"}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] Parsing file=%s size=%d model=%s",
		req.FileName, len(content), c.model)

	jobID, err := c.submitJob(ctx, req, content)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud submit: %w", err)
	}

	jsonlURL, err := c.pollJob(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud poll: %w", err)
	}

	mdContent, imagesURL, err := c.fetchResults(jsonlURL)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL Cloud fetch results: %w", err)
	}

	imageRefs := c.downloadImages(mdContent, imagesURL)
	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] Parsed successfully, markdown=%d chars, images=%d",
		len(mdContent), len(imageRefs))

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
	}, nil
}

func (c *PaddleOCRVLCloudReader) optionalPayload() map[string]interface{} {
	// Shared with the self-hosted engine so both produce identical output.
	return paddleOCRVLRecognitionParams(c.useSeal, c.useChart)
}

// --- job submit ---

type paddleOCRVLCloudSubmitResponse struct {
	Data struct {
		JobID string `json:"jobId"`
	} `json:"data"`
	ErrorCode int    `json:"errorCode"`
	ErrorMsg  string `json:"errorMsg"`
}

func (c *PaddleOCRVLCloudReader) submitJob(ctx context.Context, req *types.ReadRequest, content []byte) (string, error) {
	optional, err := json.Marshal(c.optionalPayload())
	if err != nil {
		return "", fmt.Errorf("marshal optionalPayload: %w", err)
	}

	fileName := req.FileName
	if fileName == "" {
		ext := strings.TrimPrefix(req.FileType, ".")
		if ext == "" {
			ext = "pdf"
		}
		fileName = "document." + ext
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", c.model)
	_ = writer.WriteField("optionalPayload", string(optional))
	part, err := writer.CreateFormFile("file", filepath.Base(fileName))
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", fmt.Errorf("write file content: %w", err)
	}
	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "bearer "+c.token)
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 60 * time.Second, MaxRedirects: 5})
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API status %d: %s", resp.StatusCode, string(respBody))
	}

	var result paddleOCRVLCloudSubmitResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.Data.JobID == "" {
		return "", fmt.Errorf("API returned no jobId: %s", string(respBody))
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] job submitted: jobId=%s", result.Data.JobID)
	return result.Data.JobID, nil
}

// --- polling ---

type paddleOCRVLCloudPollResponse struct {
	Data struct {
		State           string `json:"state"`
		ErrorMsg        string `json:"errorMsg"`
		ExtractProgress struct {
			TotalPages     int `json:"totalPages"`
			ExtractedPages int `json:"extractedPages"`
		} `json:"extractProgress"`
		ResultURL struct {
			JSONURL string `json:"jsonUrl"`
		} `json:"resultUrl"`
	} `json:"data"`
}

func (c *PaddleOCRVLCloudReader) pollJob(ctx context.Context, jobID string) (string, error) {
	deadline := time.Now().Add(paddleOCRVLCloudTimeout)
	pollCount := 0
	url := c.baseURL + "/" + jobID

	for time.Now().Before(deadline) {
		// Bail out promptly when the caller cancels (task cancelled / timed
		// out) instead of spinning: client.Do would fail immediately and
		// sleepCtx returns at once on a cancelled ctx, so without this guard
		// the loop busy-hammers the cloud API and floods logs until deadline.
		if err := ctx.Err(); err != nil {
			return "", err
		}
		pollCount++

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("create poll request: %w", err)
		}
		httpReq.Header.Set("Authorization", "bearer "+c.token)

		client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 30 * time.Second, MaxRedirects: 5})
		resp, err := client.Do(httpReq)
		if err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d failed: %v", pollCount, err)
			sleepCtx(ctx, paddleOCRVLCloudPollInterval)
			continue
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d status %d: %s", pollCount, resp.StatusCode, string(respBody))
			sleepCtx(ctx, paddleOCRVLCloudPollInterval)
			continue
		}

		var pollResp paddleOCRVLCloudPollResponse
		if err := json.Unmarshal(respBody, &pollResp); err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] poll #%d decode error: %v", pollCount, err)
			sleepCtx(ctx, paddleOCRVLCloudPollInterval)
			continue
		}

		state := strings.ToLower(pollResp.Data.State)
		if pollCount == 1 || pollCount%6 == 0 || state == "done" || state == "failed" {
			logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] poll #%d: state=%s pages=%d/%d",
				pollCount, state, pollResp.Data.ExtractProgress.ExtractedPages, pollResp.Data.ExtractProgress.TotalPages)
		}

		switch state {
		case "done":
			if pollResp.Data.ResultURL.JSONURL == "" {
				return "", fmt.Errorf("state=done but no jsonUrl")
			}
			return pollResp.Data.ResultURL.JSONURL, nil
		case "failed":
			return "", fmt.Errorf("task failed: %s", pollResp.Data.ErrorMsg)
		}

		sleepCtx(ctx, paddleOCRVLCloudPollInterval)
	}

	return "", fmt.Errorf("task timed out after %d polls", pollCount)
}

// --- result parsing ---

type paddleOCRVLCloudResultLine struct {
	Result struct {
		LayoutParsingResults []struct {
			Markdown struct {
				Text   string            `json:"text"`
				Images map[string]string `json:"images"`
			} `json:"markdown"`
		} `json:"layoutParsingResults"`
	} `json:"result"`
}

func (c *PaddleOCRVLCloudReader) fetchResults(jsonlURL string) (string, map[string]string, error) {
	if err := utils.ValidateURLForSSRF(jsonlURL); err != nil {
		return "", nil, fmt.Errorf("jsonl URL blocked by SSRF check: %v", err)
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 120 * time.Second, MaxRedirects: 5})
	resp, err := client.Get(jsonlURL)
	if err != nil {
		return "", nil, fmt.Errorf("download jsonl: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("download jsonl status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read jsonl body: %w", err)
	}

	texts := make([]string, 0)
	images := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var parsed paddleOCRVLCloudResultLine
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] skip malformed jsonl line: %v", err)
			continue
		}
		for _, p := range parsed.Result.LayoutParsingResults {
			if t := strings.TrimSpace(p.Markdown.Text); t != "" {
				texts = append(texts, p.Markdown.Text)
			}
			for path, u := range p.Markdown.Images {
				if _, ok := images[path]; !ok {
					images[path] = u
				}
			}
		}
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL Cloud] fetched %d page(s), images=%d", len(texts), len(images))
	return strings.Join(texts, "\n\n"), images, nil
}

// downloadImages fetches each referenced image URL and builds ImageRef entries.
func (c *PaddleOCRVLCloudReader) downloadImages(mdContent string, imagesURL map[string]string) []types.ImageRef {
	var refs []types.ImageRef
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 60 * time.Second, MaxRedirects: 5})

	for ipath, u := range imagesURL {
		matchedRefs := mineruImageOriginalRefs(mdContent, ipath)
		if len(matchedRefs) == 0 {
			continue
		}
		if err := utils.ValidateURLForSSRF(u); err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] image URL blocked %s: %v", ipath, err)
			continue
		}
		resp, err := client.Get(u)
		if err != nil {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] download image %s: %v", ipath, err)
			continue
		}
		imgBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			logger.Errorf(context.Background(), "[PaddleOCR-VL Cloud] read image %s status=%d err=%v", ipath, resp.StatusCode, err)
			continue
		}

		ext := strings.TrimPrefix(filepath.Ext(ipath), ".")
		if ext == "" {
			ext = "png"
		}
		mimeType := mime.TypeByExtension("." + ext)
		if mimeType == "" {
			mimeType = "image/png"
		}

		for _, originalRef := range matchedRefs {
			refs = append(refs, types.ImageRef{
				Filename:    ipath,
				OriginalRef: originalRef,
				MimeType:    mimeType,
				ImageData:   imgBytes,
			})
		}
	}

	return refs
}

// PingPaddleOCRVLCloud checks whether the cloud token is present (the API has
// no lightweight health endpoint, so we only validate configuration here).
func PingPaddleOCRVLCloud(token string) (bool, string) {
	if strings.TrimSpace(token) == "" {
		return false, "未配置 PaddleOCR-VL Cloud Token"
	}
	return true, ""
}
