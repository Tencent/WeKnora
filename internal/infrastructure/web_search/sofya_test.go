package web_search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sofyaTransport func(*http.Request) (*http.Response, error)

func (f sofyaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sofyaRequestBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

func TestSofyaSearchMapsOptionsAndTrimsResults(t *testing.T) {
	p := &SofyaProvider{apiKey: "test-key", client: &http.Client{Transport: sofyaTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, sofyaSearchURL, r.URL.String())
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
			require.Equal(t, "WeKnora/1.0", r.Header.Get("User-Agent"))
			require.Equal(t, map[string]any{
				"query": "rust & go", "max_results": float64(2), "search_depth": "snippets", "freshness": "week",
			}, sofyaRequestBody(t, r))
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[
			 {"title":"One","url":"https://example.com/one","content":"Page","description":"Snippet",
			  "published_date":"2026-10-01"},
			 {"title":"No url","description":"dropped"},
			 {"title":"Two","url":"https://example.com/two","content":"Fallback","description":"",
			  "published_date":null},
			 {"title":"Extra","url":"https://example.com/extra"}]}`))}, nil
		},
	)}}
	results, err := p.SearchWithFilters(t.Context(), "rust & go", 2, true,
		types.WebSearchFilters{Country: "ALL", Freshness: "pw"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), *results[0].PublishedAt)
	require.Equal(t, "Fallback", results[1].Snippet)
	require.Nil(t, results[1].PublishedAt)
	require.Equal(t, "sofya", results[1].Source)
}

func TestSofyaDefaultsLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		requested int
		sent      float64
	}{{0, 5}, {99, 20}} {
		transport := sofyaTransport(func(r *http.Request) (*http.Response, error) {
			body := sofyaRequestBody(t, r)
			require.Equal(t, tc.sent, body["max_results"])
			require.NotContains(t, body, "freshness")
			return &http.Response{
				StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{"detail":"secret upstream diagnostics"}`)),
			}, nil
		})
		p := &SofyaProvider{client: &http.Client{Transport: transport}}
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.ErrorContains(t, err, "HTTP 401")
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := NewSofyaProvider(types.WebSearchProviderParameters{})
	require.ErrorContains(t, err, "API key")
	provider, err := NewSofyaProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	require.ErrorIs(t, provider.(*SofyaProvider).client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestSofyaRejectsDateRangesAndCountry(t *testing.T) {
	var got map[string]any
	p := &SofyaProvider{apiKey: "k", client: &http.Client{Transport: sofyaTransport(
		func(r *http.Request) (*http.Response, error) {
			got = sofyaRequestBody(t, r)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
		},
	)}}
	_, err := p.SearchWithFilters(t.Context(), "query", 1, false,
		types.WebSearchFilters{Freshness: "2026-01-01to2026-02-01"})
	require.ErrorContains(t, err, "date ranges are not supported")
	require.Nil(t, got, "no request is sent for an unsupported filter")
	_, err = p.SearchWithFilters(t.Context(), "query", 1, false, types.WebSearchFilters{Country: "DE"})
	require.ErrorContains(t, err, "country")
	require.Nil(t, got, "no request is sent for an unsupported filter")
}
