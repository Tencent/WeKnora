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

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultDoubaoSearchURL is the hardcoded Volcano Doubao Search Custom
	// Edition API URL. Not configurable by tenants — prevents SSRF.
	defaultDoubaoSearchURL = "https://open.feedcoopapi.com/search_api/web_search"
	defaultDoubaoTimeout   = 30 * time.Second
	defaultDoubaoResults   = 10
	maxDoubaoResults       = 50
	// Full markdown content makes Doubao responses much larger than
	// snippet-only providers, so the read cap is doubled.
	maxDoubaoResponseBytes = 8 << 20
)

// doubaoTimeRangePattern matches the API's server-side date-range filter,
// e.g. "2026-09-12..2026-09-14".
var doubaoTimeRangePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})\.\.(\d{4}-\d{2}-\d{2})$`)

// DoubaoProvider implements web search using the Volcano Doubao Search
// Custom Edition API.
type DoubaoProvider struct {
	client      *http.Client
	baseURL     string
	apiKey      string
	timeRange   string
	needContent bool
}

func NewDoubaoProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateDoubaoParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultDoubaoTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &DoubaoProvider{
		client:      client,
		baseURL:     defaultDoubaoSearchURL,
		apiKey:      strings.TrimSpace(params.APIKey),
		timeRange:   doubaoTimeRange(params.ExtraConfig),
		needContent: doubaoNeedContent(params.ExtraConfig),
	}, nil
}

func ValidateDoubaoParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for Doubao provider")
	}
	if timeRange := strings.TrimSpace(params.ExtraConfig["time_range"]); timeRange != "" {
		match := doubaoTimeRangePattern.FindStringSubmatch(timeRange)
		if match == nil {
			return fmt.Errorf("invalid Doubao time_range (want YYYY-MM-DD..YYYY-MM-DD): %s", timeRange)
		}
		start, startErr := time.Parse("2006-01-02", match[1])
		end, endErr := time.Parse("2006-01-02", match[2])
		if startErr != nil || endErr != nil || start.After(end) {
			return fmt.Errorf("invalid Doubao time_range (start must not be after end): %s", timeRange)
		}
	}
	return nil
}

func doubaoTimeRange(extraConfig map[string]string) string {
	return strings.TrimSpace(extraConfig["time_range"])
}

func doubaoNeedContent(extraConfig map[string]string) bool {
	return strings.TrimSpace(extraConfig["need_content"]) != "false"
}

func (p *DoubaoProvider) Name() string { return "doubao" }

func (p *DoubaoProvider) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultDoubaoResults
	}
	if maxResults > maxDoubaoResults {
		maxResults = maxDoubaoResults
	}

	body, err := json.Marshal(doubaoSearchRequest{
		Query:          query,
		SearchType:     "web",
		Count:          maxResults,
		TimeRange:      p.timeRange,
		Filter:         doubaoSearchFilter{NeedContent: p.needContent, NeedUrl: true},
		ContentFormats: "markdown",
		QueryControl:   doubaoQueryControl{QueryRewrite: false},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Doubao request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create Doubao request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][Doubao] query=%q maxResults=%d timeRange=%s needContent=%t", query, maxResults, p.timeRange, p.needContent)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Doubao request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := readDoubaoResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, doubaoHTTPError(resp.StatusCode, respBody)
	}

	var response doubaoSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal Doubao response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, len(response.Result.WebResults))
	for _, item := range response.Result.WebResults {
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.Url) == "" {
			continue
		}
		// The API returns both a Snippet and a longer Summary; prefer the
		// summary for the snippet slot, mirroring the Bocha adapter.
		snippet := strings.TrimSpace(item.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.Url,
			Snippet: snippet,
			Content: strings.TrimSpace(item.Content),
			Source:  "doubao",
		}
		if includeDate {
			if publishedAt, ok := parseDoubaoPublishTime(item.PublishTime); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][Doubao] returned %d results", len(results))
	return results, nil
}

func readDoubaoResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxDoubaoResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read Doubao response: %w", err)
	}
	if len(body) > maxDoubaoResponseBytes {
		return nil, fmt.Errorf("Doubao response exceeds %d bytes", maxDoubaoResponseBytes)
	}
	return body, nil
}

func doubaoHTTPError(statusCode int, body []byte) error {
	var apiError struct {
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	if json.Unmarshal(body, &apiError) == nil {
		detail := strings.TrimSpace(apiError.Message)
		if detail == "" {
			detail = strings.TrimSpace(apiError.Msg)
		}
		if detail != "" {
			return fmt.Errorf("Doubao API returned status %d: %s", statusCode, detail)
		}
	}
	detail := strings.TrimSpace(string(body))
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	if detail == "" {
		return fmt.Errorf("Doubao API returned status %d", statusCode)
	}
	return fmt.Errorf("Doubao API returned status %d: %s", statusCode, detail)
}

// parseDoubaoPublishTime parses the API's ISO-8601 PublishTime, which carries
// an explicit offset (e.g. "2026-09-14T18:39:00+08:00"); a date-only value is
// also tolerated.
func parseDoubaoPublishTime(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

type doubaoSearchRequest struct {
	Query          string             `json:"Query"`
	SearchType     string             `json:"SearchType"`
	Count          int                `json:"Count"`
	TimeRange      string             `json:"TimeRange,omitempty"`
	Filter         doubaoSearchFilter `json:"Filter"`
	ContentFormats string             `json:"ContentFormats"`
	QueryControl   doubaoQueryControl `json:"QueryControl"`
}

type doubaoSearchFilter struct {
	NeedContent bool `json:"NeedContent"`
	NeedUrl     bool `json:"NeedUrl"`
}

type doubaoQueryControl struct {
	QueryRewrite bool `json:"QueryRewrite"`
}

type doubaoSearchResponse struct {
	Result struct {
		ResultCount int               `json:"ResultCount"`
		WebResults  []doubaoWebResult `json:"WebResults"`
	} `json:"Result"`
}

type doubaoWebResult struct {
	Title       string `json:"Title"`
	SiteName    string `json:"SiteName"`
	Url         string `json:"Url"`
	Snippet     string `json:"Snippet"`
	Summary     string `json:"Summary"`
	Content     string `json:"Content"`
	PublishTime string `json:"PublishTime"`
}
