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

// Not configurable by tenants — prevents SSRF.
const firecrawlSearchURL = "https://api.firecrawl.dev/v2/search"

// The web_search tool caps count at 20, so larger API limits are never needed.
const maxFirecrawlResults = 20

// Descriptions carry query-relevant highlights and can run to many KB; the
// agent tool prints snippets verbatim, so keep them at its content excerpt size.
const maxFirecrawlSnippetRunes = 1500

// firecrawlStatusHints explains the documented failure codes without echoing the upstream body.
var firecrawlStatusHints = map[int]string{
	http.StatusUnauthorized:    " (invalid API key)",
	http.StatusPaymentRequired: " (out of credits)",
	http.StatusTooManyRequests: " (rate limited)",
}

// firecrawlFreshness maps the shared pd/pw/pm/py filter values onto Firecrawl's tbs parameter.
var firecrawlFreshness = map[string]string{"pd": "qdr:d", "pw": "qdr:w", "pm": "qdr:m", "py": "qdr:y"}

// FirecrawlProvider queries the Firecrawl Search API.
type FirecrawlProvider struct {
	client         *http.Client
	apiKey         string
	includeContent bool
}

// NewFirecrawlProvider creates a guarded, credential-scoped Firecrawl search client.
func NewFirecrawlProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for Firecrawl provider")
	}
	includeContent := parseExaBool(params.ExtraConfig, "include_content")
	// Scraping every result in the same call takes longer than a plain search.
	timeout := 30 * time.Second
	if includeContent {
		timeout = 60 * time.Second
	}
	client, err := NewSearchHTTPClient(timeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not need redirects. Never forward the API key
	// to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &FirecrawlProvider{
		client:         client,
		apiKey:         strings.TrimSpace(params.APIKey),
		includeContent: includeContent,
	}, nil
}

// Name returns the provider registry identifier.
func (p *FirecrawlProvider) Name() string { return "firecrawl" }

// Search applies the default region and result limit.
func (p *FirecrawlProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	return p.SearchWithFilters(ctx, query, maxResults, includeDate, types.WebSearchFilters{})
}

// SearchWithFilters sends country and freshness (tbs) to the official Firecrawl endpoint.
func (p *FirecrawlProvider) SearchWithFilters(
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
	maxResults = min(maxResults, maxFirecrawlResults)
	payload := map[string]any{
		"query": query, "limit": maxResults, "sources": []string{"web"},
	}
	// Without country Firecrawl uses its default region (US), so ALL simply omits it.
	if country := strings.ToUpper(filters.Country); country != "" && country != "ALL" {
		payload["country"] = country
	}
	if filters.Freshness != "" {
		// The API accepts a custom-range tbs but does not apply it, so ranges are
		// rejected instead of silently returning unfiltered results.
		tbs, ok := firecrawlFreshness[filters.Freshness]
		if !ok {
			return nil, fmt.Errorf("firecrawl freshness must be pd, pw, pm, or py; date ranges are not supported")
		}
		payload["tbs"] = tbs
	}
	// Ask the API to finish inside the client deadline so a timed-out call is not billed.
	if p.client.Timeout > 5*time.Second {
		payload["timeout"] = (p.client.Timeout - 5*time.Second).Milliseconds()
	}
	if p.includeContent {
		payload["scrapeOptions"] = map[string]any{"formats": []string{"markdown"}, "onlyMainContent": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, firecrawlSearchURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("firecrawl search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hint := firecrawlStatusHints[resp.StatusCode]
		return nil, fmt.Errorf("firecrawl search returned HTTP %d%s", resp.StatusCode, hint)
	}
	// Full page markdown for up to 20 results can exceed the plain search cap.
	maxResponse := int64(4 << 20)
	if p.includeContent {
		maxResponse = 16 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read Firecrawl response: %w", err)
	}
	if int64(len(raw)) > maxResponse {
		return nil, fmt.Errorf("firecrawl response exceeds %d bytes", maxResponse)
	}
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Web []struct {
				URL         string `json:"url"`
				Title       string `json:"title"`
				Description string `json:"description"`
				Markdown    string `json:"markdown"`
			} `json:"web"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode Firecrawl response: %w", err)
	}
	if !response.Success {
		return nil, fmt.Errorf("firecrawl search did not succeed")
	}
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Data.Web)))
	for _, row := range response.Data.Web {
		if row.URL == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title: row.Title, URL: row.URL, Source: p.Name(),
			Snippet: truncateExaText(strings.TrimSpace(row.Description), maxFirecrawlSnippetRunes),
		}
		if p.includeContent {
			result.Content = truncateExaText(strings.TrimSpace(row.Markdown), maxExaContentRunes)
			if result.Snippet == "" {
				result.Snippet = truncateExaText(result.Content, 500)
			}
		}
		results = append(results, result)
		if len(results) == maxResults {
			break
		}
	}
	return results, nil
}
