package web_search

import (
	"context"
	"fmt"

	"google.golang.org/api/customsearch/v1"
	"google.golang.org/api/option"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// GoogleProvider implements web search using Google Custom Search Engine API
type GoogleProvider struct {
	srv      *customsearch.Service
	apiKey   string
	engineID string
}

// NewGoogleProvider creates a new Google provider from parameters (no environment variables).
// The API endpoint is the official Google Custom Search endpoint — not tenant-configurable.
func NewGoogleProvider(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
	if params.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Google provider")
	}
	if params.EngineID == "" {
		return nil, fmt.Errorf("engine ID is required for Google provider")
	}

	clientOpts := []option.ClientOption{
		option.WithAPIKey(params.APIKey),
	}
	srv, err := customsearch.NewService(context.Background(), clientOpts...)
	if err != nil {
		return nil, err
	}
	return &GoogleProvider{
		srv:      srv,
		apiKey:   params.APIKey,
		engineID: params.EngineID,
	}, nil
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
	logger.Infof(ctx, "[WebSearch][Google] query=%q maxResults=%d engineID=%s", query, maxResults, p.engineID)
	cseCall := p.srv.Cse.List().Context(ctx).Cx(p.engineID).Q(query)

	if maxResults > 0 {
		cseCall = cseCall.Num(int64(maxResults))
	} else {
		cseCall = cseCall.Num(5)
	}
	cseCall = cseCall.Hl("ch-zh")

	resp, err := cseCall.Do()
	if err != nil {
		logger.Warnf(ctx, "[WebSearch][Google] failed: %v", err)
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
	logger.Infof(ctx, "[WebSearch][Google] returned %d results", len(results))
	return results, nil
}
