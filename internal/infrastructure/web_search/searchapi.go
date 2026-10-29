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

const searchAPISearchURL = "https://www.searchapi.io/api/v1/search"

// The agent tool never asks for more than 20 rows, so larger requests only
// cost more without adding anything the caller can use.
const maxSearchAPIResults = 20

// searchAPIFreshness maps the shared pd/pw/pm/py filter values onto SearchApi's time_period parameter.
var searchAPIFreshness = map[string]string{
	"pd": "last_day", "pw": "last_week", "pm": "last_month", "py": "last_year",
}

// SearchAPIProvider queries the SearchApi Google search API.
type SearchAPIProvider struct {
	client *http.Client
	apiKey string
}

// NewSearchAPIProvider creates a guarded, credential-scoped SearchApi search client.
func NewSearchAPIProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for SearchApi provider")
	}
	client, err := NewSearchHTTPClient(30*time.Second, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not need redirects. Never forward the bearer
	// token to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &SearchAPIProvider{client: client, apiKey: strings.TrimSpace(params.APIKey)}, nil
}

// Name returns the provider registry identifier.
func (p *SearchAPIProvider) Name() string { return "searchapi" }

// Search applies the default region and result limit.
func (p *SearchAPIProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	return p.SearchWithFilters(ctx, query, maxResults, includeDate, types.WebSearchFilters{})
}

// SearchWithFilters sends country (gl) and freshness (time_period) to the official SearchApi endpoint.
func (p *SearchAPIProvider) SearchWithFilters(
	ctx context.Context, query string, maxResults int, _ bool, filters types.WebSearchFilters,
) ([]*types.WebSearchResult, error) {
	if err := filters.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	maxResults = min(maxResults, maxSearchAPIResults)
	params := url.Values{"engine": {"google"}, "q": {query}, "num": {strconv.Itoa(maxResults)}}
	// Without gl Google picks the region itself, so ALL simply omits it.
	if country := strings.ToLower(filters.Country); country != "" && country != "all" {
		params.Set("gl", country)
	}
	if filters.Freshness != "" {
		timePeriod, ok := searchAPIFreshness[filters.Freshness]
		if !ok {
			return nil, fmt.Errorf("searchapi freshness must be pd, pw, pm, or py; date ranges are not supported")
		}
		params.Set("time_period", timePeriod)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchAPISearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("searchapi search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("searchapi search returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read SearchApi response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("searchapi response exceeds %d bytes", maxResponse)
	}
	var response struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
			Date    string `json:"date"`
		} `json:"organic_results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode SearchApi response: %w", err)
	}
	// num is an upper bound on the Google page size, not an exact count, so trim here.
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.OrganicResults)))
	for _, row := range response.OrganicResults {
		if row.Link == "" {
			continue
		}
		// Google reports dates as "2 days ago" or "Oct 20, 2026"; keep the text as-is
		// instead of inventing an exact publication timestamp.
		results = append(results, &types.WebSearchResult{
			Title: row.Title, URL: row.Link, Snippet: row.Snippet, Source: p.Name(), Age: row.Date,
		})
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
