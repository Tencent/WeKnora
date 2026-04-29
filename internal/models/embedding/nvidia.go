package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// NvidiaEmbedder implements text vectorization functionality using NVIDIA API
type NvidiaEmbedder struct {
	apiKey        string
	baseURL       string
	modelName     string
	dimensions    int
	modelID       string
	httpClient    *http.Client
	timeout       time.Duration
	maxRetries    int
	customHeaders map[string]string
	EmbedderPooler
}

// SetCustomHeaders 设置用户自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
func (e *NvidiaEmbedder) SetCustomHeaders(headers map[string]string) {
	e.customHeaders = headers
}

// NvidiaEmbedRequest represents an NVIDIA embedding request
type NvidiaEmbedRequest struct {
	Model                string   `json:"model"`
	Input                []string `json:"input"`
	EncodingFormat       string   `json:"encoding_format,omitempty"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
	InputType            string   `json:"input_type,omitempty"`
}

// NvidiaEmbedResponse represents an NVIDIA embedding response
type NvidiaEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// NewNvidiaEmbedder creates a new NVIDIA embedder
func NewNvidiaEmbedder(apiKey, baseURL, modelName string,
	dimensions int, modelID string, pooler EmbedderPooler,
) (*NvidiaEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://integrate.api.nvidia.com/v1"
	}

	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	timeout := 60 * time.Second

	// Create HTTP client
	client := &http.Client{
		Timeout: timeout,
	}

	return &NvidiaEmbedder{
		apiKey:         apiKey,
		baseURL:        baseURL,
		modelName:      modelName,
		httpClient:     client,
		EmbedderPooler: pooler,
		dimensions:     dimensions,
		modelID:        modelID,
		timeout:        timeout,
		maxRetries:     3, // Maximum retry count
	}, nil
}

// Embed converts text to vector
func (e *NvidiaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	for range 3 {
		embeddings, err := e.BatchEmbed(ctx, []string{text})
		if err != nil {
			return nil, err
		}
		if len(embeddings) > 0 {
			return embeddings[0], nil
		}
	}
	return nil, fmt.Errorf("no embedding returned")
}

func (e *NvidiaEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	url := e.baseURL + "/embeddings"

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			logger.GetLogger(ctx).
				Infof("NvidiaEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Rebuild request each time to ensure Body is valid
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("NvidiaEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
		secutils.ApplyCustomHeaders(req, e.customHeaders)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("NvidiaEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *NvidiaEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Create request body
	reqBody := NvidiaEmbedRequest{
		Model:          e.modelName,
		Input:          texts,
		EncodingFormat: "float",
		InputType:      "passage",
	}
	isQuery, _ := ctx.Value(types.EmbedQueryContextKey).(bool)
	if isQuery {
		reqBody.InputType = "query"
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logger.GetLogger(ctx).Errorf("NvidiaEmbedder EmbedBatch marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Send request (passing jsonData instead of constructing http.Request)
	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("NvidiaEmbedder EmbedBatch send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("NvidiaEmbedder EmbedBatch read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.GetLogger(ctx).Errorf("NvidiaEmbedder EmbedBatch API error: Http Status %s", resp.Status)
		return nil, fmt.Errorf("EmbedBatch API error: Http Status %s", resp.Status)
	}

	// Parse response
	var response NvidiaEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("NvidiaEmbedder EmbedBatch unmarshal response error: %v", err)
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Extract embedding vectors
	embeddings := make([][]float32, 0, len(response.Data))
	for _, data := range response.Data {
		embeddings = append(embeddings, data.Embedding)
	}

	return embeddings, nil
}

// GetModelName returns the model name
func (e *NvidiaEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *NvidiaEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *NvidiaEmbedder) GetModelID() string {
	return e.modelID
}
