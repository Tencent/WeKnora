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
)

// AzureOpenAIEmbedder implements text vectorization using Azure OpenAI API
type AzureOpenAIEmbedder struct {
	apiKey               string
	baseURL              string
	modelName            string
	truncatePromptTokens int
	dimensions           int
	modelID              string
	apiVersion           string
	httpClient           *http.Client
	maxRetries           int
	EmbedderPooler
}

type azureOpenAIEmbedRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	EncodingFormat string   `json:"encoding_format,omitempty"`
	Dimensions     int      `json:"dimensions,omitempty"`
}

// NewAzureOpenAIEmbedder creates a new Azure OpenAI embedder
func NewAzureOpenAIEmbedder(apiKey, baseURL, modelName string,
	truncatePromptTokens int, dimensions int, modelID string,
	apiVersion string, pooler EmbedderPooler,
) (*AzureOpenAIEmbedder, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("Azure resource endpoint (base URL) is required")
	}
	if modelName == "" {
		return nil, fmt.Errorf("deployment name (model name) is required")
	}
	if apiVersion == "" {
		apiVersion = "2024-10-21"
	}
	if truncatePromptTokens == 0 {
		truncatePromptTokens = 511
	}

	return &AzureOpenAIEmbedder{
		apiKey:               apiKey,
		baseURL:              baseURL,
		modelName:            modelName,
		truncatePromptTokens: truncatePromptTokens,
		dimensions:           dimensions,
		modelID:              modelID,
		apiVersion:           apiVersion,
		httpClient:           &http.Client{Timeout: 60 * time.Second},
		maxRetries:           3,
		EmbedderPooler:       pooler,
	}, nil
}

func (e *AzureOpenAIEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
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

func (e *AzureOpenAIEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := azureOpenAIEmbedRequest{
		Model:          e.modelName,
		Input:          texts,
		EncodingFormat: "float",
	}
	if e.supportsDimensionsParam() {
		reqBody.Dimensions = e.dimensions
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	logger.GetLogger(ctx).Debugf("AzureOpenAIEmbedder BatchEmbed: model=%s, input_count=%d",
		e.modelName, len(texts))

	resp, err := e.doRequestWithRetry(ctx, jsonData)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		if len(bodyStr) > 1000 {
			bodyStr = bodyStr[:1000] + "... (truncated)"
		}
		return nil, fmt.Errorf("Azure Embedding API error: Http Status %s, Response: %s", resp.Status, bodyStr)
	}

	var response OpenAIEmbedResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	embeddings := make([][]float32, 0, len(response.Data))
	for _, data := range response.Data {
		embeddings = append(embeddings, data.Embedding)
	}
	return embeddings, nil
}

func (e *AzureOpenAIEmbedder) doRequestWithRetry(ctx context.Context, jsonData []byte) (*http.Response, error) {
	url := fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s",
		e.baseURL, e.modelName, e.apiVersion)

	var resp *http.Response
	var err error

	for i := 0; i <= e.maxRetries; i++ {
		if i > 0 {
			backoffTime := time.Duration(1<<uint(i-1)) * time.Second
			if backoffTime > 10*time.Second {
				backoffTime = 10 * time.Second
			}
			select {
			case <-time.After(backoffTime):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		req, reqErr := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonData))
		if reqErr != nil {
			err = reqErr
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("api-key", e.apiKey)

		resp, err = e.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}
	}
	return nil, err
}

func (e *AzureOpenAIEmbedder) supportsDimensionsParam() bool {
	if e.dimensions <= 0 {
		return false
	}

	// Azure only supports the dimensions parameter on newer embeddings APIs.
	if strings.TrimSpace(e.apiVersion) < "2024-10-21" {
		return false
	}

	modelRef := strings.ToLower(strings.TrimSpace(e.modelID))
	if modelRef == "" {
		modelRef = strings.ToLower(strings.TrimSpace(e.modelName))
	}

	// Fixed-dimension legacy models reject the field entirely.
	if strings.Contains(modelRef, "ada-002") {
		return false
	}

	return strings.Contains(modelRef, "text-embedding-3-small") ||
		strings.Contains(modelRef, "text-embedding-3-large")
}

func (e *AzureOpenAIEmbedder) GetModelName() string { return e.modelName }
func (e *AzureOpenAIEmbedder) GetDimensions() int   { return e.dimensions }
func (e *AzureOpenAIEmbedder) GetModelID() string   { return e.modelID }
