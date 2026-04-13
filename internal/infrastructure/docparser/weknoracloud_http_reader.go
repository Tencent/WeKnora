package docparser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// WeKnoraCloudSignedDocumentReader implements the docreader HTTP protocol with WeKnoraCloud signing.
type WeKnoraCloudSignedDocumentReader struct {
	baseURL             string
	appID               string
	apiKey              string
	client              *http.Client
	initialPollInterval time.Duration
	maxPollInterval     time.Duration
	pollTimeout         time.Duration
}

func NewWeKnoraCloudSignedDocumentReader(baseURL, appID, apiKey string) (*WeKnoraCloudSignedDocumentReader, error) {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("docreader base URL is required")
	}
	if appID == "" {
		return nil, fmt.Errorf("WeKnoraCloud appID is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("WeKnoraCloud apiKey is required")
	}
	return &WeKnoraCloudSignedDocumentReader{
		baseURL:             baseURL,
		appID:               appID,
		apiKey:              apiKey,
		initialPollInterval: 500 * time.Millisecond,
		maxPollInterval:     10 * time.Second,
		pollTimeout:         20 * time.Minute,
		client: &http.Client{
			Timeout: 500 * time.Minute,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     90 * time.Second,
				MaxIdleConnsPerHost: 5,
			},
		},
	}, nil
}

func (p *WeKnoraCloudSignedDocumentReader) Reconnect(addr string) error {
	p.baseURL = strings.TrimSuffix(strings.TrimSpace(addr), "/")
	return nil
}

func (p *WeKnoraCloudSignedDocumentReader) IsConnected() bool { return p.baseURL != "" }

func (p *WeKnoraCloudSignedDocumentReader) ListEngines(ctx context.Context, overrides map[string]string) ([]types.ParserEngineInfo, error) {
	if !p.IsConnected() {
		return nil, errNotConnected
	}
	return []types.ParserEngineInfo{{
		Name:        WeKnoraCloudEngineName,
		Description: "WeKnoraCloud signed docreader",
		FileTypes:   weKnoraCloudSupportedFileTypes(),
		Available:   true,
	}}, nil
}

func (p *WeKnoraCloudSignedDocumentReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if !p.IsConnected() {
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
		},
	}
	if len(req.FileContent) > 0 {
		body.FileContent = base64.StdEncoding.EncodeToString(req.FileContent)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("http marshal read request: %w", err)
	}
	httpReq, err := p.newSignedRequest(ctx, http.MethodPost, p.baseURL, jsonBody)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http read failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("http read status %d: %s", resp.StatusCode, string(bodyBytes))
	}
	var submit weKnoraCloudAsyncSubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submit); err != nil {
		return nil, fmt.Errorf("http decode read submit response: %w", err)
	}
	if strings.TrimSpace(submit.TaskID) == "" {
		return nil, fmt.Errorf("weknoracloud docreader submit response missing task_id")
	}
	return p.pollTaskResult(ctx, submit.TaskID)
}

type weKnoraCloudAsyncSubmitResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	CreatedAt int64  `json:"created_at"`
}

type weKnoraCloudAsyncTaskResponse struct {
	TaskID    string            `json:"task_id"`
	Status    string            `json:"status"`
	Message   string            `json:"message"`
	Progress  float64           `json:"progress"`
	Result    *httpReadResponse `json:"result"`
	Error     string            `json:"error"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

func weKnoraCloudSupportedFileTypes() []string {
	seen := map[string]struct{}{}
	var fileTypes []string
	for _, engine := range localEngines {
		if engine.Name() == WeKnoraCloudEngineName {
			continue
		}
		for _, fileType := range engine.FileTypes(true) {
			if _, ok := seen[fileType]; ok {
				continue
			}
			seen[fileType] = struct{}{}
			fileTypes = append(fileTypes, fileType)
		}
	}
	sort.Strings(fileTypes)
	return fileTypes
}

func (p *WeKnoraCloudSignedDocumentReader) pollTaskResult(ctx context.Context, taskID string) (*types.ReadResult, error) {
	pollCtx := ctx
	if _, ok := ctx.Deadline(); !ok && p.pollTimeout > 0 {
		var cancel context.CancelFunc
		pollCtx, cancel = context.WithTimeout(ctx, p.pollTimeout)
		defer cancel()
	}
	statusURL := strings.TrimSuffix(p.baseURL, "/reader") + "/" + taskID
	currentInterval := p.initialPollInterval
	for {
		httpReq, err := p.newSignedRequest(pollCtx, http.MethodGet, statusURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := p.client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("http poll task failed: %w", err)
		}
		var taskResp weKnoraCloudAsyncTaskResponse
		func() {
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				bodyBytes, _ := io.ReadAll(resp.Body)
				err = fmt.Errorf("http poll task status %d: %s", resp.StatusCode, string(bodyBytes))
				return
			}
			if decodeErr := json.NewDecoder(resp.Body).Decode(&taskResp); decodeErr != nil {
				err = fmt.Errorf("http decode task response: %w", decodeErr)
			}
		}()
		if err != nil {
			return nil, err
		}
		switch taskResp.Status {
		case "completed":
			if taskResp.Result == nil {
				return &types.ReadResult{}, nil
			}
			return fromHTTPReadResponse(taskResp.Result), nil
		case "failed":
			if taskResp.Error != "" {
				return nil, fmt.Errorf("weknoracloud docreader task failed: %s", taskResp.Error)
			}
			return nil, fmt.Errorf("weknoracloud docreader task failed: %s", taskResp.Message)
		case "cancelled":
			if taskResp.Error != "" {
				return nil, fmt.Errorf("weknoracloud docreader task cancelled: %s", taskResp.Error)
			}
			return nil, fmt.Errorf("weknoracloud docreader task cancelled")
		}
		if err := pollCtx.Err(); err != nil {
			return nil, err
		}

		// Exponential backoff: multiply by 1.5 each time, cap at maxPollInterval
		select {
		case <-pollCtx.Done():
			return nil, pollCtx.Err()
		case <-time.After(currentInterval):
			// Update interval for next iteration
			nextInterval := time.Duration(float64(currentInterval) * 1.5)
			if nextInterval > p.maxPollInterval {
				nextInterval = p.maxPollInterval
			}
			currentInterval = nextInterval
		}
	}
}

func (p *WeKnoraCloudSignedDocumentReader) newSignedRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	requestID := uuid.New().String()
	if len(body) == 0 {
		body = []byte("{}")
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("http new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.ContentLength = int64(len(body))
	for k, v := range utils.Sign(p.appID, p.apiKey, requestID, string(body)) {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}
