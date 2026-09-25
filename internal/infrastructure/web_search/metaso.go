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

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultMetasoSearchURL = "https://metaso.cn/api/v1/search"
	defaultMetasoTimeout   = 30 * time.Second
	defaultMetasoResults   = 10
	maxMetasoResults       = 50
	maxMetasoResponseBytes = 4 << 20
	defaultMetasoScope     = "webpage"
)

var validMetasoScopes = map[string]struct{}{
	"webpage": {}, "document": {}, "scholar": {},
	"podcast": {}, "video": {}, "image": {},
}

// MetasoProvider implements web search using the official Metaso AI Search API.
type MetasoProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	scope   string
}

func NewMetasoProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateMetasoParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultMetasoTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &MetasoProvider{
		client: client, baseURL: defaultMetasoSearchURL,
		apiKey: strings.TrimSpace(params.APIKey), scope: metasoScope(params.ExtraConfig),
	}, nil
}

func ValidateMetasoParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Metaso provider")
	}
	scope := metasoScope(params.ExtraConfig)
	if _, ok := validMetasoScopes[scope]; !ok {
		return fmt.Errorf("invalid Metaso search scope: %s", scope)
	}
	return nil
}

func metasoScope(extraConfig map[string]string) string {
	if scope := strings.TrimSpace(extraConfig["scope"]); scope != "" {
		return scope
	}
	return defaultMetasoScope
}

func (p *MetasoProvider) Name() string { return "metaso" }

func (p *MetasoProvider) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultMetasoResults
	}
	if maxResults > maxMetasoResults {
		maxResults = maxMetasoResults
	}

	body, err := json.Marshal(metasoSearchRequest{
		Query: query, Scope: p.scope, Size: maxResults,
		IncludeSummary: true, IncludeRawContent: false, ConciseSnippet: true,
	})
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

	logger.Infof(ctx, "[WebSearch][Metaso] query=%q maxResults=%d scope=%s", query, maxResults, p.scope)
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

	var response metasoSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Metaso response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, len(response.Webpages))
	for _, item := range response.Webpages {
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.Link) == "" {
			continue
		}
		snippet := strings.TrimSpace(item.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		result := &types.WebSearchResult{Title: item.Title, URL: item.Link, Snippet: snippet, Content: item.RawContent, Source: "metaso"}
		if includeDate {
			if publishedAt, ok := parseMetasoDate(item.Date); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Metaso] returned %d results", len(results))
	return results, nil
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
	var apiError struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &apiError) == nil {
		detail := strings.TrimSpace(apiError.Message)
		if detail == "" {
			detail = strings.TrimSpace(apiError.Error)
		}
		if detail != "" {
			return fmt.Errorf("Metaso API returned status %d: %s", statusCode, detail)
		}
	}
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Metaso API returned status %d", statusCode)
	}
	return fmt.Errorf("Metaso API returned status %d: %s", statusCode, detail)
}

func parseMetasoDate(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type metasoSearchRequest struct {
	Query             string `json:"q"`
	Scope             string `json:"scope"`
	Size              int    `json:"size"`
	IncludeSummary    bool   `json:"includeSummary"`
	IncludeRawContent bool   `json:"includeRawContent"`
	ConciseSnippet    bool   `json:"conciseSnippet"`
}

type metasoSearchResponse struct {
	Webpages []metasoWebpage `json:"webpages"`
}

type metasoWebpage struct {
	Title      string `json:"title"`
	Link       string `json:"link"`
	Snippet    string `json:"snippet"`
	Summary    string `json:"summary"`
	RawContent string `json:"rawContent"`
	Date       string `json:"date"`
}
