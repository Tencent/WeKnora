package web_search

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type braveTransport func(*http.Request) (*http.Response, error)

func (f braveTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBraveSearchMapsOptionsAndResultAges(t *testing.T) {
	p := &BraveProvider{apiKey: "test-subscription", client: &http.Client{Transport: braveTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, braveSearchURL, r.URL.Scheme+"://"+r.URL.Host+r.URL.Path)
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "test-subscription", r.Header.Get("X-Subscription-Token"))
			require.Equal(t, "rust & go", r.URL.Query().Get("q"))
			require.Equal(t, "2", r.URL.Query().Get("count"))
			require.Equal(t, "DE", r.URL.Query().Get("country"))
			require.Equal(t, "pw", r.URL.Query().Get("freshness"))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"web":{"results":[
			 {"title":"One","url":"https://example.com/one","description":"Snippet","age":"2 days ago"},
			 {"title":"Two","url":"https://example.com/two","page_age":"2026-09-01"},
			 {"url":"https://example.com/extra"}]}}`))}, nil
		},
	)}}
	results, err := p.SearchWithFilters(t.Context(), "rust & go", 2, false,
		types.WebSearchFilters{Country: "de", Freshness: "pw"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, "2 days ago", results[0].Age)
	require.Equal(t, "2026-09-01", results[1].Age)
	require.Nil(t, results[0].PublishedAt, "relative age is not an exact publication date")
}

func TestBraveDefaultsLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		requested int
		count     string
	}{{0, "5"}, {99, "20"}} {
		transport := braveTransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, tc.count, r.URL.Query().Get("count"))
			require.False(t, r.URL.Query().Has("country"))
			require.False(t, r.URL.Query().Has("freshness"))
			return &http.Response{
				StatusCode: 429, Body: io.NopCloser(strings.NewReader("secret upstream diagnostics")),
			}, nil
		})
		p := &BraveProvider{client: &http.Client{Transport: transport}}
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.ErrorContains(t, err, "HTTP 429")
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := NewBraveProvider(types.WebSearchProviderParameters{})
	require.ErrorContains(t, err, "API key")
	provider, err := NewBraveProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	require.ErrorIs(t, provider.(*BraveProvider).client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestBraveOmitsCountryUnlessRequestedAndForwardsALL(t *testing.T) {
	var got string
	p := &BraveProvider{apiKey: "k", client: &http.Client{Transport: braveTransport(
		func(r *http.Request) (*http.Response, error) {
			got = r.URL.Query().Get("country")
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"web":{"results":[]}}`))}, nil
		},
	)}}
	_, err := p.Search(t.Context(), "query", 1, false)
	require.NoError(t, err)
	require.Empty(t, got)
	_, err = p.SearchWithFilters(t.Context(), "query", 1, false, types.WebSearchFilters{Country: "ALL"})
	require.NoError(t, err)
	require.Equal(t, "ALL", got)
}
