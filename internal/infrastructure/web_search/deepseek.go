package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// Official endpoints. Hardcoded so tenants cannot redirect this client.
	// https://api-docs.deepseek.com/api/search
	// https://api-docs.deepseek.com/api/fetch
	deepseekAPIBase            = "https://api.deepseek.com"
	deepseekSearchPath         = "/v1/tools/search"
	deepseekFetchPath          = "/v1/tools/fetch"
	defaultDeepSeekResults     = 10
	maxDeepSeekResults         = 50
	maxDeepSeekQueryRunes      = 128
	maxDeepSeekFetchURLRunes   = 8192
	maxDeepSeekResponseBytes   = 2 << 20
	defaultDeepSeekHTTPTimeout = 60 * time.Second
)

// DeepSeekProvider calls DeepSeek Search and Fetch.
// Search discovers pages. Fetch reads one URL as Markdown.
type DeepSeekProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	// checkTarget rejects private targets before a URL leaves for DeepSeek.
	// Nil uses utils.ValidateURLForSSRF.
	checkTarget func(string) error
}

// NewDeepSeekProvider creates the DeepSeek search client from the saved API key.
func NewDeepSeekProvider(params types.WebSearchProviderParameters) (interfaces.WebSearcher, error) {
	return newDeepSeekProvider(params)
}

// NewDeepSeekFetch creates the DeepSeek fetch client from the saved API key.
func NewDeepSeekFetch(params types.WebSearchProviderParameters) (interfaces.WebFetcher, error) {
	return newDeepSeekProvider(params)
}

func newDeepSeekProvider(params types.WebSearchProviderParameters) (*DeepSeekProvider, error) {
	if err := ValidateDeepSeekParameters(params); err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(max(webfetch.PageFetchTimeout(), defaultDeepSeekHTTPTimeout), params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &DeepSeekProvider{
		client:  client,
		baseURL: deepseekAPIBase,
		apiKey:  strings.TrimSpace(params.APIKey),
	}, nil
}

// ValidateDeepSeekParameters requires the API key configured on the provider.
func ValidateDeepSeekParameters(params types.WebSearchProviderParameters) error {
	if strings.TrimSpace(params.APIKey) == "" {
		return fmt.Errorf("API key is required for DeepSeek provider")
	}
	return nil
}

// Name returns the provider identifier.
func (p *DeepSeekProvider) Name() string { return "deepseek" }

// Search queries POST /v1/tools/search.
func (p *DeepSeekProvider) Search(
	ctx context.Context, request types.WebSearchRequest,
) ([]*types.WebSearchResult, error) {
	query, maxResults, includeDate := request.Query, request.MaxResults, request.IncludeDate
	prepared, truncated := normalizeDeepSeekQuery(query)
	if prepared == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if truncated {
		logger.Infof(ctx, "[WebSearch][DeepSeek] truncated query to %d characters", maxDeepSeekQueryRunes)
	}
	if maxResults <= 0 {
		maxResults = defaultDeepSeekResults
	}
	if maxResults > maxDeepSeekResults {
		maxResults = maxDeepSeekResults
	}

	status, body, err := p.postJSON(ctx, deepseekSearchPath, deepseekSearchRequest{
		Query: prepared,
		Count: maxResults,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to execute DeepSeek search: %w", err)
	}
	if status != http.StatusOK {
		return nil, deepseekHTTPError(status, body)
	}
	var response deepseekSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DeepSeek search response: %w", err)
	}
	if message := strings.TrimSpace(response.Error.Message); message != "" && len(response.Results) == 0 {
		return nil, fmt.Errorf("DeepSeek API error: %s", message)
	}

	results := make([]*types.WebSearchResult, 0, len(response.Results))
	for _, item := range response.Results {
		pageURL := strings.TrimSpace(item.URL)
		if pageURL == "" {
			continue
		}
		snippet := strings.TrimSpace(item.Summary)
		if snippet == "" {
			snippet = strings.TrimSpace(item.Snippet)
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     pageURL,
			Snippet: snippet,
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
	logger.Infof(ctx, "[WebSearch][DeepSeek] query=%q returned %d results", prepared, len(results))
	return results, nil
}

// Fetch reads one page through POST /v1/tools/fetch and returns Markdown.
func (p *DeepSeekProvider) Fetch(ctx context.Context, pageURL string) (*types.WebPage, error) {
	pageURL = strings.TrimSpace(pageURL)
	parsed, err := validateDeepSeekFetchURL(pageURL)
	if err != nil {
		return nil, err
	}
	checkTarget := p.checkTarget
	if checkTarget == nil {
		checkTarget = utils.ValidateURLForSSRF
	}
	if err := checkTarget(pageURL); err != nil {
		return nil, &webfetch.FetchError{
			Code: webfetch.ErrorSSRFRejected, Retryable: false,
			Err: fmt.Errorf("URL rejected: %w", err),
		}
	}
	status, body, err := p.postJSON(ctx, deepseekFetchPath, deepseekFetchRequest{URL: pageURL})
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return nil, &webfetch.FetchError{
				Code: webfetch.ErrorBodyTooLarge, Retryable: false,
				Err: fmt.Errorf("DeepSeek fetch response exceeds %d bytes", maxDeepSeekResponseBytes),
			}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, err
		}
		return nil, fmt.Errorf("failed to execute DeepSeek fetch: %w", err)
	}
	if status != http.StatusOK {
		return nil, deepseekFetchStatusError(status, body)
	}
	var response deepseekFetchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DeepSeek fetch response: %w", err)
	}
	message := strings.TrimSpace(response.Error.Message)
	if message != "" && strings.TrimSpace(response.Content) == "" {
		return nil, &webfetch.FetchError{
			Code: webfetch.ErrorHTTPStatus, Retryable: false,
			Err: fmt.Errorf("DeepSeek API error: %s", message),
		}
	}
	content := strings.TrimSpace(response.Content)
	if content == "" {
		return nil, &webfetch.FetchError{
			Code: webfetch.ErrorEmptyContent, Retryable: false,
			Err: fmt.Errorf("DeepSeek fetch returned empty content"),
		}
	}
	logger.Infof(ctx, "[WebFetch][DeepSeek] fetched host=%s → %d chars", parsed.Hostname(), len(content))
	return &types.WebPage{Title: strings.TrimSpace(response.Title), Content: content, Source: p.Name()}, nil
}

