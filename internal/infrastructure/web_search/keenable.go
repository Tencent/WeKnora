package web_search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultKeenableBaseURL is the hardcoded Keenable API base URL.
	// Not configurable by tenants — prevents SSRF.
	defaultKeenableBaseURL = "https://api.keenable.ai"
	// keenableTitle is the attribution tag Keenable segments integration traffic by.
	keenableTitle = "WeKnora"
)

var (
	defaultKeenableTimeout = 15 * time.Second
)

// KeenableProvider implements web search using the Keenable Search API.
// Keyless by default: with no API key it calls the public endpoint
// (rate-limited); a key switches to the authenticated endpoint and lifts the cap.
type KeenableProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewKeenableProvider creates a new Keenable provider from parameters.
// The API key is optional; Keenable works keyless against the public endpoint.
func NewKeenableProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	client, err := NewSearchHTTPClient(defaultKeenableTimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &KeenableProvider{
		client:  client,
		baseURL: defaultKeenableBaseURL,
		apiKey:  params.APIKey,
	}, nil
}

// Name returns the provider name
func (p *KeenableProvider) Name() string {
	return "keenable"
}

// Search performs a web search using the Keenable Search API.
func (p *KeenableProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}

	// Keyless by default; a configured key switches to the authenticated path.
	path := "/v1/search/public"
	if p.apiKey != "" {
		path = "/v1/search"
	}
	endpoint := p.baseURL + path
	logger.Infof(ctx, "[WebSearch][Keenable] query=%q maxResults=%d url=%s", query, maxResults, endpoint)

	bodyBytes, err := json.Marshal(keenableSearchRequest{Query: query, Mode: "pro"})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Keenable-Title", keenableTitle)
	if p.apiKey != "" {
		req.Header.Set("X-API-Key", p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logger.Warnf(ctx, "[WebSearch][Keenable] API returned status %d: %s", resp.StatusCode, string(respBody))
		return nil, fmt.Errorf("keenable API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var respData keenableSearchResponse
	if err := json.Unmarshal(respBody, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, len(respData.Results))
	for _, item := range respData.Results {
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.URL,
			Snippet: item.Description,
			Source:  "keenable",
		}
		if includeDate && item.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, item.PublishedAt); err == nil {
				result.PublishedAt = &t
			}
		}
		results = append(results, result)
	}
	logger.Infof(ctx, "[WebSearch][Keenable] returned %d results", len(results))
	return results, nil
}

// keenableSearchRequest defines the request body for the Keenable search API.
type keenableSearchRequest struct {
	Query string `json:"query"`
	Mode  string `json:"mode"`
}

// keenableSearchResponse defines the response structure for the Keenable search API.
type keenableSearchResponse struct {
	Query   string `json:"query"`
	Results []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
		PublishedAt string `json:"published_at,omitempty"`
		AcquiredAt  string `json:"acquired_at,omitempty"`
	} `json:"results"`
}
