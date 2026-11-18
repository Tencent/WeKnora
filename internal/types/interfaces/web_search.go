package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WebSearcher runs one search against a provider.
type WebSearcher interface {
	Search(ctx context.Context, req types.WebSearchRequest) ([]*types.WebSearchResult, error)
}

// WebFetcher reads one page.
type WebFetcher interface {
	Fetch(ctx context.Context, pageURL string) (*types.WebPage, error)
}

// WebClient is one stored provider bound to its registered clients and the tenant's policy.
type WebClient interface {
	WebSearcher
	// Fetch uses the provider's own reader when it has one, and the local reader otherwise.
	WebFetcher
	Capabilities() types.WebSearchCapabilities
}

// WebSearchService resolves the web provider for a request.
type WebSearchService interface {
	// Resolve loads providerID for the tenant in ctx once. An empty providerID is an error;
	// callers select the tenant's default WebSearchProviderEntity before calling Resolve.
	Resolve(ctx context.Context, providerID string) (WebClient, error)
}

// WebResultCompressor condenses web results through a temporary, hidden knowledge base.
type WebResultCompressor interface {
	// Compress updates state in place so later calls in the session reuse the knowledge base.
	Compress(ctx context.Context, state *types.WebSearchCompressionState, questions []string,
		results []*types.WebSearchResult, cfg *types.WebSearchConfig) ([]*types.WebSearchResult, error)
}
