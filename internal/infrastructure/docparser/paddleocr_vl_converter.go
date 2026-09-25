package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

const defaultPaddleOCRVLTimeout = 1000 * time.Second // large scanned PDFs can take a while

// PaddleOCRVLReader calls a self-hosted PaddleOCR-VL pipeline service
// (the full document-parsing API, not the bare VLM inference server).
//
// Flow: POST {endpoint}/layout-parsing with base64 file → synchronous JSON
// response containing per-page markdown + inline base64 images.
type PaddleOCRVLReader struct {
	endpoint string
	timeout  time.Duration
	useSeal  bool
	useChart bool
}

// NewPaddleOCRVLReader creates a reader from ParserEngineOverrides.
func NewPaddleOCRVLReader(overrides map[string]string) *PaddleOCRVLReader {
	return &PaddleOCRVLReader{
		timeout:  paddleOCRVLRequestTimeout(),
		endpoint: strings.TrimRight(overrides["paddleocr_vl_endpoint"], "/"),
		useSeal:  parseBoolOr(overrides["paddleocr_vl_use_seal_recognition"], true),
		useChart: parseBoolOr(overrides["paddleocr_vl_use_chart_recognition"], false),
	}
}

// paddleOCRVLRequestTimeout bounds a self-hosted layout-parsing request.
// The caller's context may impose an earlier deadline.
func paddleOCRVLRequestTimeout() time.Duration {
	value := strings.TrimSpace(os.Getenv("WEKNORA_PADDLEOCR_VL_TIMEOUT"))
	if value == "" {
		return defaultPaddleOCRVLTimeout
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		logger.Warnf(context.Background(), "Invalid WEKNORA_PADDLEOCR_VL_TIMEOUT %q; using %s",
			value, defaultPaddleOCRVLTimeout)
		return defaultPaddleOCRVLTimeout
	}
	return timeout
}

func (c *PaddleOCRVLReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if c.endpoint == "" {
		return &types.ReadResult{Error: "PaddleOCR-VL endpoint is not configured"}, nil
	}
	if err := utils.ValidateURLForSSRF(c.endpoint); err != nil {
		return &types.ReadResult{Error: fmt.Sprintf("PaddleOCR-VL endpoint blocked by SSRF policy: %v", err)}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL] Parsing file=%s size=%d via %s",
		req.FileName, len(content), c.endpoint)

	mdContent, imagesB64, layoutUnits, err := c.callLayoutParsing(ctx, req, content)
	if err != nil {
		return nil, fmt.Errorf("PaddleOCR-VL layout-parsing: %w", err)
	}

	// PaddleOCR-VL renders tables as styled HTML (per-cell text-align), which
	// wastes tokens and defeats the chunker's table-protection logic. Convert
	// them to Markdown tables (or strip layout attributes when conversion is
	// not possible) before downstream processing.
	mdContent = NormalizeHTMLTables(mdContent)

	imageRefs, mdContent := c.processImages(mdContent, imagesB64)
	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	logger.Infof(context.Background(), "[PaddleOCR-VL] Parsed successfully, markdown=%d chars, images=%d",
		len(mdContent), len(imageRefs))

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
		SourceBlocks:    paddleOCRVLSourceBlocks(mdContent, layoutUnits, req.FileType),
	}, nil
}

// paddleOCRVLRecognitionParams returns the recognition / page-restructuring
// parameters shared by the self-hosted (/layout-parsing, top-level body) and
// cloud (optionalPayload) request bodies. Keeping both identical ensures the
// self-hosted engine reproduces the cloud output: cross-page table merging,
// multi-level heading reconstruction, header/footer stripping, and the same
// sampling / resolution settings used by the AI Studio service.
func paddleOCRVLRecognitionParams(useSeal, useChart bool) map[string]interface{} {
	return map[string]interface{}{
		"markdownIgnoreLabels": []string{
			"header", "header_image", "footer", "footer_image",
			"number", "footnote", "aside_text",
		},
		"useDocOrientationClassify": false,
		"useDocUnwarping":           false,
		"useLayoutDetection":        true,
		"useChartRecognition":       useChart,
		"useSealRecognition":        useSeal,
		"useOcrForImageBlock":       false,
		"mergeTables":               true,
		"relevelTitles":             true,
		"restructurePages":          true,
		"layoutShapeMode":           "auto",
		"promptLabel":               "ocr",
		"layoutNms":                 true,
		"repetitionPenalty":         1,
		"temperature":               0,
		"topP":                      1,
		"minPixels":                 147384,
		"maxPixels":                 2822400,
	}
}

// fileTypeCode maps a request to the PaddleOCR-VL fileType field:
// 0 = PDF, 1 = image (including TIFF).
func fileTypeCode(req *types.ReadRequest) int {
	ft := strings.ToLower(strings.TrimPrefix(req.FileType, "."))
	if ft == "" {
		ft = strings.TrimPrefix(strings.ToLower(filepath.Ext(req.FileName)), ".")
	}
	if ft == "pdf" {
		return 0
	}
	return 1
}

