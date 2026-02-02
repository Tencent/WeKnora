package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// defaultBingSearchURL is the default Bing search API URL.
	// Reference: https://learn.microsoft.com/en-us/previous-versions/bing/search-apis/bing-web-search/reference/endpoints
	defaultBingSearchURL = "https://api.bing.microsoft.com/v7.0/search"
)

var (
	// defaultUserAgentHeader for PC. https://learn.microsoft.com/en-us/previous-versions/bing/search-apis/bing-web-search/reference/headers
	defaultUserAgentHeader = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/139.0.0.0 Safari/537.36"
	defaultBingTimeout     = 10 * time.Second
)

type bingSafeSearch string

const (
	bingSafeSearchOff      bingSafeSearch = "Off"
	bingSafeSearchModerate bingSafeSearch = "Moderate"
	bingSafeSearchStrict   bingSafeSearch = "Strict"
)

type bingFreshness string

const (
	bingFreshnessDay   = "Day"
	bingFreshnessWeek  = "Week"
	bingFreshnessMonth = "Month"
)

// BingProvider implements web search using Bing Search API
type BingProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

// NewBingProvider creates a new Bing provider
func NewBingProvider() (interfaces.WebSearchProvider, error) {
	apiKey := os.Getenv("BING_SEARCH_API_KEY")
	if len(apiKey) == 0 {
		return nil, fmt.Errorf("BING_SEARCH_API_KEY is not set")
	}
	client := &http.Client{
		Timeout: defaultBingTimeout,
	}
	return &BingProvider{
		client:  client,
		baseURL: defaultBingSearchURL,
		apiKey:  apiKey,
	}, nil
}

// BingProviderInfo returns the provider info for registration
func BingProviderInfo() types.WebSearchProviderInfo {
	return types.WebSearchProviderInfo{
		ID:             "bing",
		Name:           "Bing",
		Free:           false,
		RequiresAPIKey: true,
		Description:    "Bing Search API",
	}
}

// Name returns the provider name
func (p *BingProvider) Name() string {
	return "bing"
}

// Search performs a web search using Bing Search API
func (p *BingProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	req, err := p.buildParams(ctx, query, maxResults, includeDate)
	if err != nil {
		return nil, err
	}
	return p.doSearch(ctx, req)
}

func (p *BingProvider) doSearch(ctx context.Context, req *http.Request) ([]*types.WebSearchResult, error) {
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var respData bingSearchResponse
	if err := json.Unmarshal(body, &respData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	results := make([]*types.WebSearchResult, 0, len(respData.WebPages.Value))
	for _, item := range respData.WebPages.Value {
		results = append(results, &types.WebSearchResult{
			Title:       item.Name,
			URL:         item.URL,
			Snippet:     item.Snippet,
			Source:      "bing",
			PublishedAt: &item.DateLastCrawled,
		})
	}
	return results, nil
}

// bingSearchResponse defines the response structure for Bing search API.
// ref: https://learn.microsoft.com/en-us/previous-versions/bing/search-apis/bing-web-search/quickstarts/rest/go
type bingSearchResponse struct {
	Type         string `json:"_type"`
	QueryContext struct {
		OriginalQuery string `json:"originalQuery"`
	} `json:"queryContext"`
	WebPages struct {
		WebSearchURL          string `json:"webSearchUrl"`
		TotalEstimatedMatches int    `json:"totalEstimatedMatches"`
		Value                 []struct {
			ID               string    `json:"id"`
			Name             string    `json:"name"`
			URL              string    `json:"url"`
			IsFamilyFriendly bool      `json:"isFamilyFriendly"`
			DisplayURL       string    `json:"displayUrl"`
			Snippet          string    `json:"snippet"`
			DateLastCrawled  time.Time `json:"dateLastCrawled"`
			SearchTags       []struct {
				Name    string `json:"name"`
				Content string `json:"content"`
			} `json:"searchTags,omitempty"`
			About []struct {
				Name string `json:"name"`
			} `json:"about,omitempty"`
		} `json:"value"`
	} `json:"webPages"`
	RelatedSearches struct {
		ID    string `json:"id"`
		Value []struct {
			Text         string `json:"text"`
			DisplayText  string `json:"displayText"`
			WebSearchURL string `json:"webSearchUrl"`
		} `json:"value"`
	} `json:"relatedSearches"`
	RankingResponse struct {
		Mainline struct {
			Items []struct {
				AnswerType  string `json:"answerType"`
				ResultIndex int    `json:"resultIndex"`
				Value       struct {
					ID string `json:"id"`
				} `json:"value"`
			} `json:"items"`
		} `json:"mainline"`
		Sidebar struct {
			Items []struct {
				AnswerType string `json:"answerType"`
				Value      struct {
					ID string `json:"id"`
				} `json:"value"`
			} `json:"items"`
		} `json:"sidebar"`
	} `json:"rankingResponse"`
}

// buildParams builds the request parameters for Bing search API.
// ref: https://learn.microsoft.com/en-us/previous-versions/bing/search-apis/bing-web-search/quickstarts/rest/go
func (p *BingProvider) buildParams(ctx context.Context, query string, maxResults int, includeDate bool) (*http.Request, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("count", strconv.Itoa(maxResults))

	queryURL := fmt.Sprintf("%s?%s", p.baseURL, params.Encode())
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgentHeader)
	req.Header.Set("Ocp-Apim-Subscription-Key", p.apiKey)
	return req, nil
}
