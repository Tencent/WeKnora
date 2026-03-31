package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WebSearchProvider defines the interface for web search providers
type WebSearchProvider interface {
	// Name returns the name of the provider
	Name() string
	// Search performs a web search
	Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error)
}

// WebSearchService defines the interface for web search services
type WebSearchService interface {
	// Search performs a web search using the provider entity identified by providerID.
	// If providerID is empty, it falls back to the deprecated config.Provider field for backward compatibility.
	Search(ctx context.Context, providerID string, config *types.WebSearchConfig, query string) ([]*types.WebSearchResult, error)
	// CompressWithRAG performs RAG-based compression using a temporary, hidden knowledge base
	// The temporary knowledge base is deleted after use. The UI will not list it due to repo filtering.
	CompressWithRAG(ctx context.Context, sessionID string, tempKBID string, questions []string,
		webSearchResults []*types.WebSearchResult, cfg *types.WebSearchConfig,
		kbSvc KnowledgeBaseService, knowSvc KnowledgeService,
		seenURLs map[string]bool, knowledgeIDs []string,
	) (compressed []*types.WebSearchResult, kbID string, newSeen map[string]bool, newIDs []string, err error)
}
