package service

import (
	"context"
	"testing"

	infra "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type unfilteredSearchProvider struct{ called bool }

func (p *unfilteredSearchProvider) Name() string { return "unfiltered" }
func (p *unfilteredSearchProvider) Search(context.Context, string, int, bool) ([]*types.WebSearchResult, error) {
	p.called = true
	return nil, nil
}

type filteredSearchProvider struct {
	unfilteredSearchProvider
	filters types.WebSearchFilters
}

func (p *filteredSearchProvider) SearchWithFilters(
	_ context.Context, _ string, _ int, _ bool, f types.WebSearchFilters,
) ([]*types.WebSearchResult, error) {
	p.filters = f
	return nil, nil
}

type filterProviderRepository struct {
	interfaces.WebSearchProviderRepository
}

func (filterProviderRepository) GetByID(
	_ context.Context, tenantID uint64, id string,
) (*types.WebSearchProviderEntity, error) {
	return &types.WebSearchProviderEntity{ID: id, TenantID: tenantID, Provider: types.WebSearchProviderType(id)}, nil
}

func TestWebSearchServiceRejectsUnsupportedFilters(t *testing.T) {
	plain := &unfilteredSearchProvider{}
	filtered := &filteredSearchProvider{}
	r := infra.NewRegistry()
	r.Register("plain", func(types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return plain, nil
	})
	r.Register("filtered", func(types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return filtered, nil
	})
	s := &WebSearchService{registry: r, providerRepo: filterProviderRepository{}}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	cfg := types.DefaultWebSearchConfig()
	cfg.Filters = types.WebSearchFilters{Country: "DE", Freshness: "pd"}
	_, err := s.Search(ctx, "plain", cfg, "query")
	require.ErrorContains(t, err, "does not support")
	require.False(t, plain.called)
	_, err = s.Search(ctx, "filtered", cfg, "query")
	require.NoError(t, err)
	require.Equal(t, cfg.Filters, filtered.filters)
	require.False(t, filtered.called)
	cfg.Filters = types.WebSearchFilters{}
	_, err = s.Search(ctx, "plain", cfg, "query")
	require.NoError(t, err)
	require.True(t, plain.called)
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
