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

	"github.com/PuerkitoBio/goquery"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// DuckDuckGoProvider implements web search using DuckDuckGo (HTML first, API fallback)
type DuckDuckGoProvider struct {
	client *http.Client
}

// NewDuckDuckGoProvider creates a new DuckDuckGo provider
func NewDuckDuckGoProvider() (interfaces.WebSearchProvider, error) {
	return &DuckDuckGoProvider{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// DuckDuckGoProviderInfo returns the provider info for registration
func DuckDuckGoProviderInfo() types.WebSearchProviderInfo {
	return types.WebSearchProviderInfo{
		ID:             "duckduckgo",
		Name:           "DuckDuckGo",
		Free:           true,
		RequiresAPIKey: false,
		Description:    "DuckDuckGo Search API",
	}
}

// Name returns the provider name
func (p *DuckDuckGoProvider) Name() string {
	return "duckduckgo"
}

// Search performs a web search using DuckDuckGo HTML endpoint with API fallback
func (p *DuckDuckGoProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if maxResults <= 0 {
		maxResults = 5
	}
	// Try HTML scraping first (more reliable for general results)
	htmlResults, err := p.searchHTML(ctx, query, maxResults)
	if err == nil && len(htmlResults) > 0 {
		return htmlResults, nil
	}
	// Fallback to Instant Answer API
	apiResults, apiErr := p.searchAPI(ctx, query, maxResults)
	if apiErr == nil && len(apiResults) > 0 {
		return apiResults, nil
	}
	if err != nil {
		return nil, fmt.Errorf("duckduckgo HTML search failed: %w", err)
	}
	return nil, fmt.Errorf("duckduckgo API search failed: %w", apiErr)
}

// searchHTML performs a web search using DuckDuckGo HTML endpoint
func (p *DuckDuckGoProvider) searchHTML(
	ctx context.Context,
	query string,
	maxResults int,
) ([]*types.WebSearchResult, error) {
	baseURL := "https://html.duckduckgo.com/html/"
	params := url.Values{}
	params.Set("q", query)
	// Prefer Chinese results if applicable; otherwise DDG will auto-detect
	params.Set("kl", "cn-zh")

	reqURL := baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	// Use a realistic UA to avoid blocks
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	)

	// print curl of request
	curlCommand := fmt.Sprintf(
		"curl -X GET '%s' -H 'User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'",
		req.URL.String(),
	)
	logger.Infof(ctx, "Curl of request: %s", secutils.SanitizeForLog(curlCommand))

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("duckduckgo HTML returned status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, maxResults)
	// Structure based on DDG HTML page
	doc.Find(".web-result").Each(func(i int, s *goquery.Selection) {
		if len(results) >= maxResults {
			return
		}
		titleNode := s.Find(".result__a")
		title := strings.TrimSpace(titleNode.Text())
		var link string
		if href, exists := titleNode.Attr("href"); exists {
			link = cleanDDGURL(href)
		}
		snippet := strings.TrimSpace(s.Find(".result__snippet").Text())
		if title != "" && link != "" {
			results = append(results, &types.WebSearchResult{
				Title:   title,
				URL:     link,
				Snippet: snippet,
				Source:  "duckduckgo",
			})
		}
	})

	logger.Infof(ctx, "DuckDuckGo HTML search returned %d results for query: %s", len(results), query)
	return results, nil
}

// searchAPI performs a web search using DuckDuckGo API endpoint
func (p *DuckDuckGoProvider) searchAPI(
	ctx context.Context,
	query string,
	maxResults int,
) ([]*types.WebSearchResult, error) {
	baseURL := "https://api.duckduckgo.com/"
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("no_html", "1")
	params.Set("skip_disambig", "1")

	reqURL := baseURL + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "WeKnora/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("duckduckgo API returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResponse struct {
		AbstractText  string `json:"AbstractText"`
		AbstractURL   string `json:"AbstractURL"`
		Heading       string `json:"Heading"`
		RelatedTopics []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		} `json:"RelatedTopics"`
		Results []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		} `json:"Results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode API response: %w", err)
	}

	results := make([]*types.WebSearchResult, 0, maxResults)
	if apiResponse.AbstractText != "" && apiResponse.AbstractURL != "" {
		results = append(results, &types.WebSearchResult{
			Title:   apiResponse.Heading,
			URL:     apiResponse.AbstractURL,
			Snippet: apiResponse.AbstractText,
			Source:  "duckduckgo",
		})
	}
	for _, topic := range apiResponse.RelatedTopics {
		if len(results) >= maxResults {
			break
		}
		if topic.Text != "" && topic.FirstURL != "" {
			results = append(results, &types.WebSearchResult{
				Title:   extractTitle(topic.Text),
				URL:     topic.FirstURL,
				Snippet: topic.Text,
				Source:  "duckduckgo",
			})
		}
	}
	for _, r := range apiResponse.Results {
		if len(results) >= maxResults {
			break
		}
		if r.Text != "" && r.FirstURL != "" {
			results = append(results, &types.WebSearchResult{
				Title:   extractTitle(r.Text),
				URL:     r.FirstURL,
				Snippet: r.Text,
				Source:  "duckduckgo",
			})
		}
	}

	logger.Infof(ctx, "DuckDuckGo API search returned %d results for query: %s", len(results), query)
	return results, nil
}

// cleanDDGURL cleans the URL from DuckDuckGo HTML endpoint
func cleanDDGURL(urlStr string) string {
	if strings.HasPrefix(urlStr, "//duckduckgo.com/l/?uddg=") {
		trimmed := strings.TrimPrefix(urlStr, "//duckduckgo.com/l/?uddg=")
		if idx := strings.Index(trimmed, "&rut="); idx != -1 {
			decodedStr, err := url.PathUnescape(trimmed[:idx])
			if err == nil {
				return decodedStr
			}
			return ""
		}
	}
	if strings.HasPrefix(urlStr, "https://duckduckgo.com/l/?uddg=") {
		if parsedURL, err := url.Parse(urlStr); err == nil {
			if uddg := parsedURL.Query().Get("uddg"); uddg != "" {
				return uddg
			}
		}
	}
	return urlStr
}

// extractTitle extracts the title from the text
func extractTitle(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 {
		title := strings.TrimSpace(lines[0])
		if len(title) > 100 {
			title = title[:100] + "..."
		}
		return title
	}
	return strings.TrimSpace(text)
}
