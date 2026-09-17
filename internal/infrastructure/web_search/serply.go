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

const serplySearchURL = "https://api.serply.io/v1/search"

// Serply returns at most one Google results page per call, so larger requests
// only waste credits without adding rows.
const maxSerplyResults = 10

// serplyFreshness maps the shared pd/pw/pm/py filter values onto Google's tbs parameter.
var serplyFreshness = map[string]string{"pd": "qdr:d", "pw": "qdr:w", "pm": "qdr:m", "py": "qdr:y"}

// SerplyProvider queries the Serply Google search API.
type SerplyProvider struct {
	client *http.Client
	apiKey string
}

// NewSerplyProvider creates a guarded, credential-scoped Serply search client.
func NewSerplyProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for Serply provider")
	}
	client, err := NewSearchHTTPClient(30*time.Second, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not need redirects. Never forward the API key
	// to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &SerplyProvider{client: client, apiKey: strings.TrimSpace(params.APIKey)}, nil
}

// Name returns the provider registry identifier.
func (p *SerplyProvider) Name() string { return "serply" }

// Search applies the default region and result limit.
func (p *SerplyProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	return p.SearchWithFilters(ctx, query, maxResults, includeDate, types.WebSearchFilters{})
}

// SearchWithFilters sends country (gl) and freshness (tbs) to the official Serply endpoint.
func (p *SerplyProvider) SearchWithFilters(
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
	params := url.Values{"q": {query}, "num": {strconv.Itoa(min(maxResults, maxSerplyResults))}}
	// Without gl Google picks the region itself, so ALL simply omits it.
	if country := strings.ToLower(filters.Country); country != "" && country != "all" {
		params.Set("gl", country)
	}
	if filters.Freshness != "" {
		tbs, ok := serplyFreshness[filters.Freshness]
		if !ok {
			return nil, fmt.Errorf("serply freshness must be pd, pw, pm, or py; date ranges are not supported")
		}
		params.Set("tbs", tbs)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, serplySearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")
	req.Header.Set("X-Api-Key", p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serply search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("serply search returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read Serply response: %w", err)
	}
	if len(body) > maxResponse {
		return nil, fmt.Errorf("serply response exceeds %d bytes", maxResponse)
	}
	var response struct {
		Results []struct {
			Title       string `json:"title"`
			Link        string `json:"link"`
			Description string `json:"description"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Serply response: %w", err)
	}
	// num is an upper bound on the Google page size, not an exact count, so trim here.
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Results)))
	for _, row := range response.Results {
		if row.Link == "" {
			continue
		}
		results = append(results, &types.WebSearchResult{
			Title: row.Title, URL: row.Link, Snippet: row.Description, Source: p.Name(),
		})
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
