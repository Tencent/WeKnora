package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultDeepSeekSearchURL is the hardcoded DeepSeek Search API endpoint.
	// Not configurable by tenants — prevents SSRF.
	defaultDeepSeekSearchURL = "https://api.deepseek.com/v1/tools/search"
	defaultDeepSeekTimeout   = 30 * time.Second
	// defaultDeepSeekResults matches the API's own default count.
	defaultDeepSeekResults = 10
	// maxDeepSeekResults mirrors the API's upper bound on count; sending more
	// returns HTTP 400. The lower bound of 1 is satisfied by construction: a
	// non-positive caller value falls back to defaultDeepSeekResults.
	maxDeepSeekResults    = 50
	maxDeepSeekQueryRunes = 128
	// DeepSeek returns a 600–800 character summary per hit, and page content
	// when need_content is set, so a full page of results is far larger than
	// the metadata-only payloads of the other providers.
	maxDeepSeekResponseBytes = 16 << 20
)

const (
	// deepSeekSnippetShort uses the API's `snippet` (100–200 chars, meant for
	// display); deepSeekSnippetLong uses `summary` (600–800 chars, meant to be
	// read by a model).
	deepSeekSnippetShort = "snippet"
	deepSeekSnippetLong  = "summary"
)

var validDeepSeekSnippetSources = map[string]struct{}{
	deepSeekSnippetShort: {},
	deepSeekSnippetLong:  {},
}

// DeepSeekProvider implements web search using DeepSeek's standalone Search API.
//
// The API is OpenAI-compatible in its error envelope but not in its request
// shape: the query travels as `query` (not `search_query`) and the page size as
// `count` (not `max_results`). Unknown request fields are silently ignored
// upstream, so a misnamed field would look like an unfiltered search rather
// than an error — hence the explicit struct below.
type DeepSeekProvider struct {
	client        *http.Client
	baseURL       string
	apiKey        string
	snippetSource string
	needContent   bool
}

// NewDeepSeekProvider creates a DeepSeek web search provider from persisted parameters.
func NewDeepSeekProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if err := ValidateDeepSeekParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultDeepSeekTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	// The official endpoint does not redirect. Never forward the API key to a
	// redirect destination, even another public host.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	snippetSource, needContent := deepSeekOptions(params.ExtraConfig)
	return &DeepSeekProvider{
		client:        client,
		baseURL:       defaultDeepSeekSearchURL,
		apiKey:        strings.TrimSpace(params.APIKey),
		snippetSource: snippetSource,
		needContent:   needContent,
	}, nil
}

// ValidateDeepSeekParameters validates credentials and provider-specific options.
func ValidateDeepSeekParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for DeepSeek provider")
	}
	snippetSource, _ := deepSeekOptions(params.ExtraConfig)
	if _, ok := validDeepSeekSnippetSources[snippetSource]; !ok {
		return fmt.Errorf("invalid DeepSeek snippet source: %s", snippetSource)
	}
	return nil
}

func deepSeekOptions(extraConfig map[string]string) (snippetSource string, needContent bool) {
	snippetSource = deepSeekSnippetShort
	if value := strings.TrimSpace(extraConfig["snippet_source"]); value != "" {
		snippetSource = value
	}
	needContent = strings.EqualFold(strings.TrimSpace(extraConfig["need_content"]), "true")
	return snippetSource, needContent
}

// Name returns the provider name.
func (p *DeepSeekProvider) Name() string {
	return "deepseek"
}

