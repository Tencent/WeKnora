package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// NvidiaReranker implements a reranking system using Jina AI API
// Jina API uses different parameters than standard OpenAI-compatible APIs
type NvidiaReranker struct {
	modelName     string       // Name of the model used for reranking
	modelID       string       // Unique identifier of the model
	apiKey        string       // API key for authentication
	baseURL       string       // Base URL for API requests
	client        *http.Client // HTTP client for making API requests
	customHeaders map[string]string
}

// SetCustomHeaders 设置用户自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
func (r *NvidiaReranker) SetCustomHeaders(headers map[string]string) {
	r.customHeaders = headers
}
type NvidiaRerankDocument struct {
	Text string `json:"text"`
}

// NvidiaRerankRequest represents a Jina rerank request
// Note: Jina does NOT support truncate_prompt_tokens parameter
type NvidiaRerankRequest struct {
	Model     string                 `json:"model"`    // Model to use for reranking
	Query     NvidiaRerankDocument   `json:"query"`    // Query text to compare documents against
	Documents []NvidiaRerankDocument `json:"passages"` // List of document texts to rerank
}

type NvidiaRankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"logit"`
}

// NvidiaRerankResponse represents the response from a Jina reranking request
type NvidiaRerankResponse struct {
	Model   string             `json:"model"`    // Model used for reranking
	Results []NvidiaRankResult `json:"rankings"` // Ranked results with relevance scores
}

// NewNvidiaReranker creates a new instance of Jina reranker with the provided configuration
func NewNvidiaReranker(config *RerankerConfig) (*NvidiaReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	return &NvidiaReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
			},
		},
	}, nil
}

// Rerank performs document reranking based on relevance to the query
func (r *NvidiaReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body - Jina does NOT use truncate_prompt_tokens
	requestBody := &NvidiaRerankRequest{
		Model:     r.modelName,
		Query:     NvidiaRerankDocument{Text: query},
		Documents: make([]NvidiaRerankDocument, len(documents)),
	}
	for i := range requestBody.Documents {
		requestBody.Documents[i].Text = documents[i]
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	// Send the request
	req, err := http.NewRequestWithContext(ctx, "POST", r.baseURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.apiKey))
	secutils.ApplyCustomHeaders(req, r.customHeaders)

	// Log the curl equivalent for debugging (API key masked for security)
	logger.GetLogger(ctx).Infof(
		"curl -X POST %s/rerank -H \"Content-Type: application/json\" -H \"Authorization: Bearer ***\" -d '%s'",
		r.baseURL, string(jsonData),
	)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		logger.GetLogger(ctx).Errorf("JinaReranker API error: Http Status: %s, Body: %s", resp.Status, string(body))
		return nil, fmt.Errorf("Rerank API error: Http Status: %s", resp.Status)
	}

	var response NvidiaRerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	ret := make([]RankResult, len(response.Results))
	for i, result := range response.Results {
		ret[i] = RankResult{
			Index:          result.Index,
			Document:       DocumentInfo{Text: documents[result.Index]},
			RelevanceScore: result.RelevanceScore,
		}
	}
	return ret, nil
}

// GetModelName returns the name of the reranking model
func (r *NvidiaReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *NvidiaReranker) GetModelID() string {
	return r.modelID
}
