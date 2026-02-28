package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/Tencent/WeKnora/internal/types"
)

const mineruTimeout = 1000 * time.Second // large docs can take a while

var b64DataURIPattern = regexp.MustCompile(`^data:image/(\w+);base64,(.+)$`)

// MinerUReader calls a self-hosted MinerU API to read/convert documents.
type MinerUReader struct {
	endpoint      string
	backend       string // "pipeline", "vlm-*", "hybrid-*"
	formulaEnable bool
	tableEnable   bool
	ocrEnable     bool
	language      string
}

// NewMinerUReader creates a reader from ParserEngineOverrides.
func NewMinerUReader(overrides map[string]string) *MinerUReader {
	c := &MinerUReader{
		endpoint:      strings.TrimRight(overrides["mineru_endpoint"], "/"),
		backend:       stringOr(overrides["mineru_model"], "pipeline"),
		formulaEnable: parseBoolOr(overrides["mineru_enable_formula"], true),
		tableEnable:   parseBoolOr(overrides["mineru_enable_table"], true),
		ocrEnable:     parseBoolOr(overrides["mineru_enable_ocr"], true),
		language:      stringOr(overrides["mineru_language"], "ch"),
	}
	return c
}

func (c *MinerUReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if c.endpoint == "" {
		return &types.ReadResult{Error: "MinerU endpoint is not configured"}, nil
	}

	content := req.FileContent
	if len(content) == 0 {
		return &types.ReadResult{Error: "no file content provided"}, nil
	}

	logger.Printf("INFO: [MinerU] Parsing file=%s size=%d via %s", req.FileName, len(content), c.endpoint)

	mdContent, imagesB64, err := c.callFileParse(ctx, content)
	if err != nil {
		return nil, fmt.Errorf("MinerU file_parse: %w", err)
	}

	// HTML -> Markdown conversion (equivalent to Python markdownify)
	mdContent = htmlToMarkdown(mdContent)

	// Process images: decode base64, build ImageRef list, replace refs in markdown
	imageRefs, mdContent := c.processImages(mdContent, imagesB64)

	mdContent, imageRefs = ensureOriginalImageRef(req, mdContent, imageRefs)

	logger.Printf("INFO: [MinerU] Parsed successfully, markdown=%d chars, images=%d", len(mdContent), len(imageRefs))

	return &types.ReadResult{
		MarkdownContent: mdContent,
		ImageRefs:       imageRefs,
	}, nil
}

// mineruFileParseResponse mirrors the relevant fields from the MinerU API response.
type mineruFileParseResponse struct {
	Results struct {
		Files struct {
			MDContent string            `json:"md_content"`
			Images    map[string]string `json:"images"` // path -> "data:image/png;base64,..." or raw base64
		} `json:"files"`
	} `json:"results"`
}

func (c *MinerUReader) callFileParse(ctx context.Context, content []byte) (string, map[string]string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// Form fields
	fields := map[string]string{
		"return_md":           "true",
		"return_images":       "true",
		"table_enable":        fmt.Sprintf("%v", c.tableEnable),
		"formula_enable":      fmt.Sprintf("%v", c.formulaEnable),
		"parse_method":        "ocr",
		"start_page_id":       "0",
		"end_page_id":         "99999",
		"backend":             c.backend,
		"response_format_zip": "false",
		"return_middle_json":  "false",
		"return_model_output": "false",
		"return_content_list": "true",
	}
	if !c.ocrEnable {
		fields["parse_method"] = "txt"
	}
	if c.language != "" {
		fields["lang_list"] = c.language
	}
	for k, v := range fields {
		_ = writer.WriteField(k, v)
	}

	// File part
	part, err := writer.CreateFormFile("files", "document")
	if err != nil {
		return "", nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(content); err != nil {
		return "", nil, fmt.Errorf("write file content: %w", err)
	}
	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/file_parse", &body)
	if err != nil {
		return "", nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: mineruTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", nil, fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("MinerU API status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("read response body: %w", err)
	}

	// Dump raw response for debugging (truncate if too large)
	rawStr := string(respBody)
	if len(rawStr) > 4000 {
		logger.Printf("DEBUG: [MinerU] Raw response (truncated to 4000 chars): %s ...", rawStr[:4000])
	} else {
		logger.Printf("DEBUG: [MinerU] Raw response: %s", rawStr)
	}

	// Also pretty-print the top-level structure (without large base64 blobs)
	var rawMap map[string]interface{}
	if err := json.Unmarshal(respBody, &rawMap); err == nil {
		c.logMinerUResponseStructure(rawMap, "")
	}

	var result mineruFileParseResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", nil, fmt.Errorf("decode response: %w", err)
	}

	return result.Results.Files.MDContent, result.Results.Files.Images, nil
}

// processImages decodes base64 images from MinerU response and returns ImageRef list.
// It also replaces image references in the markdown content.
func (c *MinerUReader) processImages(mdContent string, imagesB64 map[string]string) ([]types.ImageRef, string) {
	var refs []types.ImageRef

	for ipath, b64Str := range imagesB64 {
		originalRef := "images/" + ipath
		if !strings.Contains(mdContent, originalRef) {
			continue
		}

		var imgBytes []byte
		var ext string

		if m := b64DataURIPattern.FindStringSubmatch(b64Str); len(m) == 3 {
			ext = m[1]
			decoded, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil {
				logger.Printf("WARN: [MinerU] Failed to decode base64 image %s: %v", ipath, err)
				continue
			}
			imgBytes = decoded
		} else {
			// raw base64 without data URI prefix
			decoded, err := base64.StdEncoding.DecodeString(b64Str)
			if err != nil {
				logger.Printf("WARN: [MinerU] Failed to decode raw base64 image %s: %v", ipath, err)
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

		refs = append(refs, types.ImageRef{
			Filename:    ipath,
			OriginalRef: originalRef,
			MimeType:    mimeType,
			ImageData:   imgBytes,
		})
	}

	return refs, mdContent
}

// logMinerUResponseStructure logs the structure of the MinerU API response.
func (c *MinerUReader) logMinerUResponseStructure(obj interface{}, prefix string) {
	logResponseStructure("MinerU", obj, prefix)
}

// PingMinerU checks if the self-hosted MinerU service is reachable.
func PingMinerU(endpoint string) (bool, string) {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return false, "未配置 MinerU 端点"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(endpoint + "/docs")
	if err != nil {
		return false, fmt.Sprintf("MinerU 服务不可达: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("MinerU 服务返回状态 %d", resp.StatusCode)
	}
	return true, ""
}

// htmlToMarkdown converts HTML content to markdown.
// Falls back to the original content if conversion fails.
func htmlToMarkdown(content string) string {
	md, err := htmltomd.ConvertString(content)
	if err != nil {
		logger.Printf("WARN: [MinerU] html-to-markdown conversion failed, using raw content: %v", err)
		return content
	}
	return md
}
