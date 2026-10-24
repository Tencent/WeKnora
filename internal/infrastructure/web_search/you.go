package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Not configurable by tenants, which prevents SSRF.
const (
	// youKeylessSearchURL is You.com's keyless tier (100 searches a day, no account).
	youKeylessSearchURL = "https://api.you.com/v1/agents/search"
	// youKeyedSearchURL is the authenticated Search API used when a key is configured.
	youKeyedSearchURL = "https://ydc-index.io/v1/search"
)

// youUserAgent keeps the WeKnora token other providers send and adds You.com's
// integration token, so You.com can attribute keyless traffic to WeKnora.
const youUserAgent = "WeKnora/1.0 youdotcom-integration/tencent-weknora"

// defaultYouTimeout matches the other hosted search providers.
var defaultYouTimeout = 15 * time.Second

// The web_search tool caps count at 20, so larger API limits are never needed.
const maxYouResults = 20

// Each row carries up to a few snippets; keep the snippet at the size other
// providers use and the joined excerpts at the Exa content cap.
const maxYouSnippetRunes = 1500

// youStatusHints explains the documented failure codes without echoing the upstream body.
var youStatusHints = map[int]string{
	http.StatusUnauthorized:    " (invalid API key)",
	http.StatusForbidden:       " (invalid API key)",
	http.StatusPaymentRequired: " (out of credits)",
}

// YouProvider queries the You.com Search API. Keyless by default: with no API
// key it calls the free tier; a key switches to the authenticated endpoint.
type YouProvider struct {
	client *http.Client
	apiKey string
}

// NewYouProvider creates a You.com search client. The API key is optional.
func NewYouProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	client, err := NewSearchHTTPClient(defaultYouTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoints do not need redirects. Never forward the API key
	// to a redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &YouProvider{client: client, apiKey: strings.TrimSpace(params.APIKey)}, nil
}

// Name returns the provider registry identifier.
func (p *YouProvider) Name() string { return "you" }

// Search queries the keyless tier, or the Search API when a key is configured.
func (p *YouProvider) Search(
	ctx context.Context, query string, maxResults int, includeDate bool,
) ([]*types.WebSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	maxResults = min(maxResults, maxYouResults)
	endpoint := youKeylessSearchURL
	if p.apiKey != "" {
		endpoint = youKeyedSearchURL
	}
	params := url.Values{"query": {query}, "count": {strconv.Itoa(maxResults)}}
	logger.Infof(ctx, "[WebSearch][You] query=%q maxResults=%d url=%s", query, maxResults, endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", youUserAgent)
	if p.apiKey != "" {
		req.Header.Set("X-API-Key", p.apiKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("you.com search request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		hint := youStatusHints[resp.StatusCode]
		if resp.StatusCode == http.StatusTooManyRequests {
			hint = " (rate limited)"
			if p.apiKey == "" {
				hint = " (keyless limit reached; add an API key for higher limits)"
			}
		}
		logger.Warnf(ctx, "[WebSearch][You] API returned status %d%s", resp.StatusCode, hint)
		return nil, fmt.Errorf("you.com search returned HTTP %d%s", resp.StatusCode, hint)
	}
	raw, err := readCapped(resp.Body, 0)
	if err != nil {
		return nil, fmt.Errorf("read You.com response: %w", err)
	}
	var response struct {
		Results struct {
			Web []struct {
				URL         string   `json:"url"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Snippets    []string `json:"snippets"`
				PageAge     string   `json:"page_age"`
			} `json:"web"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode You.com response: %w", err)
	}
	// count is an upper bound per section, not an exact total, so trim here.
	results := make([]*types.WebSearchResult, 0, min(maxResults, len(response.Results.Web)))
	for _, row := range response.Results.Web {
		if row.URL == "" {
			continue
		}
		content := strings.TrimSpace(strings.Join(row.Snippets, "\n"))
		snippet := strings.TrimSpace(row.Description)
		if snippet == "" {
			snippet = content
		}
		result := &types.WebSearchResult{
			Title: row.Title, URL: row.URL, Source: p.Name(),
			Snippet: truncateExaText(snippet, maxYouSnippetRunes),
			Content: truncateExaText(content, maxExaContentRunes),
		}
		if includeDate {
			result.PublishedAt = parseYouPageAge(row.PageAge)
		}
		results = append(results, result)
		if len(results) == maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][You] returned %d results", len(results))
	return results, nil
}

// parseYouPageAge reads page_age, which You.com returns as RFC 3339 or as a
// zoneless timestamp such as 2026-08-14T00:00:00 (treated as UTC).
func parseYouPageAge(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, value); err == nil {
			return &t
		}
	}
	return nil
}
