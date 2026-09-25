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
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultZhipuSearchURL    = "https://open.bigmodel.cn/api/paas/v4/web_search"
	defaultZhipuTimeout      = 15 * time.Second
	defaultZhipuResults      = 10
	maxZhipuResults          = 50
	maxZhipuQueryRunes       = 70
	maxZhipuResponseBytes    = 2 << 20
	defaultZhipuSearchEngine = "search_std"
	defaultZhipuContentSize  = "medium"
)

var validZhipuSearchEngines = map[string]struct{}{
	"search_std":       {},
	"search_pro":       {},
	"search_pro_sogou": {},
	"search_pro_quark": {},
}

var validZhipuContentSizes = map[string]struct{}{
	"medium": {},
	"high":   {},
}

// ZhipuProvider implements web search using Zhipu AI's standalone Web Search API.
type ZhipuProvider struct {
	client       *http.Client
	baseURL      string
	apiKey       string
	searchEngine string
	contentSize  string
}

// NewZhipuProvider creates a Zhipu AI web search provider from persisted parameters.
func NewZhipuProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateZhipuParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultZhipuTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	searchEngine, contentSize := zhipuOptions(params.ExtraConfig)
	return &ZhipuProvider{
		client:       client,
		baseURL:      defaultZhipuSearchURL,
		apiKey:       strings.TrimSpace(params.APIKey),
		searchEngine: searchEngine,
		contentSize:  contentSize,
	}, nil
}

// ValidateZhipuParameters validates credentials and provider-specific options.
func ValidateZhipuParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Zhipu provider")
	}
	searchEngine, contentSize := zhipuOptions(params.ExtraConfig)
	if _, ok := validZhipuSearchEngines[searchEngine]; !ok {
		return fmt.Errorf("invalid Zhipu search engine: %s", searchEngine)
	}
	if _, ok := validZhipuContentSizes[contentSize]; !ok {
		return fmt.Errorf("invalid Zhipu content size: %s", contentSize)
	}
	return nil
}

func zhipuOptions(extraConfig map[string]string) (searchEngine, contentSize string) {
	searchEngine = defaultZhipuSearchEngine
	contentSize = defaultZhipuContentSize
	if value := strings.TrimSpace(extraConfig["search_engine"]); value != "" {
		searchEngine = value
	}
	if value := strings.TrimSpace(extraConfig["content_size"]); value != "" {
		contentSize = value
	}
	return searchEngine, contentSize
}

// Name returns the provider name.
func (p *ZhipuProvider) Name() string {
	return "zhipu"
}

// Search performs a web search using Zhipu AI's standalone Web Search API.
func (p *ZhipuProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	preparedQuery := normalizeZhipuQuery(query)
	if preparedQuery == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if utf8.RuneCountInString(strings.TrimSpace(query)) > maxZhipuQueryRunes {
		logger.Infof(ctx, "[WebSearch][Zhipu] truncated query to %d characters", maxZhipuQueryRunes)
	}
	if maxResults <= 0 {
		maxResults = defaultZhipuResults
	}
	if maxResults > maxZhipuResults {
		maxResults = maxZhipuResults
	}

	requestBody := zhipuSearchRequest{
		SearchQuery:  preparedQuery,
		SearchEngine: p.searchEngine,
		SearchIntent: false,
		Count:        maxResults,
		ContentSize:  p.contentSize,
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Zhipu request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Zhipu request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][Zhipu] query=%q maxResults=%d engine=%s", preparedQuery, maxResults, p.searchEngine)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Zhipu request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := readZhipuResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, zhipuHTTPError(resp.StatusCode, respBody)
	}

	var response zhipuSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Zhipu response: %w", err)
	}
	if response.Error.Message != "" || response.Error.Code != "" {
		return nil, fmt.Errorf("Zhipu API error (%s): %s", response.Error.Code, response.Error.Message)
	}

	results := make([]*types.WebSearchResult, 0, len(response.SearchResult))
	for _, item := range response.SearchResult {
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.Link) == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Content,
			Source:  "zhipu",
		}
		if includeDate {
			if publishedAt, ok := parseZhipuDate(item.PublishDate); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Zhipu] returned %d results", len(results))
	return results, nil
}

func normalizeZhipuQuery(query string) string {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) <= maxZhipuQueryRunes {
		return query
	}
	runes := []rune(query)
	return string(runes[:maxZhipuQueryRunes])
}

func parseZhipuDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func readZhipuResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxZhipuResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Zhipu response: %w", err)
	}
	if len(body) > maxZhipuResponseBytes {
		return nil, fmt.Errorf("Zhipu response exceeds %d bytes", maxZhipuResponseBytes)
	}
	return body, nil
}

func zhipuHTTPError(statusCode int, body []byte) error {
	var response zhipuSearchResponse
	if err := json.Unmarshal(body, &response); err == nil && (response.Error.Code != "" || response.Error.Message != "") {
		return fmt.Errorf("Zhipu API returned status %d (%s): %s", statusCode, response.Error.Code, response.Error.Message)
	}
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Zhipu API returned status %d", statusCode)
	}
	return fmt.Errorf("Zhipu API returned status %d: %s", statusCode, detail)
}

type zhipuSearchRequest struct {
	SearchQuery  string `json:"search_query"`
	SearchEngine string `json:"search_engine"`
	SearchIntent bool   `json:"search_intent"`
	Count        int    `json:"count"`
	ContentSize  string `json:"content_size"`
}

type zhipuSearchResponse struct {
	ID           string              `json:"id"`
	RequestID    string              `json:"request_id"`
	SearchResult []zhipuSearchResult `json:"search_result"`
	Error        zhipuError          `json:"error"`
}

type zhipuSearchResult struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	Link        string `json:"link"`
	Media       string `json:"media"`
	Icon        string `json:"icon"`
	Refer       string `json:"refer"`
	PublishDate string `json:"publish_date"`
}

type zhipuError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
