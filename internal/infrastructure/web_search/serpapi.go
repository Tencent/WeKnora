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
)

const (
	defaultSerpAPIURL     = "https://serpapi.com/search.json"
	defaultSerpAPITimeout = 15 * time.Second
	defaultSerpAPIEngine  = "google"
	defaultSerpAPIResults = 5
	maxSerpAPIResults     = 20
)

var supportedSerpAPIEngines = map[string]struct{}{
	"google": {}, "google_news": {}, "google_scholar": {}, "google_patents": {},
	"bing": {}, "duckduckgo": {}, "google_images": {}, "google_videos": {}, "youtube": {},
}

// SerpAPIProvider implements web search using SerpApi's official endpoint.
type SerpAPIProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
	engine  string
}

// NewSerpAPIProvider creates a SerpApi provider from tenant parameters.
func NewSerpAPIProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if strings.TrimSpace(params.APIKey) == "" {
		return nil, fmt.Errorf("API key is required for SerpApi provider")
	}
	engine, err := normalizeSerpAPIEngine(params.ExtraConfig["engine"])
	if err != nil {
		return nil, err
	}
	client, err := NewSearchHTTPClient(defaultSerpAPITimeout, params.ProxyURL)
	if err != nil {
		return nil, err
	}
	return &SerpAPIProvider{
		client: client, baseURL: defaultSerpAPIURL,
		apiKey: params.APIKey, engine: engine,
	}, nil
}

func (p *SerpAPIProvider) Name() string { return "serpapi" }

func (p *SerpAPIProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is empty")
	}
	if maxResults <= 0 {
		maxResults = defaultSerpAPIResults
	}
	if maxResults > maxSerpAPIResults {
		maxResults = maxSerpAPIResults
	}

	u, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid SerpApi endpoint: %w", err)
	}
	q := u.Query()
	q.Set("engine", p.engine)
	q.Set("q", query)
	q.Set("api_key", p.apiKey)
	q.Set("num", fmt.Sprintf("%d", maxResults))
	u.RawQuery = q.Encode()

	logger.Infof(ctx, "[WebSearch][SerpApi] query=%q engine=%s maxResults=%d", query, p.engine, maxResults)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SerpApi request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute SerpApi request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read SerpApi response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("SerpApi returned status %d: %s", resp.StatusCode, truncateSerpAPIError(body))
	}

	var payload serpAPIResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal SerpApi response: %w", err)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("SerpApi error: %s", payload.Error)
	}

	items := payload.firstResultSet()
	results := make([]*types.WebSearchResult, 0, min(len(items), maxResults))
	for _, item := range items {
		link := firstNonEmpty(item.Link, item.URL, item.Original, item.Thumbnail)
		if strings.TrimSpace(link) == "" {
			continue
		}
		result := &types.WebSearchResult{
			Title: firstNonEmpty(item.Title, "Untitled"), URL: link,
			Snippet: firstNonEmpty(item.Snippet, item.Description), Source: "serpapi",
		}
		if includeDate {
			if parsed, ok := parseSerpAPIDate(item.Date); ok {
				result.PublishedAt = &parsed
			}
		}
		results = append(results, result)
		if len(results) >= maxResults {
			break
		}
	}
	logger.Infof(ctx, "[WebSearch][SerpApi] returned %d results", len(results))
	return results, nil
}

func normalizeSerpAPIEngine(engine string) (string, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	if engine == "" {
		return defaultSerpAPIEngine, nil
	}
	if _, ok := supportedSerpAPIEngines[engine]; !ok {
		return "", fmt.Errorf("unsupported SerpApi engine %q", engine)
	}
	return engine, nil
}

// ValidateSerpAPIEngine validates the optional engine selection before a
// provider is persisted. An empty value is accepted and resolves to Google.
func ValidateSerpAPIEngine(engine string) error {
	_, err := normalizeSerpAPIEngine(engine)
	return err
}

type serpAPIResponse struct {
	Error           string          `json:"error"`
	OrganicResults  []serpAPIResult `json:"organic_results"`
	NewsResults     []serpAPIResult `json:"news_results"`
	ImagesResults   []serpAPIResult `json:"images_results"`
	VideoResults    []serpAPIResult `json:"video_results"`
	VideosResults   []serpAPIResult `json:"videos_results"`
	ShoppingResults []serpAPIResult `json:"shopping_results"`
}

func (r serpAPIResponse) firstResultSet() []serpAPIResult {
	for _, items := range [][]serpAPIResult{
		r.OrganicResults, r.NewsResults, r.ImagesResults,
		r.VideoResults, r.VideosResults, r.ShoppingResults,
	} {
		if len(items) > 0 {
			return items
		}
	}
	return nil
}

type serpAPIResult struct {
	Title       string `json:"title"`
	Link        string `json:"link"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	Description string `json:"description"`
	Date        string `json:"date"`
	Original    string `json:"original"`
	Thumbnail   string `json:"thumbnail"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseSerpAPIDate(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{time.RFC3339, "2006-01-02", "Jan 2, 2006", "January 2, 2006"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func truncateSerpAPIError(body []byte) string {
	const max = 300
	value := strings.TrimSpace(string(body))
	if len(value) > max {
		return value[:max] + "..."
	}
	return value
}
