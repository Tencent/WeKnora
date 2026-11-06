package service

import (
	"context"
	"testing"

	infra "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingSearcher struct {
	calls   int
	request types.WebSearchRequest
	results []*types.WebSearchResult
}

func (p *recordingSearcher) Search(_ context.Context, req types.WebSearchRequest) ([]*types.WebSearchResult, error) {
	p.calls++
	p.request = req
	return p.results, nil
}

func searcherFactory(searcher interfaces.WebSearcher) infra.SearchFactory {
	return func(types.WebSearchProviderParameters) (interfaces.WebSearcher, error) { return searcher, nil }
}

type filterProviderRepository struct {
	interfaces.WebSearchProviderRepository
}

func (filterProviderRepository) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.WebSearchProviderEntity, error) {
	return &types.WebSearchProviderEntity{ID: id, TenantID: tenantID, Provider: types.WebSearchProviderType(id)}, nil
}

func TestWebClientRejectsUnsupportedFilters(t *testing.T) {
	plain := &recordingSearcher{}
	filtered := &recordingSearcher{}
	r := infra.NewRegistry()
	r.Add(infra.Spec{Type: "plain", Search: searcherFactory(plain)})
	r.Add(infra.Spec{Type: "filtered", Filters: true, Search: searcherFactory(filtered)})
	s := &WebSearchService{registry: r, providerRepo: filterProviderRepository{}}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	plainClient, err := s.Resolve(ctx, "plain")
	require.NoError(t, err)
	filteredClient, err := s.Resolve(ctx, "filtered")
	require.NoError(t, err)

	filters := types.WebSearchFilters{Country: "DE", Freshness: "pd"}
	_, err = plainClient.Search(ctx, types.WebSearchRequest{Query: "query", Filters: filters})
	require.ErrorContains(t, err, "does not support")
	require.Zero(t, plain.calls)

	_, err = filteredClient.Search(ctx, types.WebSearchRequest{
		Query: "query", Filters: types.WebSearchFilters{Country: "Germany"},
	})
	require.ErrorContains(t, err, "two-letter")
	require.Zero(t, filtered.calls, "invalid filters never reach the provider")

	_, err = filteredClient.Search(ctx, types.WebSearchRequest{Query: "query", MaxResults: 3, Filters: filters})
	require.NoError(t, err)
	require.Equal(t, types.WebSearchRequest{Query: "query", MaxResults: 3, Filters: filters}, filtered.request)

	_, err = plainClient.Search(ctx, types.WebSearchRequest{Query: "query"})
	require.NoError(t, err)
	require.Equal(t, 1, plain.calls)
}

func TestWebClientAppliesTenantBlacklist(t *testing.T) {
	searcher := &recordingSearcher{results: []*types.WebSearchResult{
		{URL: "https://blocked.example.com/a"},
		{URL: "https://kept.example.org/b"},
	}}
	r := infra.NewRegistry()
	r.Add(infra.Spec{Type: "plain", Search: searcherFactory(searcher)})
	s := &WebSearchService{registry: r, providerRepo: filterProviderRepository{}}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		WebSearchConfig: &types.WebSearchConfig{Blacklist: []string{"*://blocked.example.com/*"}},
	})

	client, err := s.Resolve(ctx, "plain")
	require.NoError(t, err)
	results, err := client.Search(ctx, types.WebSearchRequest{Query: "q"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "https://kept.example.org/b", results[0].URL)
}

func TestBraveProviderConfigurationIsAvailable(t *testing.T) {
	require.True(t, isValidProviderType(types.WebSearchProviderTypeBrave))
	require.Error(t, validateProviderParameters(types.WebSearchProviderTypeBrave, types.WebSearchProviderParameters{}))
	require.NoError(t, validateProviderParameters(
		types.WebSearchProviderTypeBrave, types.WebSearchProviderParameters{APIKey: "test"},
	))
	found := false
	for _, info := range types.GetWebSearchProviderTypes() {
		if info.ID == "brave" {
			found = true
			require.True(t, info.RequiresAPIKey)
			require.True(t, info.SupportsProxy)
		}
	}
	require.True(t, found)
}
