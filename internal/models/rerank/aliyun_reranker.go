package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/sync/errgroup"
)

const (
	// aliyunRerankMaxDocuments is DashScope text-rerank's per-request document
	// cap. Exceeding it returns HTTP 400:
	//   Value error, batch size of documents is invalid, it should not be
	//   larger than 500.
	aliyunRerankMaxDocuments = 500
	// aliyunRerankMaxConcurrency bounds the number of in-flight batch requests
	// so a very large candidate set cannot open hundreds of sockets at once.
	aliyunRerankMaxConcurrency = 4
)

// AliyunReranker implements a reranking system based on Aliyun DashScope models
type AliyunReranker struct {
	modelName     string       // Name of the model used for reranking
	modelID       string       // Unique identifier of the model
	apiKey        string       // API key for authentication
	baseURL       string       // Base URL for API requests
	client        *http.Client // HTTP client for making API requests
	customHeaders map[string]string
}

// SetCustomHeaders 设置用户自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
func (r *AliyunReranker) SetCustomHeaders(headers map[string]string) {
	r.customHeaders = headers
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
	if err := validateRerankBaseURL(baseURL); err != nil {
		return nil, err
	}

	return &AliyunReranker{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		apiKey:    apiKey,
		baseURL:   baseURL,
		client:    newRerankHTTPClient(0),
	}, nil
}

// Rerank performs document reranking based on relevance to the query using Aliyun DashScope API
func (r *AliyunReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	if len(documents) == 0 {
		return []RankResult{}, nil
	}

	// DashScope's text-rerank endpoint caps the document count per request and
	// answers HTTP 400 "batch size of documents is invalid, it should not be
	// larger than 500" past that. Upstream callers (chat pipeline, agent
	// knowledge search, message search) hand in every retrieval candidate and
	// never cap per provider, so a graph expansion or a large embedding_top_k
	// blows straight through the limit — and the pipeline then silently falls
	// back to unranked results. Scores are per (query, document) pair, so they
	// stay comparable across requests: split into limit-sized batches, rerank
	// them concurrently, and merge without dropping any candidate.
	batchCount := (len(documents) + aliyunRerankMaxDocuments - 1) / aliyunRerankMaxDocuments
	batches := make([][]RankResult, batchCount)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(aliyunRerankMaxConcurrency)
	for b := 0; b < batchCount; b++ {
		b := b
		start := b * aliyunRerankMaxDocuments
		end := min(start+aliyunRerankMaxDocuments, len(documents))
		g.Go(func() error {
			results, err := r.rerankBatch(gctx, query, documents[start:end], start)
			if err != nil {
				return err
			}
			batches[b] = results
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Preserve the pre-batching contract: a single request returned the
	// provider's ordering, i.e. descending relevance. Downstream (rerank.go)
	// relies on results[0] being the top candidate and does not sort itself.
	results := make([]RankResult, 0, len(documents))
	for _, batch := range batches {
		results = append(results, batch...)
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].RelevanceScore > results[j].RelevanceScore
	})
	return results, nil
}

// rerankBatch reranks one batch (already sized within the provider limit) and
// returns the results with their indices rebased onto the original document
// slice via offset.
func (r *AliyunReranker) rerankBatch(
	ctx context.Context, query string, documents []string, offset int,
) ([]RankResult, error) {
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
	secutils.ApplyCustomHeaders(req, r.customHeaders)

	logger.Debugf(ctx, "%s", buildRerankRequestDebug(r.modelName, r.baseURL, query, documents))

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
		return nil, fmt.Errorf(
			"aliyun rerank API error: Http Status: %s, Body: %s (documents in this batch: %d)",
			resp.Status, string(body), len(documents),
		)
	}

	var response AliyunRerankResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	// Convert Aliyun results to standard RankResult format, rebasing the
	// provider's batch-relative index onto the original document slice.
	results := make([]RankResult, 0, len(response.Output.Results))
	for _, aliyunResult := range response.Output.Results {
		index := offset + aliyunResult.Index
		if aliyunResult.Index < 0 || aliyunResult.Index >= len(documents) {
			// Defensive: a provider index outside the batch would otherwise
			// silently point at an unrelated candidate downstream.
			logger.Warnf(ctx,
				"aliyun rerank returned out-of-range index %d for a batch of %d (offset %d), skipped",
				aliyunResult.Index, len(documents), offset)
			continue
		}
		results = append(results, RankResult{
			Index: index,
			Document: DocumentInfo{
				Text: aliyunResult.Document.Text,
			},
			RelevanceScore: aliyunResult.RelevanceScore,
		})
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
