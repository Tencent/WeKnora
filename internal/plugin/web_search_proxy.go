package plugin

import (
	"context"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pkg/pluginapi"
)

// GRPCWebSearchProxy adapts the public web-search plugin protocol to the
// tenant-scoped WebSearchProvider interface used by WeKnora.
type GRPCWebSearchProxy struct {
	Client    pluginapi.WebSearchPluginClient
	Params    types.WebSearchProviderParameters
	NameValue string
	Manager   *Manager
	PluginID  string
}

func (p *GRPCWebSearchProxy) Name() string {
	if p == nil || p.NameValue == "" {
		return "external"
	}
	return p.NameValue
}

func (p *GRPCWebSearchProxy) Search(ctx context.Context, query string, maxResults int, includeDate bool) ([]*types.WebSearchResult, error) {
	if p == nil || p.Client == nil {
		return nil, fmt.Errorf("web search plugin client is not connected")
	}
	callCtx, release, generation, err := acquirePluginCall(ctx, p.Manager, p.PluginID, invocationFromContext(ctx, ""))
	if err != nil {
		return nil, err
	}
	defer release()
	wire, err := pluginapi.EncodeWebSearchRequest(pluginapi.WebSearchRequest{
		Query:       query,
		MaxResults:  maxResults,
		IncludeDate: includeDate,
		APIKey:      p.Params.APIKey,
		EngineID:    p.Params.EngineID,
		BaseURL:     p.Params.BaseURL,
		ProxyURL:    p.Params.ProxyURL,
		ExtraConfig: p.Params.ExtraConfig,
	})
	if err != nil {
		return nil, err
	}
	responseWire, err := p.Client.Search(callCtx, wire)
	if err != nil {
		return nil, err
	}
	if p.Manager != nil && !p.Manager.GenerationValid(p.PluginID, generation) {
		return nil, fmt.Errorf("web search plugin runtime generation %d is stale", generation)
	}
	var response pluginapi.WebSearchResponse
	if err := pluginapi.DecodeWebSearchResponse(responseWire, &response); err != nil {
		return nil, err
	}
	if response.Error != "" {
		return nil, fmt.Errorf("web search plugin: %s", response.Error)
	}
	results := make([]*types.WebSearchResult, 0, len(response.Results))
	for _, result := range response.Results {
		if result == nil {
			continue
		}
		item := &types.WebSearchResult{
			Title:   result.Title,
			URL:     result.URL,
			Snippet: result.Snippet,
			Content: result.Content,
			Source:  result.Source,
		}
		if result.PublishedAt != "" {
			published, parseErr := time.Parse(time.RFC3339, result.PublishedAt)
			if parseErr != nil {
				return nil, fmt.Errorf("web search plugin returned invalid published_at %q: %w", result.PublishedAt, parseErr)
			}
			item.PublishedAt = &published
		}
		results = append(results, item)
	}
	return results, nil
}

var _ interfaces.WebSearchProvider = (*GRPCWebSearchProxy)(nil)