func (p *DeepSeekProvider) postJSON(ctx context.Context, path string, payload any) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := readCapped(resp.Body, maxDeepSeekResponseBytes)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func normalizeDeepSeekQuery(query string) (string, bool) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) <= maxDeepSeekQueryRunes {
		return query, false
	}
	runes := []rune(query)
	return string(runes[:maxDeepSeekQueryRunes]), true
}

func validateDeepSeekFetchURL(rawURL string) (*url.URL, error) {
	invalid := func(format string, args ...any) error {
		return &webfetch.FetchError{
			Code: webfetch.ErrorInvalidURL, Retryable: false,
			Err: fmt.Errorf(format, args...),
		}
	}
	if rawURL == "" {
		return nil, invalid("url is empty")
	}
	if utf8.RuneCountInString(rawURL) > maxDeepSeekFetchURLRunes {
		return nil, invalid("url exceeds %d characters", maxDeepSeekFetchURLRunes)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, invalid("invalid URL: %v", err)
	}
	if parsed.User != nil {
		return nil, invalid("url must not contain a username or password")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, invalid("url must be an HTTP or HTTPS URL with a host")
	}
	return parsed, nil
}

func parseDeepSeekDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func deepseekHTTPError(statusCode int, body []byte) error {
	if message := deepseekErrorMessage(body); message != "" {
		return fmt.Errorf("DeepSeek API returned status %d: %s", statusCode, message)
	}
	detail := errorPreviewString(body)
	if detail == "" {
		return fmt.Errorf("DeepSeek API returned status %d", statusCode)
	}
	return fmt.Errorf("DeepSeek API returned status %d: %s", statusCode, detail)
}

func deepseekFetchStatusError(statusCode int, body []byte) error {
	code, retryable := webfetch.ErrorHTTPStatus, false
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity:
		code = webfetch.ErrorInvalidURL
	case http.StatusUnauthorized, http.StatusForbidden:
		// DeepSeek rejected the API key; the target page was never requested.
		return &webfetch.FetchError{Code: webfetch.ErrorProviderAuth, Retryable: false, Err: fmt.Errorf(
			"DeepSeek fetch API rejected the configured API key: %w", deepseekHTTPError(statusCode, body))}
	case http.StatusTooManyRequests:
		code, retryable = webfetch.ErrorHTTP429, true
	default:
		if statusCode >= 500 {
			code, retryable = webfetch.ErrorHTTP5xx, true
		}
	}
	return &webfetch.FetchError{Code: code, Retryable: retryable, Err: deepseekHTTPError(statusCode, body)}
}

func deepseekErrorMessage(body []byte) string {
	var payload struct {
		Error   deepseekErrorBody `json:"error"`
		Message string            `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if message := strings.TrimSpace(payload.Error.Message); message != "" {
		return message
	}
	return strings.TrimSpace(payload.Message)
}

type deepseekSearchRequest struct {
	Query string `json:"query"`
	Count int    `json:"count,omitempty"`
}

// deepseekErrorBody accepts both {"error":{"message":"..."}} and {"error":"..."}.
type deepseekErrorBody struct {
	Message string
}

func (e *deepseekErrorBody) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		e.Message = text
		return nil
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &object) == nil {
		e.Message = object.Message
	}
	// Any other shape carries no message; it must not fail the whole response.
	return nil
}

type deepseekSearchResponse struct {
	Results []deepseekSearchResult `json:"results"`
	Error   deepseekErrorBody      `json:"error"`
}

type deepseekSearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Summary string `json:"summary"`
	Content string `json:"content"`
	Date    string `json:"date"`
}

type deepseekFetchRequest struct {
	URL string `json:"url"`
}

type deepseekFetchResponse struct {
	Title   string            `json:"title"`
	Content string            `json:"content"`
	Error   deepseekErrorBody `json:"error"`
}
