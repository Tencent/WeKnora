package web_search

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
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultOllamaWebSearchURL is the hardcoded Ollama web search API URL.
	// Not configurable by tenants — prevents SSRF.
	defaultOllamaWebSearchURL = "https://ollama.com/api/web_search"
	defaultOllamaTimeout      = 10 * time.Second
	defaultOllamaResults      = 5
	maxOllamaResults          = 10 // Ollama限制最多10个结果
)

// OllamaProvider implements web search using Ollama Cloud API
type OllamaProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewOllamaProvider creates a new Ollama web search provider from parameters.
func NewOllamaProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if params.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Ollama provider")
	}
	client := &http.Client{
		Timeout: defaultOllamaTimeout,
	}
	return &OllamaProvider{
		client:  client,
		baseURL: defaultOllamaWebSearchURL, // Hardcoded — not tenant-configurable
		apiKey:  params.APIKey,
	}, nil
}

// Name returns the provider name
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Search performs a web search using Ollama Cloud API
func (p *OllamaProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}

	if maxResults <= 0 {
		maxResults = defaultOllamaResults
	}

	// Ollama限制最多10个结果
	if maxResults > maxOllamaResults {
		maxResults = maxOllamaResults
	}

	logger.Infof(ctx, "[WebSearch][Ollama] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)
	req, err := p.buildRequest(ctx, query, maxResults)
	if err != nil {
		return nil, err
	}
	results, err := p.doSearch(ctx, req)
	if err != nil {
		logger.Warnf(ctx, "[WebSearch][Ollama] failed: %v", err)
		return nil, err
	}
	logger.Infof(ctx, "[WebSearch][Ollama] returned %d results", len(results))
	return results, nil
}

func (p *OllamaProvider) buildRequest(ctx context.Context, query string, maxResults int) (*http.Request, error) {
	requestBody := map[string]interface{}{
		"query":       query,
		"max_results": maxResults,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", defaultUserAgentHeader)

	return req, nil
}

func (p *OllamaProvider) doSearch(ctx context.Context, req *http.Request) ([]*types.WebSearchResult, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		logger.Warnf(ctx, "[WebSearch][Ollama] API returned status %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var respData ollamaSearchResponse
	if err := json.Unmarshal(body, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, len(respData.Results))
	for _, item := range respData.Results {
		results = append(results, &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Snippet,
			Content: item.Content,
			Source:  "ollama",
		})
	}
	return results, nil
}

// ollamaSearchResponse defines the response structure for Ollama web search API.
type ollamaSearchResponse struct {
	Results []ollamaSearchResult `json:"results"`
}

type ollamaSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Snippet string `json:"snippet"`
}
