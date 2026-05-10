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

const defaultSearxngTimeout = 15 * time.Second

// SearxngProvider implements web search using a self-hosted SearXNG instance.
//
// Unlike commercial providers, SearXNG is self-hosted, so the instance URL is
// supplied by the tenant via WebSearchProviderParameters.BaseURL. The URL is
// validated with utils.ValidateURLForSSRF; private/loopback hosts must be added
// to the SSRF_WHITELIST environment variable.
//
// API key is optional and only required when the SearXNG instance enables it
// (server.secret_key with limiter / json format restrictions).
type SearxngProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewSearxngProvider builds a SearXNG provider from tenant parameters.
func NewSearxngProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	base := strings.TrimSpace(params.BaseURL)
	if base == "" {
		return nil, fmt.Errorf("base_url is required for SearXNG provider")
	}
	if err := utils.ValidateURLForSSRF(base); err != nil {
		return nil, fmt.Errorf("invalid SearXNG base_url: %w", err)
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid SearXNG base_url: must be an absolute http(s) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid SearXNG base_url scheme: %s", parsed.Scheme)
	}

	client, err := NewSearchHTTPClient(defaultSearxngTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &SearxngProvider{
		client:  client,
		baseURL: strings.TrimRight(base, "/"),
		apiKey:  strings.TrimSpace(params.APIKey),
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
	q.Set("safesearch", "1")
	q.Set("language", "auto")

	reqURL := p.baseURL + "/search?" + q.Encode()
	logger.Infof(ctx, "[WebSearch][SearXNG] query=%q maxResults=%d url=%s", query, maxResults, p.baseURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora/1.0")
	if p.apiKey != "" {
		// SearXNG does not have a built-in auth header; the api_key field is reused
		// to support reverse-proxies that gate access via Authorization.
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

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
			if t, err := time.Parse(time.RFC3339, r.PublishedDate); err == nil {
				item.PublishedAt = &t
			}
		}
		results = append(results, item)
	}
	logger.Infof(ctx, "[WebSearch][SearXNG] returned %d results", len(results))
	return results, nil
}

type searxngResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Content       string `json:"content"`
		PublishedDate string `json:"publishedDate,omitempty"`
	} `json:"results"`
}
