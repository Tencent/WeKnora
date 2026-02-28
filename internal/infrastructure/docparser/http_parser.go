package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	PathRead        = "/read"
	PathListEngines = "/list-engines"
)

// --- JSON DTOs ---

type httpReadConfig struct {
	ParserEngine          string            `json:"parser_engine,omitempty"`
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides,omitempty"`
	ImageStorage          map[string]string `json:"image_storage,omitempty"`
}

type httpReadRequest struct {
	FileContent string          `json:"file_content,omitempty"` // base64
	FileName    string          `json:"file_name,omitempty"`
	FileType    string          `json:"file_type,omitempty"`
	URL         string          `json:"url,omitempty"`
	Title       string          `json:"title,omitempty"`
	Config      *httpReadConfig `json:"config,omitempty"`
	RequestID   string          `json:"request_id,omitempty"`
}

type httpImageRef struct {
	Filename    string `json:"filename"`
	OriginalRef string `json:"original_ref"`
	MimeType    string `json:"mime_type"`
	StorageKey  string `json:"storage_key,omitempty"`
	ImageData   []byte `json:"image_data,omitempty"`
}

type httpReadResponse struct {
	MarkdownContent string            `json:"markdown_content"`
	ImageRefs       []httpImageRef    `json:"image_refs,omitempty"`
	ImageDirPath    string            `json:"image_dir_path,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Error           string            `json:"error,omitempty"`
}

// HTTPDocumentReader implements DocumentReader over HTTP/JSON.
type HTTPDocumentReader struct {
	mu      sync.RWMutex
	baseURL string
	client  *http.Client
}

func NewHTTPDocumentReader(baseURL string) (*HTTPDocumentReader, error) {
	p := &HTTPDocumentReader{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client: &http.Client{
			Timeout: 5 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 5,
			},
		},
	}
	if p.baseURL != "" {
		logger.Printf("INFO: HTTP docreader base URL: %s", p.baseURL)
	}
	return p, nil
}

func (p *HTTPDocumentReader) base() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.baseURL
}

func (p *HTTPDocumentReader) Reconnect(addr string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.baseURL = strings.TrimSuffix(addr, "/")
	logger.Printf("INFO: HTTP docreader base URL set to %s", p.baseURL)
	return nil
}

func (p *HTTPDocumentReader) IsConnected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.baseURL != ""
}

func (p *HTTPDocumentReader) Close() error { return nil }

type httpListEnginesRequest struct {
	ConfigOverrides map[string]string `json:"config_overrides,omitempty"`
}

type httpParserEngineInfo struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	FileTypes         []string `json:"file_types"`
	Available         bool     `json:"available"`
	UnavailableReason string   `json:"unavailable_reason,omitempty"`
}

type httpListEnginesResponse struct {
	Engines []httpParserEngineInfo `json:"engines"`
}

func (p *HTTPDocumentReader) ListEngines(ctx context.Context, overrides map[string]string) ([]types.ParserEngineInfo, error) {
	base := p.base()
	if base == "" {
		return nil, errNotConnected
	}

	body := httpListEnginesRequest{ConfigOverrides: overrides}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("http marshal list-engines request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PathListEngines, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("http new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http list-engines failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http list-engines status %d: %s", resp.StatusCode, string(respBytes))
	}

	var out httpListEnginesResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("http decode list-engines response: %w", err)
	}

	result := make([]types.ParserEngineInfo, 0, len(out.Engines))
	for _, e := range out.Engines {
		result = append(result, types.ParserEngineInfo{
			Name:              e.Name,
			Description:       e.Description,
			FileTypes:         e.FileTypes,
			Available:         e.Available,
			UnavailableReason: e.UnavailableReason,
		})
	}
	return result, nil
}

func fromHTTPReadResponse(resp *httpReadResponse) *types.ReadResult {
	result := &types.ReadResult{
		MarkdownContent: resp.MarkdownContent,
		ImageDirPath:    resp.ImageDirPath,
		Metadata:        resp.Metadata,
		Error:           resp.Error,
	}
	for _, ref := range resp.ImageRefs {
		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			Filename:    ref.Filename,
			OriginalRef: ref.OriginalRef,
			MimeType:    ref.MimeType,
			StorageKey:  ref.StorageKey,
			ImageData:   ref.ImageData,
		})
	}
	return result
}

func (p *HTTPDocumentReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	base := p.base()
	if base == "" {
		return nil, errNotConnected
	}

	body := httpReadRequest{
		FileName:  req.FileName,
		FileType:  req.FileType,
		URL:       req.URL,
		Title:     req.Title,
		RequestID: req.RequestID,
		Config: &httpReadConfig{
			ParserEngine:          req.ParserEngine,
			ParserEngineOverrides: req.ParserEngineOverrides,
			ImageStorage:          req.ImageStorage,
		},
	}
	if len(req.FileContent) > 0 {
		body.FileContent = base64.StdEncoding.EncodeToString(req.FileContent)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("http marshal read request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PathRead, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("http new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.ContentLength = int64(len(jsonBody))

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http read failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http read status %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var out httpReadResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("http decode read response: %w", err)
	}
	return fromHTTPReadResponse(&out), nil
}
