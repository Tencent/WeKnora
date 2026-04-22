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

// OpenAIReranker implements a reranking system based on OpenAI-compatible APIs.
// It supports both probability-scoring models and logit-scoring models via the
// ScoreFormat configuration.
type OpenAIReranker struct {
	modelName    string       // Name of the model used for reranking
	modelID      string       // Unique identifier of the model
	apiKey       string       // API key for authentication
	baseURL      string       // Base URL for API requests
	client       *http.Client // HTTP client for making API requests
	scoreFormat  ScoreFormat  // How to interpret RelevanceScore from the model
}

// RerankRequest represents a request to rerank documents based on relevance to a query
type RerankRequest struct {
	Model                string                 `json:"model"`                  // Model to use for reranking
	Query                string                 `json:"query"`                  // Query text to compare documents against
	Documents            []string               `json:"documents"`              // List of document texts to rerank
	AdditionalData       map[string]interface{} `json:"additional_data"`        // Optional additional data for the model
	TruncatePromptTokens int                    `json:"truncate_prompt_tokens"` // Maximum prompt tokens to use
}

// RerankResponse represents the response from a reranking request
type RerankResponse struct {
	ID      string       `json:"id"`      // Request ID
	Model   string       `json:"model"`   // Model used for reranking
	Usage   UsageInfo    `json:"usage"`   // Token usage information
	Results []RankResult `json:"results"` // Ranked results with relevance scores
}

// UsageInfo contains information about token usage in the API request
type UsageInfo struct {
	TotalTokens int `json:"total_tokens"` // Total tokens consumed
}

// NewOpenAIReranker creates a new instance of OpenAI reranker with the provided configuration.
// The ScoreFormat from config controls how returned scores are interpreted:
//   - ScoreFormatProbability: pass through as-is (model already returns [0,1])
//   - ScoreFormatLogit: always apply sigmoid (model returns raw logits)
//   - ScoreFormatAuto (default): runtime range detection (values in [0,1] pass through,
//     values outside are sigmoid-converted). This is a safe fallback for unknown deployments.
func NewOpenAIReranker(config *RerankerConfig) (*OpenAIReranker, error) {
	apiKey := config.APIKey
	baseURL := "https://api.openai.com/v1"
	if url := config.BaseURL; url != "" {
		baseURL = url
	}

	format := config.Format
	if format == "" {
		format = ScoreFormatAuto
	}

	return &OpenAIReranker{
		modelName:   config.ModelName,
		modelID:     config.ModelID,
		apiKey:      apiKey,
		baseURL:     baseURL,
		client:      &http.Client{},
		scoreFormat: format,
	}, nil
}

// Rerank performs document reranking based on relevance to the query.
// Scores are normalized according to the configured ScoreFormat.
func (r *OpenAIReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	// Build the request body
	requestBody := &RerankRequest{
		Model:                r.modelName,
		Query:                query,
		Documents:            documents,
		TruncatePromptTokens: 511,
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

	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, fmt.Sprintf("%s/rerank", r.baseURL), query, documents))

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
		return nil, fmt.Errorf("Rerank API error: Http Status: %s", resp.Status)
	}

	var response RerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Normalize scores according to the configured ScoreFormat.
	// This ensures all downstream consumers receive probabilities in [0, 1].
	for i := range response.Results {
		response.Results[i].RelevanceScore = NormalizeByFormat(response.Results[i].RelevanceScore, r.scoreFormat)
	}
	return response.Results, nil
}

// GetModelName returns the name of the reranking model
func (r *OpenAIReranker) GetModelName() string {
	return r.modelName
}

// GetModelID returns the unique identifier of the reranking model
func (r *OpenAIReranker) GetModelID() string {
	return r.modelID
}
