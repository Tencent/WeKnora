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

// ZhipuReranker implements a reranking system based on Zhipu AI models
type ZhipuReranker struct {
	modelName string       // Name of the model used for reranking
	modelID   string       // Unique identifier of the model
	apiKey    string       // API key for authentication
	baseURL   string       // Base URL for API requests
	client    *http.Client // HTTP client for making API requests
}

// ZhipuRerankRequest represents a request to rerank documents using Zhipu AI API
type ZhipuRerankRequest struct {
	Model           string   `json:"model"`                       // Model to use for reranking
	Query           string   `json:"query"`                       // Query text to compare documents against
	Documents       []string `json:"documents"`                   // List of document texts to rerank
	TopN            int      `json:"top_n,omitempty"`             // Number of top results to return (0 = all)
	ReturnDocuments bool     `json:"return_documents,omitempty"`  // Whether to return documents in response
	ReturnRawScores bool     `json:"return_raw_scores,omitempty"` // Whether to return raw scores
}

// ZhipuRerankResponse represents the response from Zhipu AI reranking request
type ZhipuRerankResponse struct {
	RequestID string            `json:"request_id"` // Request ID from client or platform
	ID        string            `json:"id"`         // Task order ID from Zhipu platform
	Results   []ZhipuRankResult `json:"results"`    // Ranked results with relevance scores
	Usage     ZhipuUsage        `json:"usage"`      // Token usage information
}

// ZhipuRankResult represents a single reranking result from Zhipu AI
type ZhipuRankResult struct {
	Index          int     `json:"index"`              // Original index of the document
	RelevanceScore float64 `json:"relevance_score"`    // Relevance score
	Document       string  `json:"document,omitempty"` // Document text (optional)
}

// ZhipuUsage contains information about token usage in the Zhipu API request
type ZhipuUsage struct {
	TotalTokens  int `json:"total_tokens"`  // Total tokens consumed
	PromptTokens int `json:"prompt_tokens"` // Prompt tokens
}

// NewZhipuReranker creates a new instance of Zhipu reranker with the provided configuration
func NewZhipuReranker(config *RerankerConfig) (*ZhipuReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://open.bigmodel.cn/api/paas/v4/rerank"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	return &ZhipuReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client:    &http.Client{},
	}, nil
}

// Rerank performs document reranking based on relevance to the query using Zhipu AI API
func (r *ZhipuReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body
	requestBody := &ZhipuRerankRequest{
		Model:           r.modelName,
		Query:           query,
		Documents:       documents,
		TopN:            0, // Return all documents
		ReturnDocuments: true,
		ReturnRawScores: false,
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

	// Log the curl equivalent for debugging
	logger.Debugf(ctx, "curl -X POST %s -H \"Content-Type: application/json\" -H \"Authorization: Bearer %s\" -d '%s'",
		r.baseURL, r.apiKey, string(jsonData),
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
		return nil, fmt.Errorf("zhipu rerank API error: Http Status: %s, Body: %s", resp.Status, string(body))
	}

	var response ZhipuRerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Convert Zhipu results to standard RankResult format
	results := make([]RankResult, len(response.Results))
	for i, zhipuResult := range response.Results {
		results[i] = RankResult{
			Index: zhipuResult.Index,
			Document: DocumentInfo{
				Text: zhipuResult.Document,
			},
			RelevanceScore: zhipuResult.RelevanceScore,
		}
	}

	return results, nil
}

// GetModelName returns the name of the reranking model
func (r *ZhipuReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *ZhipuReranker) GetModelID() string {
	return r.modelID
}
