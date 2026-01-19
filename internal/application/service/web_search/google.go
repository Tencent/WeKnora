package web_search

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// GoogleProvider implements web search using Google Custom Search Engine API
type GoogleProvider struct {
	srv      *customsearch.Service
	apiKey   string
	engineID string
	baseURL  string
}

// NewGoogleProvider creates a new Google provider
func NewGoogleProvider() (interfaces.WebSearchProvider, error) {
	apiURL := os.Getenv("GOOGLE_SEARCH_API_URL")
	if apiURL == "" {
		return nil, fmt.Errorf("GOOGLE_SEARCH_API_URL environment variable is not set")
	}

	u, err := url.Parse(apiURL)
	if err != nil {
		return nil, err
	}
	engineID := u.Query().Get("engine_id")
	if engineID == "" {
		return nil, fmt.Errorf("engine_id is empty")
	}
	apiKey := u.Query().Get("api_key")
	if apiKey == "" {
		return nil, fmt.Errorf("api_key is empty")
	}
	clientOpts := make([]option.ClientOption, 0)
	clientOpts = append(clientOpts, option.WithAPIKey(apiKey))
	clientOpts = append(clientOpts, option.WithEndpoint(u.Scheme+"://"+u.Host))
	srv, err := customsearch.NewService(context.Background(), clientOpts...)
	if err != nil {
		return nil, err
	}
	return &GoogleProvider{
		srv:      srv,
		apiKey:   apiKey,
		engineID: engineID,
		baseURL:  apiURL,
	}, nil
}

// GoogleProviderInfo returns the provider info for registration
func GoogleProviderInfo() types.WebSearchProviderInfo {
	return types.WebSearchProviderInfo{
		ID:             "google",
		Name:           "Google",
		Free:           false,
		RequiresAPIKey: true,
		Description:    "Google Custom Search API",
	}
}

// Name returns the provider name
func (p *GoogleProvider) Name() string {
	return "google"
}

// Search performs a web search using Google Custom Search Engine API
func (p *GoogleProvider) Search(
	ctx context.Context,
	query string,
	maxResults int,
	includeDate bool,
) ([]*types.WebSearchResult, error) {
	if len(query) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	cseCall := p.srv.Cse.List().Context(ctx).Cx(p.engineID).Q(query)

	if maxResults > 0 {
		cseCall = cseCall.Num(int64(maxResults))
	} else {
		cseCall = cseCall.Num(5)
	}
	cseCall = cseCall.Hl("ch-zh")

	resp, err := cseCall.Do()
	if err != nil {
		return nil, err
	}
	results := make([]*types.WebSearchResult, 0)
	for _, item := range resp.Items {
		result := &types.WebSearchResult{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
			Source:  "google",
		}
		results = append(results, result)
	}
	return results, nil
}