// paddleOCRVLResponse mirrors the relevant fields of the PaddleX serving
// /layout-parsing response. The service returns one entry per page.
type paddleOCRVLResponse struct {
	ErrorCode int    `json:"errorCode"`
	ErrorMsg  string `json:"errorMsg"`
	Result    struct {
		LayoutParsingResults []paddleOCRVLPage `json:"layoutParsingResults"`
	} `json:"result"`
}

func (c *PaddleOCRVLReader) callLayoutParsing(
	ctx context.Context, req *types.ReadRequest, content []byte,
) (string, map[string]string, []sourceloc.Unit, error) {
	payload := paddleOCRVLRecognitionParams(c.useSeal, c.useChart)
	payload["file"] = base64.StdEncoding.EncodeToString(content)
	payload["fileType"] = fileTypeCode(req)
	payload["visualize"] = false

	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, fmt.Errorf("marshal payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.endpoint+"/layout-parsing", bytes.NewReader(body),
	)
	if err != nil {
		return "", nil, nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      c.timeout,
		MaxRedirects: 5,
	})
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", nil, nil, fmt.Errorf("PaddleOCR-VL API status %d: %s", resp.StatusCode, string(respBody))
	}

	var result paddleOCRVLResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, nil, fmt.Errorf("decode response: %w", err)
	}
	if result.ErrorCode != 0 {
		return "", nil, nil, fmt.Errorf("PaddleOCR-VL error %d: %s", result.ErrorCode, result.ErrorMsg)
	}

	pages := result.Result.LayoutParsingResults
	if len(pages) == 0 {
		logger.Errorf(context.Background(), "[PaddleOCR-VL] response has no layoutParsingResults")
		return "", nil, nil, nil
	}

	// Merge per-page markdown and image dicts into one document.
	texts := make([]string, 0, len(pages))
	images := make(map[string]string)
	var units []sourceloc.Unit
	for i, p := range pages {
		units = append(units, paddleOCRVLPageUnits(i+1, p)...)
		if t := strings.TrimSpace(p.Markdown.Text); t != "" {
			texts = append(texts, p.Markdown.Text)
		}
		for path, data := range p.Markdown.Images {
			if _, ok := images[path]; !ok {
				images[path] = data
			}
		}
	}

	logger.Infof(context.Background(), "[PaddleOCR-VL] parsed %d page(s), images=%d", len(pages), len(images))
	return strings.Join(texts, "\n\n"), images, units, nil
}

// processImages decodes the inline base64 images returned by PaddleOCR-VL and
// builds ImageRef entries, matching them against references in the markdown.
func (c *PaddleOCRVLReader) processImages(
	mdContent string, imagesB64 map[string]string,
) ([]types.ImageRef, string) {
	var refs []types.ImageRef

	for ipath, b64Str := range imagesB64 {
		matchedRefs := mineruImageOriginalRefs(mdContent, ipath)
		if len(matchedRefs) == 0 {
			continue
		}

		var imgBytes []byte
		var ext string
		if m := b64DataURIPattern.FindStringSubmatch(b64Str); len(m) == 3 {
			ext = m[1]
			decoded, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil {
				logger.Errorf(context.Background(), "[PaddleOCR-VL] decode base64 image %s: %v", ipath, err)
				continue
			}
			imgBytes = decoded
		} else {
			decoded, err := base64.StdEncoding.DecodeString(b64Str)
			if err != nil {
				logger.Errorf(context.Background(), "[PaddleOCR-VL] decode raw base64 image %s: %v", ipath, err)
				continue
			}
			imgBytes = decoded
			ext = strings.TrimPrefix(filepath.Ext(ipath), ".")
			if ext == "" {
				ext = "png"
			}
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

	return refs, mdContent
}

// PingPaddleOCRVL checks whether a self-hosted PaddleOCR-VL service is reachable.
func PingPaddleOCRVL(endpoint string) (bool, string) {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return false, "未配置 PaddleOCR-VL 端点"
	}
	if err := utils.ValidateURLForSSRF(endpoint); err != nil {
		return false, fmt.Sprintf("PaddleOCR-VL 端点未通过 SSRF 校验: %v", err)
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      5 * time.Second,
		MaxRedirects: 5,
	})
	// The pipeline only exposes POST /layout-parsing; an empty GET should still
	// produce a routed HTTP response (e.g. 404/405) when the service is up.
	resp, err := client.Get(endpoint + "/layout-parsing")
	if err != nil {
		return false, fmt.Sprintf("PaddleOCR-VL 服务不可达: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return false, fmt.Sprintf("PaddleOCR-VL 服务返回状态 %d", resp.StatusCode)
	}
	return true, ""
}
