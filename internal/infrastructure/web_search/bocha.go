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
	// defaultBochaSearchURL is the hardcoded Bocha API URL.
	// Not configurable by tenants — prevents SSRF.
	defaultBochaSearchURL = "https://api.bochaai.com/v1/web-search"
	defaultBochaTimeout   = 30 * time.Second
	defaultBochaResults   = 10
	maxBochaResults       = 50
	maxBochaResponseBytes = 4 << 20
	defaultBochaFreshness = "noLimit"
)

var validBochaFreshness = map[string]struct{}{
	"noLimit": {}, "oneDay": {}, "oneWeek": {}, "oneMonth": {}, "oneYear": {},
}

// BochaProvider implements web search using the Bocha AI Search API.
type BochaProvider struct {
	client    *http.Client
	baseURL   string
	apiKey    string
	freshness string
	summary   bool
}

func NewBochaProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateBochaParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultBochaTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &BochaProvider{
		client: client, baseURL: defaultBochaSearchURL,
		apiKey: strings.TrimSpace(params.APIKey),
		freshness: bochaFreshness(params.ExtraConfig), summary: bochaSummary(params.ExtraConfig),
	}, nil
}

func ValidateBochaParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Bocha provider")
	}
	if freshness := bochaFreshness(params.ExtraConfig); freshness == "" {
		return fmt.Errorf("invalid Bocha freshness: %s", params.ExtraConfig["freshness"])
	}
	return nil
}

func bochaFreshness(extraConfig map[string]string) string {
	if freshness := strings.TrimSpace(extraConfig["freshness"]); freshness != "" {
		if _, ok := validBochaFreshness[freshness]; ok {
			return freshness
		}
		return ""
	}
	return defaultBochaFreshness
}

func bochaSummary(extraConfig map[string]string) bool {
	return strings.TrimSpace(extraConfig["summary"]) != "false"
}

func (p *BochaProvider) Name() string { return "bocha" }

func (p *BochaProvider) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultBochaResults
	}
	if maxResults > maxBochaResults {
		maxResults = maxBochaResults
	}

	body, err := json.Marshal(bochaSearchRequest{
		Query: query, Freshness: p.freshness, Summary: p.summary, Count: maxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Bocha request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Bocha request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][Bocha] query=%q maxResults=%d freshness=%s", query, maxResults, p.freshness)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Bocha request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readBochaResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, bochaHTTPError(resp.StatusCode, respBody)
	}

	var response bochaSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Bocha response: %w", err)
	}
	if response.Code != 0 && response.Code != http.StatusOK {
		return nil, fmt.Errorf("Bocha API returned code %d: %s", response.Code, response.Msg)
	}
	results := make([]*types.WebSearchResult, 0, len(response.Data.WebPages.Value))
	for _, item := range response.Data.WebPages.Value {
		if strings.TrimSpace(item.Name) == "" && strings.TrimSpace(item.URL) == "" {
			continue
		}
		snippet := strings.TrimSpace(item.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		result := &types.WebSearchResult{Title: item.Name, URL: item.URL, Snippet: snippet, Source: "bocha"}
		if includeDate {
			if publishedAt, ok := parseBochaDate(item.DatePublished); ok {
				result.PublishedAt = &publishedAt
			} else if publishedAt, ok := parseBochaDate(item.DateLastCrawled); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Bocha] returned %d results", len(results))
	return results, nil
}

func readBochaResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBochaResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Bocha response: %w", err)
	}
	if len(body) > maxBochaResponseBytes {
		return nil, fmt.Errorf("Bocha response exceeds %d bytes", maxBochaResponseBytes)
	}
	return body, nil
}

func bochaHTTPError(statusCode int, body []byte) error {
	var apiError struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if json.Unmarshal(body, &apiError) == nil {
		if detail := strings.TrimSpace(apiError.Msg); detail != "" {
			return fmt.Errorf("Bocha API returned status %d: %s", statusCode, detail)
		}
	}
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Bocha API returned status %d", statusCode)
	}
	return fmt.Errorf("Bocha API returned status %d: %s", statusCode, detail)
}

func parseBochaDate(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type bochaSearchRequest struct {
	Query     string `json:"query"`
	Freshness string `json:"freshness"`
	Summary   bool   `json:"summary"`
	Count     int    `json:"count"`
}

type bochaSearchResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		WebPages struct {
			Value []bochaWebPage `json:"value"`
		} `json:"webPages"`
	} `json:"data"`
}

type bochaWebPage struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	Snippet         string `json:"snippet"`
	Summary         string `json:"summary"`
	DatePublished   string `json:"datePublished"`
	DateLastCrawled string `json:"dateLastCrawled"`
}
