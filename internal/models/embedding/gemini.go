package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const geminiEmbeddingBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiEmbedder implements text vectorization using the native Gemini
// embedContent / batchEmbedContents REST API.
type GeminiEmbedder struct {
	apiKey               string
	baseURL              string
	modelName            string
	truncatePromptTokens int
	dimensions           int
	modelID              string
	httpClient           *http.Client
	timeout              time.Duration
	maxRetries           int
	customHeaders        map[string]string
	EmbedderPooler
}

type geminiBatchEmbedRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

type geminiEmbedRequest struct {
	Model                string        `json:"model"`
	Content              geminiContent `json:"content"`
	TaskType             string        `json:"taskType,omitempty"`
	OutputDimensionality int           `json:"output_dimensionality,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiBatchEmbedResponse struct {
	Embeddings []geminiEmbedding `json:"embeddings"`
}

type geminiEmbedding struct {
	Values []float32 `json:"values"`
}

func NewGeminiEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*GeminiEmbedder, error) {
	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}
	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}
	if baseURL == "" {
		baseURL = geminiEmbeddingBaseURL
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/openai") {
		baseURL = strings.TrimSuffix(baseURL, "/openai")
	}

	timeout := 60 * time.Second
	return &GeminiEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            strings.TrimPrefix(modelName, "models/"),
		truncatePromptTokens: truncatePromptTokens,
		dimensions:           dimensions,
		modelID:              modelID,
		httpClient:           &http.Client{Timeout: timeout},
		timeout:              timeout,
		maxRetries:           3,
		EmbedderPooler:       pooler,
	}, nil
}

func (e *GeminiEmbedder) SetCustomHeaders(headers map[string]string) {
	e.customHeaders = headers
}

func (e *GeminiEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	embeddings, err := e.BatchEmbed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], nil
}

func (e *GeminiEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	requests := make([]geminiEmbedRequest, 0, len(texts))
	for _, text := range texts {
		requests = append(requests, geminiEmbedRequest{
			Model: "models/" + e.modelName,
			Content: geminiContent{Parts: []geminiPart{
				{Text: text},
			}},
			OutputDimensionality: e.dimensions,
		})
	}

	jsonData, err := json.Marshal(geminiBatchEmbedRequest{Requests: requests})
	if err != nil {
		logger.GetLogger(ctx).Errorf("GeminiEmbedder BatchEmbed marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logger.GetLogger(ctx).Debugf("GeminiEmbedder BatchEmbed: model=%s, input_count=%d",
		e.modelName, len(texts))

	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("GeminiEmbedder BatchEmbed send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("GeminiEmbedder BatchEmbed read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 1000 {
			bodyStr = bodyStr[:1000] + "... (truncated)"
		}
		logger.GetLogger(ctx).Errorf("GeminiEmbedder BatchEmbed API error: Http Status %s, Response Body: %s", resp.Status, bodyStr)
		return nil, fmt.Errorf("Gemini BatchEmbed API error: Http Status %s, Response: %s", resp.Status, bodyStr)
	}

	var response geminiBatchEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("GeminiEmbedder BatchEmbed unmarshal response error: %v", err)
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if len(response.Embeddings) != len(texts) {
		return nil, fmt.Errorf("Gemini BatchEmbed returned %d embeddings for %d inputs", len(response.Embeddings), len(texts))
	}

	embeddings := make([][]float32, 0, len(response.Embeddings))
	for _, embedding := range response.Embeddings {
		embeddings = append(embeddings, embedding.Values)
	}
	return embeddings, nil
}

func (e *GeminiEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	url := fmt.Sprintf("%s/models/%s:batchEmbedContents", e.baseURL, e.modelName)

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).
				Infof("GeminiEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("GeminiEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-goog-api-key", e.apiKey)
		secutils.ApplyCustomHeaders(req, e.customHeaders)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("GeminiEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *GeminiEmbedder) GetModelName() string {
	return e.modelName
}

func (e *GeminiEmbedder) GetDimensions() int {
	return e.dimensions
}

func (e *GeminiEmbedder) GetModelID() string {
	return e.modelID
}
