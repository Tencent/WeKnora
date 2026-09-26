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
	// defaultYoucomSearchURL is the You.com search API endpoint.
	defaultYoucomSearchURL = "https://api.you.com/api/v1/search"
)

var defaultYoucomTimeout = 15 * time.Second

// YoucomProvider implements web search using the You.com Search API.
type YoucomProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewYoucomProvider creates a new You.com provider from parameters.
func NewYoucomProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if params.APIKey == "" {
		return nil, fmt.Errorf("API key is required for You.com provider")
	}
	client, err := NewSearchHTTPClient(defaultYoucomTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &YoucomProvider{
		client:  client,
		baseURL: defaultYoucomSearchURL,
		apiKey:  params.APIKey,
	}, nil
}

// Name returns the provider name.
func (p *YoucomProvider) Name() string {
	return "youcom"
}

// Search performs a web search using the You.com Search API.
func (p *YoucomProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	logger.Infof(ctx, "[WebSearch][Youcom] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)

	reqBody := youcomSearchRequest{
		Query:  query,
		Count:  maxResults,
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
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logger.Warnf(ctx, "[WebSearch][Youcom] API returned status %d: %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("you.com API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var respData youcomSearchResponse
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, len(respData.Results))
	for _, item := range respData.Results {
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Snippet,
			Source:  "youcom",
		}
		if includeDate && item.PublishedDate != "" {
			if t, err := time.Parse(time.RFC3339, item.PublishedDate); err == nil {
				result.PublishedAt = &t
			}
		}
		results = append(results, result)
	}
	logger.Infof(ctx, "[WebSearch][Youcom] returned %d results", len(results))
	return results, nil
}

// youcomSearchRequest defines the request body for the You.com search API.
type youcomSearchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

// youcomSearchResponse defines the response structure for the You.com search API.
type youcomSearchResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Snippet       string `json:"snippet"`
		PublishedDate string `json:"published_date,omitempty"`
	} `json:"results"`
}