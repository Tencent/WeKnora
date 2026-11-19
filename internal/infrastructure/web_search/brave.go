package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const braveSearchURL = "https://api.search.brave.com/res/v1/web/search"

// BraveProvider queries the Brave Web Search API.
type BraveProvider struct {
	client *http.Client
	apiKey string
}

// NewBraveProvider creates a guarded, credential-scoped Brave search client.
func NewBraveProvider(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for Brave provider")
	}
	client, err := NewSearchHTTPClient(30*time.Second, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not need redirects. Never forward its subscription
	// token to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &BraveProvider{client: client, apiKey: strings.TrimSpace(params.APIKey)}, nil
}

// Name returns the provider registry identifier.
func (p *BraveProvider) Name() string { return "brave" }

// Search sends country and freshness to the official Brave endpoint; empty filters use its defaults.
func (p *BraveProvider) Search(
	ctx context.Context, request types.WebSearchRequest,
) ([]*types.WebSearchResult, error) {
	query, maxResults, filters := request.Query, request.MaxResults, request.Filters
	if err := filters.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	maxResults = min(maxResults, 20)
	params := url.Values{"q": {query}, "count": {strconv.Itoa(maxResults)}}
	if country := strings.ToUpper(filters.Country); country != "" {
		params.Set("country", country)
	}
	if filters.Freshness != "" {
		params.Set("freshness", filters.Freshness)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveSearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("brave search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("brave search returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read Brave response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("brave response exceeds %d bytes", maxResponse)
	}
	var response struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
				Age         string `json:"age"`
				PageAge     string `json:"page_age"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Brave response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Web.Results)))
	for _, row := range response.Web.Results {
		age := row.Age
		if age == "" {
			age = row.PageAge
		}
		results = append(results, &types.WebSearchResult{
			Title: row.Title, URL: row.URL, Snippet: row.Description, Source: p.Name(), Age: age,
		})
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
