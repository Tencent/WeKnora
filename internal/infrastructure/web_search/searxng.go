package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// defaultSearxngTimeout is sized slightly above the SearXNG image's default
// outgoing.max_request_timeout (10s in docker/searxng/settings.yml) so a slow
// upstream engine surfaces as a SearXNG-side error instead of a client cancel.
const defaultSearxngTimeout = 12 * time.Second

// SearxngProvider implements web search using a self-hosted SearXNG instance.
//
// Unlike commercial providers, SearXNG is self-hosted, so the instance URL is
// supplied by the tenant via WebSearchProviderParameters.BaseURL. The URL is
// validated with utils.ValidateURLForSSRF; private/loopback hosts must be added
// to the SSRF_WHITELIST environment variable.
type SearxngProvider struct {
	client  *http.Client
	baseURL string
}

// ValidateSearxngBaseURL validates a SearXNG instance URL: must be a non-empty,
// absolute http(s) URL, and must pass the SSRF whitelist check. Shared between
// the service-layer parameter validation and the provider constructor so that
// "save" and "use" never disagree.
func ValidateSearxngBaseURL(rawURL string) error {
	base := strings.TrimSpace(rawURL)
	if base == "" {
		return fmt.Errorf("base_url is required for SearXNG provider")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid SearXNG base_url: must be an absolute http(s) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("invalid SearXNG base_url scheme: %s", parsed.Scheme)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid SearXNG base_url: must not contain query or fragment")
	}
	if err := utils.ValidateURLForSSRF(base); err != nil {
		return fmt.Errorf("invalid SearXNG base_url: %w", err)
	}
	return nil
}

// NewSearxngProvider builds a SearXNG provider from tenant parameters.
func NewSearxngProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	base := strings.TrimSpace(params.BaseURL)
	if err := ValidateSearxngBaseURL(base); err != nil {
		return nil, err
	}

	client, err := NewSearchHTTPClient(defaultSearxngTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &SearxngProvider{
		client:  client,
		baseURL: strings.TrimRight(base, "/"),
	}, nil
}

// Name returns the provider name.
func (p *SearxngProvider) Name() string { return "searxng" }

// Search performs a metasearch query against the configured SearXNG instance.
// SearXNG must have `search.formats: [json]` enabled in settings.yml.
func (p *SearxngProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}

	q := url.Values{}
	q.Set("q", query)
	q.Set("format", "json")
	// Use "all" (SearXNG's documented value for "no language filter") instead
	// of "auto", which is a UI-side default and not a valid /search parameter.
	// safesearch is intentionally not set here so the value configured in the
	// instance's settings.yml is honored.
	q.Set("language", "all")

	reqURL := p.baseURL + "/search?" + q.Encode()
	logger.Infof(ctx, "[WebSearch][SearXNG] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("searxng returned status %d: %s", resp.StatusCode, string(body))
	}

	var data searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode SearXNG response (ensure JSON format is enabled in settings.yml): %w", err)
	}

	results := make([]*types.WebSearchResult, 0, maxResults)
	for _, r := range data.Results {
		if len(results) >= maxResults {
			break
		}
		if r.URL == "" || r.Title == "" {
			continue
		}
		item := &types.WebSearchResult{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: r.Content,
			Source:  "searxng",
		}
		if includeDate && r.PublishedDate != "" {
			if t, ok := parseSearxngDate(r.PublishedDate); ok {
				item.PublishedAt = &t
			} else {
				logger.Debugf(ctx, "[WebSearch][SearXNG] unparsable publishedDate=%q", r.PublishedDate)
			}
		}
		results = append(results, item)
	}
	if len(results) == 0 && len(data.UnresponsiveEngines) > 0 {
		logger.Warnf(ctx, "[WebSearch][SearXNG] empty results, unresponsive_engines=%v", data.UnresponsiveEngines)
	}
	logger.Infof(ctx, "[WebSearch][SearXNG] returned %d results", len(results))
	return results, nil
}

// searxngDateLayouts covers the formats different SearXNG engines emit for
// publishedDate. Order matters only for performance; first match wins.
// time.RFC3339 already accepts the nanosecond-precision form, so RFC3339Nano
// is intentionally omitted.
var searxngDateLayouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
	time.RFC1123Z,
	time.RFC1123,
}

func parseSearxngDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range searxngDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

type searxngResponse struct {
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"publishedDate,omitempty"`
	} `json:"results"`
	// UnresponsiveEngines is a list of [engine, reason] tuples returned by
	// SearXNG when one or more upstream engines fail. We surface it on empty
	// result sets to aid debugging.
	UnresponsiveEngines [][]string `json:"unresponsive_engines,omitempty"`
}
