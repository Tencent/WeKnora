package embedding

import (
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
	apiKey                    string
	baseURL                   string
	modelName                 string
	truncatePromptTokens      int
	dimensions                int
	modelID                   string
	httpClient                *http.Client
	timeout                   time.Duration
	customHeaders             map[string]string
	supportsDimensionOverride bool
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

	if err := validateEmbeddingBaseURL(baseURL); err != nil {
		return nil, err
	}

	return &GeminiEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            strings.TrimPrefix(modelName, "models/"),
		truncatePromptTokens: truncatePromptTokens,
		dimensions:           dimensions,
		modelID:              modelID,
		httpClient:           newEmbeddingHTTPClient(timeout),
		timeout:              timeout,
		EmbedderPooler:       pooler,
	}, nil
}

func (e *GeminiEmbedder) SetCustomHeaders(headers map[string]string) {
	e.customHeaders = headers
}

func (e *GeminiEmbedder) SetSupportsDimensionOverride(supported bool) {
	e.supportsDimensionOverride = supported
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
		req := geminiEmbedRequest{
			Model: "models/" + e.modelName,
			Content: geminiContent{Parts: []geminiPart{
				{Text: text},
			}},
		}
		if e.supportsDimensionOverride && e.dimensions > 0 {
			req.OutputDimensionality = e.dimensions
		}
		requests = append(requests, req)
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
		return nil, embedHTTPError(resp.StatusCode,
			fmt.Sprintf("Gemini BatchEmbed API error: Http Status %s, Response: %s", resp.Status, bodyStr))
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

// doRequestWithRetry sends the request under the shared retry policy in
// retry_http.go (429/5xx + Retry-After + exponential backoff).
func (e *GeminiEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/models/%s:batchEmbedContents", e.baseURL, e.modelName)
	return retryEmbeddingRequest(ctx, e.httpClient, http.MethodPost, url, jsonData,
		func(req *http.Request) {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("x-goog-api-key", e.apiKey)
			secutils.ApplyCustomHeaders(req, e.customHeaders)
		})
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
