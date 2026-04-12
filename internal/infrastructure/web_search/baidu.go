package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultBaiduWebSearchURL is the hardcoded Baidu AI Search API URL.
	// Not configurable by tenants — prevents SSRF.
	defaultBaiduWebSearchURL = "https://qianfan.baidubce.com/v2/ai_search/web_search"
	defaultBaiduTimeout      = 15 * time.Second
	defaultBaiduResults      = 5
	maxBaiduResults          = 50
	maxBaiduQueryUnits       = 72
)

// BaiduProvider implements web search using Baidu AI Search API.
type BaiduProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewBaiduProvider creates a new Baidu web search provider from parameters.
func NewBaiduProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if params.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Baidu provider")
	}
	client := &http.Client{
		Timeout: defaultBaiduTimeout,
	}
	return &BaiduProvider{
		client:  client,
		baseURL: defaultBaiduWebSearchURL, // Hardcoded — not tenant-configurable
		apiKey:  params.APIKey,
	}, nil
}

// Name returns the provider name.
func (p *BaiduProvider) Name() string {
	return "baidu"
}

// Search performs a web search using Baidu AI Search API.
func (p *BaiduProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	preparedQuery := normalizeBaiduQuery(query)
	if preparedQuery == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if preparedQuery != strings.TrimSpace(query) {
		logger.Infof(ctx, "[WebSearch][Baidu] normalized query to satisfy API constraints")
	}

	if maxResults <= 0 {
		maxResults = defaultBaiduResults
	}
	if maxResults > maxBaiduResults {
		maxResults = maxBaiduResults
	}

	logger.Infof(ctx, "[WebSearch][Baidu] query=%q maxResults=%d url=%s", preparedQuery, maxResults, p.baseURL)
	req, err := p.buildRequest(ctx, preparedQuery, maxResults)
	if err != nil {
		return nil, err
	}
	results, err := p.doSearch(ctx, req, includeDate)
	if err != nil {
		logger.Warnf(ctx, "[WebSearch][Baidu] failed: %v", err)
		return nil, err
	}
	logger.Infof(ctx, "[WebSearch][Baidu] returned %d results", len(results))
	return results, nil
}

func (p *BaiduProvider) buildRequest(ctx context.Context, query string, maxResults int) (*http.Request, error) {
	requestBody := baiduSearchRequest{
		Messages: []baiduMessage{
			{Role: "user", Content: query},
		},
		SearchSource: "baidu_search_v2",
		ResourceTypeFilter: []baiduResourceTypeFilter{
			{Type: "web", TopK: maxResults},
		},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

func (p *BaiduProvider) doSearch(ctx context.Context, req *http.Request, includeDate bool) ([]*types.WebSearchResult, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2MB limit
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		logger.Warnf(ctx, "[WebSearch][Baidu] API returned status %d: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("baidu API returned status %d: %s", resp.StatusCode, string(body))
	}

	var respData baiduSearchResponse
	if err := json.Unmarshal(body, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Check for API-level error (returned with 200 status)
	if respData.Code != 0 {
		return nil, fmt.Errorf("baidu API error (code %d): %s", respData.Code, respData.Message)
	}

	results := make([]*types.WebSearchResult, 0, len(respData.References))
	for _, ref := range respData.References {
		result := &types.WebSearchResult{
			Title:   ref.Title,
			URL:     ref.URL,
			Content: ref.Content,
			Source:  "baidu",
		}

		if includeDate && ref.Date != "" {
			if t, err := parseBaiduDate(ref.Date); err == nil {
				result.PublishedAt = &t
			}
		}

		results = append(results, result)
	}
	return results, nil
}

var baiduDateRe = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?`)

// parseBaiduDate extracts date components via regex, handling variable formats
// like "2025-4-24", "2025-04-27 18:02:00", "2025-05-20 11:58" uniformly.
func parseBaiduDate(dateStr string) (time.Time, error) {
	m := baiduDateRe.FindStringSubmatch(dateStr)
	if m == nil {
		return time.Time{}, fmt.Errorf("unable to parse date: %s", dateStr)
	}
	// Pad to "YYYY-MM-DD HH:MM:SS" and parse once
	normalized := fmt.Sprintf("%s-%02s-%02s %02s:%02s:%02s",
		m[1], m[2], m[3],
		defaultStr(m[4], "00"), defaultStr(m[5], "00"), defaultStr(m[6], "00"))
	return time.Parse("2006-01-02 15:04:05", normalized)
}

func defaultStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// Baidu documents the query length limit as 72 chars, counting CJK/full-width
// runes as 2. Use a conservative width model so mixed-language input stays
// under the API limit.
func normalizeBaiduQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if baiduQueryUnits(query) <= maxBaiduQueryUnits {
		return query
	}

	var b strings.Builder
	b.Grow(len(query))
	used := 0
	for _, r := range query {
		width := baiduQueryUnitWidth(r)
		if used+width > maxBaiduQueryUnits {
			break
		}
		b.WriteRune(r)
		used += width
	}
	return b.String()
}

func baiduQueryUnits(query string) int {
	units := 0
	for _, r := range query {
		units += baiduQueryUnitWidth(r)
	}
	return units
}

func baiduQueryUnitWidth(r rune) int {
	if r <= unicode.MaxASCII {
		return 1
	}
	return 2
}

// --- Request/Response types ---

type baiduSearchRequest struct {
	Messages            []baiduMessage            `json:"messages"`
	SearchSource        string                    `json:"search_source"`
	ResourceTypeFilter  []baiduResourceTypeFilter `json:"resource_type_filter"`
	SearchRecencyFilter string                    `json:"search_recency_filter,omitempty"`
}

type baiduMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type baiduResourceTypeFilter struct {
	Type string `json:"type"`
	TopK int    `json:"top_k"`
}

type baiduSearchResponse struct {
	References []baiduReference `json:"references"`
	RequestID  string           `json:"request_id"`
	Code       int              `json:"code"`
	Message    string           `json:"message"`
}

type baiduReference struct {
	ID      int    `json:"id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
	Date    string `json:"date"`
	Type    string `json:"type"`
}