// Search performs a web search using the DeepSeek Search API.
func (p *DeepSeekProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	preparedQuery := normalizeDeepSeekQuery(query)
	if preparedQuery == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if preparedQuery != strings.TrimSpace(query) {
		logger.Infof(ctx, "[WebSearch][DeepSeek] truncated query to %d characters", maxDeepSeekQueryRunes)
	}
	if maxResults <= 0 {
		maxResults = defaultDeepSeekResults
	}
	if maxResults > maxDeepSeekResults {
		maxResults = maxDeepSeekResults
	}

	body, err := json.Marshal(deepSeekSearchRequest{
		Query:       preparedQuery,
		Count:       maxResults,
		NeedContent: p.needContent,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal DeepSeek request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	logger.Infof(ctx, "[WebSearch][DeepSeek] query=%q maxResults=%d snippet=%s needContent=%t",
		preparedQuery, maxResults, p.snippetSource, p.needContent)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute DeepSeek request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readCapped(resp.Body, maxDeepSeekResponseBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read DeepSeek response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, deepSeekHTTPError(resp.StatusCode, respBody)
	}

	var response deepSeekSearchResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DeepSeek response: %w", err)
	}
	if response.Error != nil && (response.Error.Message != "" || response.Error.Code != "") {
		return nil, deepSeekAPIError(response.Error)
	}

	results := make([]*types.WebSearchResult, 0, len(response.Results))
	for _, item := range response.Results {
		if strings.TrimSpace(item.Title) == "" && strings.TrimSpace(item.URL) == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: deepSeekSnippet(item, p.snippetSource),
			Content: strings.TrimSpace(item.Content),
			Source:  "deepseek",
		}
		if includeDate {
			if publishedAt, ok := parseDeepSeekDate(item.Date); ok {
				result.PublishedAt = &publishedAt
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	// An empty result set is a legitimate answer, not a failure: DeepSeek bills
	// only non-empty 200 responses, and callers treat zero hits as "no sources".
	if len(results) == 0 {
		logger.Infof(ctx, "[WebSearch][DeepSeek] no results for query=%q", preparedQuery)
		return results, nil
	}
	logger.Infof(ctx, "[WebSearch][DeepSeek] returned %d results", len(results))
	return results, nil
}

// deepSeekSnippet picks the text field carried in the unified Snippet column.
// It falls back to `snippet` when the requested `summary` is absent.
func deepSeekSnippet(item deepSeekSearchResult, source string) string {
	if source == deepSeekSnippetLong {
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			return summary
		}
	}
	return strings.TrimSpace(item.Snippet)
}

// normalizeDeepSeekQuery trims and bounds the query to the API's 1–128 Unicode
// character window. A query that is only whitespace is reported as empty rather
// than sent, so it never becomes a 400.
func normalizeDeepSeekQuery(query string) string {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) <= maxDeepSeekQueryRunes {
		return query
	}
	return string([]rune(query)[:maxDeepSeekQueryRunes])
}

func parseDeepSeekDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" || value == "null" {
		return time.Time{}, false
	}
	// The API documents RFC 3339 and returns `null` when the publish time is
	// unknown; date-only strings appear in practice, so accept them too.
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

// deepSeekHTTPError turns a non-200 response into an actionable message. The
// status codes are the ones the API documents as operator-actionable; leaking
// the raw body is safe because the preview is bounded and UTF-8 trimmed.
func deepSeekHTTPError(statusCode int, body []byte) error {
	var envelope deepSeekSearchResponse
	if json.Unmarshal(body, &envelope) == nil && envelope.Error != nil &&
		strings.TrimSpace(envelope.Error.Message) != "" {
		return fmt.Errorf("%s (HTTP %d): %s", deepSeekStatusHint(statusCode), statusCode,
			strings.TrimSpace(envelope.Error.Message))
	}
	detail := errorPreviewString(body)
	if detail == "" {
		return fmt.Errorf("%s (HTTP %d)", deepSeekStatusHint(statusCode), statusCode)
	}
	return fmt.Errorf("%s (HTTP %d): %s", deepSeekStatusHint(statusCode), statusCode, detail)
}

// deepSeekAPIError reports an error envelope returned with HTTP 200.
func deepSeekAPIError(apiError *deepSeekError) error {
	message := strings.TrimSpace(apiError.Message)
	if message == "" {
		message = strings.TrimSpace(apiError.Code)
	}
	if message == "" {
		message = "unknown error"
	}
	return fmt.Errorf("DeepSeek Search API error (%s): %s", strings.TrimSpace(apiError.Type), message)
}

func deepSeekStatusHint(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized:
		return "DeepSeek API key is missing or invalid"
	case http.StatusPaymentRequired:
		return "DeepSeek account has insufficient balance"
	case http.StatusTooManyRequests:
		return "DeepSeek Search rate limit exceeded"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "DeepSeek Search request timed out"
	case http.StatusRequestEntityTooLarge:
		return "DeepSeek Search request body is too large"
	case http.StatusServiceUnavailable, http.StatusInternalServerError:
		return "DeepSeek Search service error"
	default:
		return "DeepSeek Search API returned status"
	}
}

type deepSeekSearchRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count"`
	NeedContent bool   `json:"need_content"`
}

type deepSeekSearchResponse struct {
	Results []deepSeekSearchResult `json:"results"`
	Error   *deepSeekError         `json:"error"`
}

type deepSeekSearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Snippet     string  `json:"snippet"`
	Summary     string  `json:"summary"`
	Content     string  `json:"content"`
	SiteName    string  `json:"site_name"`
	SiteIcon    string  `json:"site_icon"`
	Date        string  `json:"date"`
	Authority   int     `json:"authority"`
	RerankScore float64 `json:"rerank_score"`
}

type deepSeekError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param"`
	Code    string `json:"code"`
}
