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
)

// OpenAIEmbedder implements text vectorization functionality using OpenAI API
type OpenAIEmbedder struct {
	apiKey               string
	baseURL              string
	modelName            string
	truncatePromptTokens int
	dimensions           int
	modelID              string
	httpClient           *http.Client
	timeout              time.Duration
	maxRetries           int
	EmbedderPooler
}

// OpenAIEmbedRequest represents an OpenAI embedding request
type OpenAIEmbedRequest struct {
	Model                string   `json:"model"`
	Input                []string `json:"input"`
	EncodingFormat       string   `json:"encoding_format,omitempty"`
	TruncatePromptTokens int      `json:"truncate_prompt_tokens,omitempty"`
}

// OpenAIEmbedResponse represents an OpenAI embedding response
type OpenAIEmbedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
}

// NewOpenAIEmbedder creates a new OpenAI embedder
func NewOpenAIEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string, pooler EmbedderPooler,
) (*OpenAIEmbedder, error) {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	if modelName == "" {
		return nil, fmt.Errorf("model name is required")
	}

	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}

	timeout := 60 * time.Second

	// Create HTTP client
	client := &http.Client{
		Timeout: timeout,
	}

	return &OpenAIEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            modelName,
		httpClient:           client,
		truncatePromptTokens: truncatePromptTokens,
		EmbedderPooler:       pooler,
		dimensions:           dimensions,
		modelID:              modelID,
		timeout:              timeout,
		maxRetries:           3, // Maximum retry count
	}, nil
}

// Embed converts text to vector
func (e *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
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

func (e *OpenAIEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
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
				Infof("OpenAIEmbedder retrying request (%d/%d), waiting %v", i, e.maxRetries, backoffTime)

			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Rebuild request each time to ensure Body is valid
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if err != nil {
			logger.GetLogger(ctx).Errorf("OpenAIEmbedder failed to create request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+e.apiKey)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}

		logger.GetLogger(ctx).Errorf("OpenAIEmbedder request failed (attempt %d/%d): %v", i+1, e.maxRetries+1, err)
	}

	return nil, err
}

func (e *OpenAIEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	// Create request body
	reqBody := OpenAIEmbedRequest{
		Model:                e.modelName,
		Input:                texts,
		EncodingFormat:       "float",
		TruncatePromptTokens: e.truncatePromptTokens,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		logger.GetLogger(ctx).Errorf("OpenAIEmbedder EmbedBatch marshal request error: %v", err)
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// Send request (passing jsonData instead of constructing http.Request)
	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		logger.GetLogger(ctx).Errorf("OpenAIEmbedder EmbedBatch send request error: %v", err)
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.GetLogger(ctx).Errorf("OpenAIEmbedder EmbedBatch read response error: %v", err)
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.GetLogger(ctx).Errorf("OpenAIEmbedder EmbedBatch API error: Http Status %s", resp.Status)
		return nil, fmt.Errorf("EmbedBatch API error: Http Status %s", resp.Status)
	}

	// Parse response
	var response OpenAIEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		logger.GetLogger(ctx).Errorf("OpenAIEmbedder EmbedBatch unmarshal response error: %v", err)
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
func (e *OpenAIEmbedder) GetModelName() string {
	return e.modelName
}

// GetDimensions returns the vector dimensions
func (e *OpenAIEmbedder) GetDimensions() int {
	return e.dimensions
}

// GetModelID returns the model ID
func (e *OpenAIEmbedder) GetModelID() string {
	return e.modelID
}
