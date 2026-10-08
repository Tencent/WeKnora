package web_search

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type serplyTransport func(*http.Request) (*http.Response, error)

func (f serplyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSerplySearchMapsOptionsAndTrimsResults(t *testing.T) {
	p := &SerplyProvider{apiKey: "test-key", client: &http.Client{Transport: serplyTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, serplySearchURL, r.URL.Scheme+"://"+r.URL.Host+r.URL.Path)
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "test-key", r.Header.Get("X-Api-Key"))
			require.Equal(t, "WeKnora/1.0", r.Header.Get("User-Agent"))
			require.Equal(t, "rust & go", r.URL.Query().Get("q"))
			require.Equal(t, "2", r.URL.Query().Get("num"))
			require.Equal(t, "de", r.URL.Query().Get("gl"))
			require.Equal(t, "qdr:w", r.URL.Query().Get("tbs"))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[
			 {"title":"One","link":"https://example.com/one","description":"Snippet","position":1},
			 {"title":"No link","description":"dropped"},
			 {"title":"Two","link":"https://example.com/two"},
			 {"title":"Extra","link":"https://example.com/extra"}]}`))}, nil
		},
	)}}
	results, err := p.SearchWithFilters(t.Context(), "rust & go", 2, false,
		types.WebSearchFilters{Country: "DE", Freshness: "pw"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, "https://example.com/two", results[1].URL)
	require.Equal(t, "serply", results[1].Source)
}

func TestSerplyDefaultsLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		requested int
		num       string
	}{{0, "5"}, {99, "10"}} {
		transport := serplyTransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, tc.num, r.URL.Query().Get("num"))
			require.False(t, r.URL.Query().Has("gl"))
			require.False(t, r.URL.Query().Has("tbs"))
			return &http.Response{
				StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"detail":"secret upstream diagnostics"}`)),
			}, nil
		})
		p := &SerplyProvider{client: &http.Client{Transport: transport}}
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.ErrorContains(t, err, "HTTP 401")
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := NewSerplyProvider(types.WebSearchProviderParameters{})
	require.ErrorContains(t, err, "API key")
	provider, err := NewSerplyProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	require.ErrorIs(t, provider.(*SerplyProvider).client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestSerplyOmitsRegionForALLAndRejectsDateRanges(t *testing.T) {
	var got url.Values
	p := &SerplyProvider{apiKey: "k", client: &http.Client{Transport: serplyTransport(
		func(r *http.Request) (*http.Response, error) {
			got = r.URL.Query()
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
		},
	)}}
	_, err := p.SearchWithFilters(t.Context(), "query", 1, false, types.WebSearchFilters{Country: "ALL"})
	require.NoError(t, err)
	require.False(t, got.Has("gl"))
	got = nil
	_, err = p.SearchWithFilters(t.Context(), "query", 1, false,
		types.WebSearchFilters{Freshness: "2026-01-01to2026-02-01"})
	require.ErrorContains(t, err, "date ranges are not supported")
	require.Nil(t, got, "no request is sent for an unsupported filter")
}
