package service

import (
	"context"
	"testing"

	infra "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type recordingFetcher struct {
	url string
}

func (p *recordingFetcher) Fetch(_ context.Context, pageURL string) (*types.WebPage, error) {
	p.url = pageURL
	return &types.WebPage{Content: "api:" + pageURL, Source: "pagefetch"}, nil
}

// countingProviderRepository counts lookups so a test can check the provider loads once.
type countingProviderRepository struct {
	filterProviderRepository
	lookups int
}

func (r *countingProviderRepository) GetByID(
	ctx context.Context, tenantID uint64, id string,
) (*types.WebSearchProviderEntity, error) {
	r.lookups++
	return r.filterProviderRepository.GetByID(ctx, tenantID, id)
}

func TestWebSearchResolveFollowsProviderType(t *testing.T) {
	page := &recordingFetcher{}
	plain := &recordingSearcher{}
	registry := infra.NewRegistry()
	registry.Add(infra.Spec{
		Type:   "pagefetch",
		Search: searcherFactory(plain),
		Fetch:  func(types.WebSearchProviderParameters) (interfaces.WebFetcher, error) { return page, nil },
	})
	registry.Add(infra.Spec{Type: "filtered", Filters: true, Search: searcherFactory(&recordingSearcher{})})
	repo := &countingProviderRepository{}
	svc := &WebSearchService{registry: registry, providerRepo: repo}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))

	pageClient, err := svc.Resolve(ctx, "pagefetch")
	require.NoError(t, err)
	filteredClient, err := svc.Resolve(ctx, "filtered")
	require.NoError(t, err)
	require.False(t, pageClient.Capabilities().Filters)
	require.True(t, filteredClient.Capabilities().Filters)

	for range 2 {
		_, err = pageClient.Search(ctx, types.WebSearchRequest{Query: "q"})
		require.NoError(t, err)
	}
	require.Equal(t, 2, plain.calls)
	require.Equal(t, 2, repo.lookups, "searches must reuse the resolved provider")

	got, err := pageClient.Fetch(ctx, "https://example.com/a")
	require.NoError(t, err)
	require.Equal(t, &types.WebPage{Content: "api:https://example.com/a", Source: "pagefetch"}, got)
	require.Equal(t, "https://example.com/a", page.url)

	// A provider without a fetch API reads locally, which rejects an empty URL without a network call.
	_, err = filteredClient.Fetch(ctx, "")
	require.ErrorContains(t, err, "url is empty")

	_, err = svc.Resolve(context.Background(), "pagefetch")
	require.Error(t, err, "no tenant: provider cannot be loaded")
	_, err = svc.Resolve(ctx, "  ")
	require.ErrorContains(t, err, "no web search provider configured")
	_, err = svc.Resolve(ctx, "unregistered")
	require.ErrorContains(t, err, "not registered")
}

func TestValidateProviderParametersDeepSeek(t *testing.T) {
	err := validateProviderParameters(types.WebSearchProviderTypeDeepSeek, types.WebSearchProviderParameters{})
	if err == nil {
		t.Fatal("missing DeepSeek API key was accepted")
	}
	err = validateProviderParameters(
		types.WebSearchProviderTypeDeepSeek, types.WebSearchProviderParameters{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("saved DeepSeek key rejected: %v", err)
	}
	if !isValidProviderType(types.WebSearchProviderTypeDeepSeek) {
		t.Fatal("DeepSeek provider type is not accepted")
	}
}
