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

type searchAPITransport func(*http.Request) (*http.Response, error)

func (f searchAPITransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchAPISearchMapsOptionsAndTrimsResults(t *testing.T) {
	p := &SearchAPIProvider{apiKey: "test-key", client: &http.Client{Transport: searchAPITransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, searchAPISearchURL, r.URL.Scheme+"://"+r.URL.Host+r.URL.Path)
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
			require.Equal(t, "WeKnora/1.0", r.Header.Get("User-Agent"))
			require.Equal(t, "google", r.URL.Query().Get("engine"))
			require.Equal(t, "rust & go", r.URL.Query().Get("q"))
			require.Equal(t, "2", r.URL.Query().Get("num"))
			require.Equal(t, "de", r.URL.Query().Get("gl"))
			require.Equal(t, "last_week", r.URL.Query().Get("time_period"))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{
			 "search_metadata":{"id":"x","status":"Success"},
			 "organic_results":[
			 {"position":1,"title":"One","link":"https://example.com/one","snippet":"Snippet","date":"2 days ago"},
			 {"position":2,"title":"No link","snippet":"dropped"},
			 {"position":3,"title":"Two","link":"https://example.com/two"},
			 {"position":4,"title":"Extra","link":"https://example.com/extra"}]}`))}, nil
		},
	)}}
	results, err := p.SearchWithFilters(t.Context(), "rust & go", 2, false,
		types.WebSearchFilters{Country: "DE", Freshness: "pw"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "One", results[0].Title)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, "2 days ago", results[0].Age)
	require.Nil(t, results[0].PublishedAt)
	require.Equal(t, "https://example.com/two", results[1].URL)
	require.Empty(t, results[1].Age)
	require.Equal(t, "searchapi", results[1].Source)
}

func TestSearchAPIDefaultsLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		requested int
		num       string
	}{{0, "5"}, {99, "20"}} {
		transport := searchAPITransport(func(r *http.Request) (*http.Response, error) {
			require.Equal(t, tc.num, r.URL.Query().Get("num"))
			require.Equal(t, "google", r.URL.Query().Get("engine"))
			require.False(t, r.URL.Query().Has("gl"))
			require.False(t, r.URL.Query().Has("time_period"))
			return &http.Response{
				StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"error":"secret upstream diagnostics"}`)),
			}, nil
		})
		p := &SearchAPIProvider{client: &http.Client{Transport: transport}}
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.ErrorContains(t, err, "HTTP 401")
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := NewSearchAPIProvider(types.WebSearchProviderParameters{})
	require.ErrorContains(t, err, "API key")
	provider, err := NewSearchAPIProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	require.ErrorIs(t, provider.(*SearchAPIProvider).client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestSearchAPIHandlesEmptyAndMalformedPayloads(t *testing.T) {
	for _, body := range []string{`{}`, `{"organic_results":[]}`, `{"organic_results":null}`} {
		p := &SearchAPIProvider{apiKey: "k", client: &http.Client{Transport: searchAPITransport(
			func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			},
		)}}
		results, err := p.Search(t.Context(), "query", 3, false)
		require.NoError(t, err, body)
		require.Empty(t, results, body)
	}
	p := &SearchAPIProvider{apiKey: "k", client: &http.Client{Transport: searchAPITransport(
		func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<html>not json`))}, nil
		},
	)}}
	_, err := p.Search(t.Context(), "query", 3, false)
	require.ErrorContains(t, err, "decode SearchApi response")
	_, err = p.Search(t.Context(), "   ", 3, false)
	require.ErrorContains(t, err, "query is empty")
}

func TestSearchAPIOmitsRegionForALLAndRejectsDateRanges(t *testing.T) {
	var got url.Values
	p := &SearchAPIProvider{apiKey: "k", client: &http.Client{Transport: searchAPITransport(
		func(r *http.Request) (*http.Response, error) {
			got = r.URL.Query()
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"organic_results":[]}`))}, nil
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
