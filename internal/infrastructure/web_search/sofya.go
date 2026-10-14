package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const sofyaSearchURL = "https://sofya.co/v1/search"

// Sofya serves up to 20 results per call.
const maxSofyaResults = 20

// sofyaFreshness maps the shared pd/pw/pm/py filter values onto Sofya's freshness.
var sofyaFreshness = map[string]string{"pd": "day", "pw": "week", "pm": "month", "py": "year"}

// SofyaProvider queries the Sofya search API.
type SofyaProvider struct {
	client *http.Client
	apiKey string
}

// NewSofyaProvider creates a guarded, credential-scoped Sofya search client.
func NewSofyaProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for Sofya provider")
	}
	client, err := NewSearchHTTPClient(30*time.Second, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not need redirects. Never forward the API key
	// to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &SofyaProvider{client: client, apiKey: strings.TrimSpace(params.APIKey)}, nil
}

// Name returns the provider registry identifier.
func (p *SofyaProvider) Name() string { return "sofya" }

// Search applies the default result limit with no filters.
func (p *SofyaProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	return p.SearchWithFilters(ctx, query, maxResults, includeDate, types.WebSearchFilters{})
}

// SearchWithFilters sends freshness to the official Sofya endpoint. Sofya has no
// region parameter, so a country other than ALL is rejected instead of dropped.
// Its date-range form did not narrow results in live checks, so ranges are
// rejected too.
func (p *SofyaProvider) SearchWithFilters(
	ctx context.Context, query string, maxResults int, includeDate bool, filters types.WebSearchFilters,
) ([]*types.WebSearchResult, error) {
	if err := filters.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if country := strings.ToUpper(filters.Country); country != "" && country != "ALL" {
		return nil, fmt.Errorf("sofya does not support the country filter; omit it or use ALL")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	// snippets is the SERP alone; the web_search tool fetches page text itself.
	payload := map[string]any{
		"query":        query,
		"max_results":  min(maxResults, maxSofyaResults),
		"search_depth": "snippets",
	}
	if filters.Freshness != "" {
		freshness, ok := sofyaFreshness[filters.Freshness]
		if !ok {
			return nil, fmt.Errorf("sofya freshness must be pd, pw, pm, or py; date ranges are not supported")
		}
		payload["freshness"] = freshness
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sofyaSearchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sofya search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("sofya search returned HTTP %d", resp.StatusCode)
	}
	const maxResponse = 4 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read Sofya response: %w", err)
	}
	if len(data) > maxResponse {
		return nil, fmt.Errorf("sofya response exceeds %d bytes", maxResponse)
	}
	var response struct {
		Results []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Content       string `json:"content"`
			Description   string `json:"description"`
			PublishedDate string `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode Sofya response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Results)))
	for _, row := range response.Results {
		if row.URL == "" {
			continue
		}
		snippet := strings.TrimSpace(row.Description)
		if snippet == "" {
			snippet = strings.TrimSpace(row.Content)
		}
		result := &types.WebSearchResult{Title: row.Title, URL: row.URL, Snippet: snippet, Source: p.Name()}
		if includeDate {
			if publishedAt, err := time.Parse(time.DateOnly, row.PublishedDate); err == nil {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
