package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Tencent/WeKnora/internal/logger"
)

// JinaReranker implements a reranking system using Jina AI API
// Jina API uses different parameters than standard OpenAI-compatible APIs
type JinaReranker struct {
	modelName string       // Name of the model used for reranking
	modelID   string       // Unique identifier of the model
	apiKey    string       // API key for authentication
	baseURL   string       // Base URL for API requests
	client    *http.Client // HTTP client for making API requests
}

// JinaRerankRequest represents a Jina rerank request
// Note: Jina does NOT support truncate_prompt_tokens parameter
type JinaRerankRequest struct {
	Model           string   `json:"model"`                      // Model to use for reranking
	Query           string   `json:"query"`                      // Query text to compare documents against
	Documents       []string `json:"documents"`                  // List of document texts to rerank
	TopN            int      `json:"top_n,omitempty"`            // Number of top results to return
	ReturnDocuments bool     `json:"return_documents,omitempty"` // Whether to return document text in response
}

// JinaRerankResponse represents the response from a Jina reranking request
type JinaRerankResponse struct {
	Model   string       `json:"model"`   // Model used for reranking
	Results []RankResult `json:"results"` // Ranked results with relevance scores
	Usage   struct {
		TotalTokens int `json:"total_tokens"` // Total tokens consumed
	} `json:"usage"`
}

// NewJinaReranker creates a new instance of Jina reranker with the provided configuration
func NewJinaReranker(config *RerankerConfig) (*JinaReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://api.jina.ai/v1"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	return &JinaReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client:    &http.Client{},
	}, nil
}

// Rerank performs document reranking based on relevance to the query
func (r *JinaReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body - Jina does NOT use truncate_prompt_tokens
	requestBody := &JinaRerankRequest{
		Model:           r.modelName,
		Query:           query,
		Documents:       documents,
		ReturnDocuments: true, // Return document text in response
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	// Send the request
	req, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("%s/rerank", r.baseURL), bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", r.apiKey))

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

	var response JinaRerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return response.Results, nil
}

// GetModelName returns the name of the reranking model
func (r *JinaReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *JinaReranker) GetModelID() string {
	return r.modelID
}
