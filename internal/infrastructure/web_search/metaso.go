package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultMetasoSearchURL is the hardcoded Metaso API URL.
	// Not configurable by tenants — prevents SSRF.
	defaultMetasoSearchURL  = "https://metaso.cn/api/v1/search"
	defaultMetasoTimeout    = 15 * time.Second
	defaultMetasoResults    = 10
	maxMetasoResults        = 50
	maxMetasoQueryRunes     = 200
	maxMetasoResponseBytes  = 4 << 20
	defaultMetasoScope      = "webpage"
	defaultMetasoSizeString = "10"
)

// metasoScopeFields maps a Metaso scope to the JSON field name that carries
// the result array in the response. Metaso returns different array keys per
// scope (e.g. "webpages", "scholars", "podcasts"); we normalize them here.
var metasoScopeFields = map[string]string{
	"webpage":  "webpages",
	"document": "documents",
	"scholar":  "scholars",
	"image":    "images",
	"video":    "videos",
	"podcast":  "podcasts",
}

// MetasoProvider implements web search using the Metaso Search API.
type MetasoProvider struct {
	client         *http.Client
	baseURL        string
	apiKey         string
	scope          string
	conciseSnippet bool
	includeSummary bool
}

// NewMetasoProvider creates a Metaso search provider from persisted parameters.
func NewMetasoProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateMetasoParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultMetasoTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	scope, conciseSnippet, includeSummary := metasoOptions(params.ExtraConfig)
	return &MetasoProvider{
		client:         client,
		baseURL:        defaultMetasoSearchURL,
		apiKey:         strings.TrimSpace(params.APIKey),
		scope:          scope,
		conciseSnippet: conciseSnippet,
		includeSummary: includeSummary,
	}, nil
}

// ValidateMetasoParameters validates credentials and provider-specific options.
func ValidateMetasoParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Metaso provider")
	}
	scope, _, _ := metasoOptions(params.ExtraConfig)
	if _, ok := metasoScopeFields[scope]; !ok {
		return fmt.Errorf("invalid Metaso scope: %s", scope)
	}
	return nil
}

// metasoOptions resolves the scope and boolean options from ExtraConfig.
// Booleans are stored as strings ("true"/"false") in ExtraConfig.
func metasoOptions(extraConfig map[string]string) (scope string, conciseSnippet bool, includeSummary bool) {
	scope = defaultMetasoScope
	if value := strings.TrimSpace(extraConfig["scope"]); value != "" {
		scope = value
	}
	conciseSnippet = true
	if value, ok := extraConfig["conciseSnippet"]; ok {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			conciseSnippet = parsed
		}
	}
	includeSummary = true
	if value, ok := extraConfig["includeSummary"]; ok {
		if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
			includeSummary = parsed
		}
	}
	return scope, conciseSnippet, includeSummary
}

// Name returns the provider name.
func (p *MetasoProvider) Name() string {
	return "metaso"
}

// Search performs a web search using the Metaso Search API.
func (p *MetasoProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	preparedQuery := normalizeMetasoQuery(query)
	if preparedQuery == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultMetasoResults
	}
	if maxResults > maxMetasoResults {
		maxResults = maxMetasoResults
	}

	// Metaso expects size as a string.
	reqBody := metasoSearchRequest{
		Query:          preparedQuery,
		Scope:          p.scope,
		IncludeSummary: p.includeSummary,
		Size:           strconv.Itoa(maxResults),
		ConciseSnippet: p.conciseSnippet,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Metaso request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Metaso request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][Metaso] query=%q scope=%s size=%d concise=%v summary=%v",
		preparedQuery, p.scope, maxResults, p.conciseSnippet, p.includeSummary)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Metaso request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := readMetasoResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, metasoHTTPError(resp.StatusCode, respBody)
	}

	// Metaso returns the result array under a scope-specific key
	// (e.g. "webpages", "scholars"). Decode the raw JSON once and extract
	// the relevant array; fall back to an empty result list when the key is
	// absent so partial/empty responses don't fail the search.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Metaso response: %w", err)
	}
	arrayKey := metasoScopeFields[p.scope]
	items, ok := raw[arrayKey]
	if !ok {
		logger.Infof(ctx, "[WebSearch][Metaso] response missing %q field; returning 0 results", arrayKey)
		return []*types.WebSearchResult{}, nil
	}

	var results []metasoResultItem
	if err := json.Unmarshal(items, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Metaso %s array: %w", arrayKey, err)
	}

	out := make([]*types.WebSearchResult, 0, len(results))
	for _, item := range results {
		// Skip entries with neither title nor URL.
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.URL()) == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL(),
			Snippet: item.SnippetText(),
			Source:  "metaso",
		}
		if includeDate {
			if publishedAt, ok := parseMetasoDate(item.Date); ok {
				result.PublishedAt = &publishedAt
			}
		}
		out = append(out, result)
		if len(out) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Metaso] returned %d results", len(out))
	return out, nil
}

// normalizeMetasoQuery trims and truncates the query to a safe rune length.
func normalizeMetasoQuery(query string) string {
	query = strings.TrimSpace(query)
	runeCount := 0
	for range query {
		runeCount++
	}
	if runeCount <= maxMetasoQueryRunes {
		return query
	}
	runes := []rune(query)
	return string(runes[:maxMetasoQueryRunes])
}

// metasoResultItem models a single Metaso result across all scopes.
// Different scopes populate different subsets of these fields, so every
// field except title is optional. SnippetText/URL provide the normalized
// value used to build WebSearchResult.
type metasoResultItem struct {
	Title    string `json:"title"`
	Link     string `json:"link"`
	Snippet  string `json:"snippet"`
	Summary  string `json:"summary"`
	ImageURL string `json:"imageUrl"`
	Date     string `json:"date"`
	Position int    `json:"position"`
}

// URL returns the canonical URL for the item. For image scope, Metaso
// returns imageUrl instead of link, so we prefer it as the URL.
func (i metasoResultItem) URL() string {
	if i.Link != "" {
		return i.Link
	}
	return i.ImageURL
}

// SnippetText returns the best available text snippet. When includeSummary
// is enabled, webpage/document scopes populate summary (no snippet); we
// prefer summary when snippet is empty so the result still carries context.
func (i metasoResultItem) SnippetText() string {
	if i.Snippet != "" {
		return i.Snippet
	}
	return i.Summary
}

// parseMetasoDate parses common Metaso date formats.
func parseMetasoDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006年01月02日",
		"2006年1月2日",
		"2006年01月",
		"2006年1月",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func readMetasoResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxMetasoResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Metaso response: %w", err)
	}
	if len(body) > maxMetasoResponseBytes {
		return nil, fmt.Errorf("Metaso response exceeds %d bytes", maxMetasoResponseBytes)
	}
	return body, nil
}

func metasoHTTPError(statusCode int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Metaso API returned status %d", statusCode)
	}
	return fmt.Errorf("Metaso API returned status %d: %s", statusCode, detail)
}

type metasoSearchRequest struct {
	Query          string `json:"q"`
	Scope          string `json:"scope"`
	IncludeSummary bool   `json:"includeSummary"`
	Size           string `json:"size"`
	ConciseSnippet bool   `json:"conciseSnippet"`
}
