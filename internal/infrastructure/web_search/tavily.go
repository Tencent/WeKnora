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
	// defaultTavilySearchURL is the hardcoded Tavily API URL.
	// Not configurable by tenants — prevents SSRF.
	defaultTavilySearchURL = "https://api.tavily.com/search"
)

var (
	defaultTavilyTimeout = 15 * time.Second
)

// TavilyProvider implements web search using Tavily Search API
type TavilyProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewTavilyProvider creates a new Tavily provider from parameters (no environment variables).
func NewTavilyProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if params.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Tavily provider")
	}
	client := &http.Client{
		Timeout: defaultTavilyTimeout,
	}
	return &TavilyProvider{
		client:  client,
		baseURL: defaultTavilySearchURL,
		apiKey:  params.APIKey,
	}, nil
}

// Name returns the provider name
func (p *TavilyProvider) Name() string {
	return "tavily"
}

// Search performs a web search using Tavily Search API
func (p *TavilyProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	logger.Infof(ctx, "[WebSearch][Tavily] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)

	reqBody := tavilySearchRequest{
		APIKey:     p.apiKey,
		Query:      query,
		MaxResults: maxResults,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logger.Warnf(ctx, "[WebSearch][Tavily] API returned status %d: %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("tavily API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var respData tavilySearchResponse
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, len(respData.Results))
	for _, item := range respData.Results {
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Content,
			Source:  "tavily",
		}
		if includeDate && item.PublishedDate != "" {
			if t, err := time.Parse(time.RFC3339, item.PublishedDate); err == nil {
				result.PublishedAt = &t
			}
		}
		results = append(results, result)
	}
	logger.Infof(ctx, "[WebSearch][Tavily] returned %d results", len(results))
	return results, nil
}

// tavilySearchRequest defines the request body for Tavily search API
type tavilySearchRequest struct {
	APIKey     string `json:"api_key"`
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

// tavilySearchResponse defines the response structure for Tavily search API
type tavilySearchResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title         string  `json:"title"`
		URL           string  `json:"url"`
		Content       string  `json:"content"`
		Score         float64 `json:"score"`
		PublishedDate string  `json:"published_date,omitempty"`
	} `json:"results"`
}
