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

// AliyunReranker implements a reranking system based on Aliyun DashScope models
type AliyunReranker struct {
	modelName string       // Name of the model used for reranking
	modelID   string       // Unique identifier of the model
	apiKey    string       // API key for authentication
	baseURL   string       // Base URL for API requests
	client    *http.Client // HTTP client for making API requests
}

// AliyunRerankRequest represents a request to rerank documents using Aliyun DashScope API
type AliyunRerankRequest struct {
	Model      string                 `json:"model"`      // Model to use for reranking
	Input      AliyunRerankInput      `json:"input"`      // Input containing query and documents
	Parameters AliyunRerankParameters `json:"parameters"` // Parameters for the reranking
}

// AliyunRerankInput contains the query and documents for reranking
type AliyunRerankInput struct {
	Query     string   `json:"query"`     // Query text to compare documents against
	Documents []string `json:"documents"` // List of document texts to rerank
}

// AliyunRerankParameters contains parameters for the reranking request
type AliyunRerankParameters struct {
	ReturnDocuments bool `json:"return_documents"` // Whether to return documents in response
	TopN            int  `json:"top_n"`            // Number of top results to return
}

// AliyunRerankResponse represents the response from Aliyun DashScope reranking request
type AliyunRerankResponse struct {
	Output AliyunOutput `json:"output"` // Output containing results
	Usage  AliyunUsage  `json:"usage"`  // Token usage information
}

// AliyunOutput contains the reranking results
type AliyunOutput struct {
	Results []AliyunRankResult `json:"results"` // Ranked results with relevance scores
}

// AliyunRankResult represents a single reranking result from Aliyun
type AliyunRankResult struct {
	Document       AliyunDocument `json:"document"`        // Document information
	Index          int            `json:"index"`           // Original index of the document
	RelevanceScore float64        `json:"relevance_score"` // Relevance score
}

// AliyunDocument represents document information in Aliyun response
type AliyunDocument struct {
	Text string `json:"text"` // Document text
}

// AliyunUsage contains information about token usage in the Aliyun API request
type AliyunUsage struct {
	TotalTokens int `json:"total_tokens"` // Total tokens consumed
}

// NewAliyunReranker creates a new instance of Aliyun reranker with the provided configuration
func NewAliyunReranker(config *RerankerConfig) (*AliyunReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	return &AliyunReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client:    &http.Client{},
	}, nil
}

// Rerank performs document reranking based on relevance to the query using Aliyun DashScope API
func (r *AliyunReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body
	requestBody := &AliyunRerankRequest{
		Model: r.modelName,
		Input: AliyunRerankInput{
			Query:     query,
			Documents: documents,
		},
		Parameters: AliyunRerankParameters{
			ReturnDocuments: true,
			TopN:            len(documents), // Return all documents
		},
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
		return nil, fmt.Errorf("aliyun rerank API error: Http Status: %s, Body: %s", resp.Status, string(body))
	}

	var response AliyunRerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Convert Aliyun results to standard RankResult format
	results := make([]RankResult, len(response.Output.Results))
	for i, aliyunResult := range response.Output.Results {
		results[i] = RankResult{
			Index: aliyunResult.Index,
			Document: DocumentInfo{
				Text: aliyunResult.Document.Text,
			},
			RelevanceScore: aliyunResult.RelevanceScore,
		}
	}

	return results, nil
}

// GetModelName returns the name of the reranking model
func (r *AliyunReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *AliyunReranker) GetModelID() string {
	return r.modelID
}
