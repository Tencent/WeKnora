package web_search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type firecrawlTransport func(*http.Request) (*http.Response, error)

func (f firecrawlTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func firecrawlBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}

func firecrawlOK(body string) *http.Response {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
}

func TestFirecrawlSearchMapsOptionsAndTrimsResults(t *testing.T) {
	p := &FirecrawlProvider{apiKey: "test-key", client: &http.Client{Transport: firecrawlTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, firecrawlSearchURL, r.URL.String())
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
			require.Equal(t, "WeKnora/1.0", r.Header.Get("User-Agent"))
			body := firecrawlBody(t, r)
			require.Equal(t, "rust & go", body["query"])
			require.EqualValues(t, 2, body["limit"])
			require.Equal(t, []any{"web"}, body["sources"])
			require.Equal(t, "DE", body["country"])
			require.Equal(t, "qdr:w", body["tbs"])
			require.NotContains(t, body, "origin")
			require.NotContains(t, body, "scrapeOptions")
			return firecrawlOK(`{"success":true,"data":{"web":[
			 {"title":"One","url":"https://example.com/one","description":"Snippet","position":1},
			 {"title":"No url","description":"dropped"},
			 {"title":"Two","url":"https://example.com/two","markdown":"ignored"},
			 {"title":"Extra","url":"https://example.com/extra"}]}}`), nil
		},
	)}}
	results, err := p.SearchWithFilters(t.Context(), "rust & go", 2, false,
		types.WebSearchFilters{Country: "de", Freshness: "pw"})
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, "https://example.com/two", results[1].URL)
	require.Equal(t, "firecrawl", results[1].Source)
	require.Empty(t, results[1].Content, "content stays empty unless include_content is set")
}

func TestFirecrawlCapsLongDescriptions(t *testing.T) {
	long := strings.Repeat("检索", maxFirecrawlSnippetRunes)
	p := &FirecrawlProvider{apiKey: "k", client: &http.Client{Transport: firecrawlTransport(
		func(*http.Request) (*http.Response, error) {
			row := `{"url":"https://example.com","description":"` + long + `"}`
			return firecrawlOK(`{"success":true,"data":{"web":[` + row + `]}}`), nil
		},
	)}}
	results, err := p.Search(t.Context(), "query", 1, false)
	require.NoError(t, err)
	require.Len(t, []rune(results[0].Snippet), maxFirecrawlSnippetRunes)
}

func TestFirecrawlDefaultsLimitsAndErrors(t *testing.T) {
	for _, tc := range []struct {
		requested int
		limit     float64
	}{{0, 5}, {99, 20}} {
		transport := firecrawlTransport(func(r *http.Request) (*http.Response, error) {
			body := firecrawlBody(t, r)
			require.Equal(t, tc.limit, body["limit"])
			require.NotContains(t, body, "country")
			require.NotContains(t, body, "tbs")
			body401 := `{"success":false,"error":"secret upstream diagnostics"}`
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(body401))}, nil
		})
		p := &FirecrawlProvider{client: &http.Client{Transport: transport}}
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.ErrorContains(t, err, "HTTP 401 (invalid API key)")
		require.NotContains(t, err.Error(), "secret")
	}
	_, err := NewFirecrawlProvider(types.WebSearchProviderParameters{APIKey: "  "})
	require.ErrorContains(t, err, "API key")
	provider, err := NewFirecrawlProvider(types.WebSearchProviderParameters{APIKey: "test"})
	require.NoError(t, err)
	require.ErrorIs(t, provider.(*FirecrawlProvider).client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestFirecrawlOmitsCountryForALLAndRejectsDateRanges(t *testing.T) {
	var got map[string]any
	p := &FirecrawlProvider{apiKey: "k", client: &http.Client{Transport: firecrawlTransport(
		func(r *http.Request) (*http.Response, error) {
			got = firecrawlBody(t, r)
			return firecrawlOK(`{"success":true,"data":{"web":[]}}`), nil
		},
	)}}
	_, err := p.SearchWithFilters(t.Context(), "query", 1, false, types.WebSearchFilters{Country: "ALL"})
	require.NoError(t, err)
	require.NotContains(t, got, "country")
	got = nil
	_, err = p.SearchWithFilters(t.Context(), "query", 1, false,
		types.WebSearchFilters{Freshness: "2026-01-05to2026-02-28"})
	require.ErrorContains(t, err, "date ranges are not supported")
	require.Nil(t, got, "no request is sent for an unsupported filter")
}

func TestFirecrawlIncludeContentAndUnsuccessfulResponse(t *testing.T) {
	provider, err := NewFirecrawlProvider(types.WebSearchProviderParameters{
		APIKey: "k", ExtraConfig: map[string]string{"include_content": "true"},
	})
	require.NoError(t, err)
	p := provider.(*FirecrawlProvider)
	p.client.Transport = firecrawlTransport(func(r *http.Request) (*http.Response, error) {
		body := firecrawlBody(t, r)
		require.Equal(t, map[string]any{"formats": []any{"markdown"}, "onlyMainContent": true}, body["scrapeOptions"])
		require.EqualValues(t, 55000, body["timeout"], "API deadline stays inside the 60s client timeout")
		return firecrawlOK(`{"success":true,"data":{"web":[
		 {"title":"One","url":"https://example.com/one","description":"Snippet","markdown":"  # Page body  "},
		 {"title":"Two","url":"https://example.com/two","markdown":"Body without description"}]}}`), nil
	})
	results, err := p.Search(t.Context(), "query", 3, false)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "# Page body", results[0].Content)
	require.Equal(t, "Snippet", results[0].Snippet)
	require.Equal(t, "Body without description", results[1].Snippet, "snippet falls back to page content")

	p.client.Transport = firecrawlTransport(func(*http.Request) (*http.Response, error) {
		return firecrawlOK(`{"success":false,"error":"secret upstream diagnostics"}`), nil
	})
	_, err = p.Search(t.Context(), "query", 3, false)
	require.ErrorContains(t, err, "did not succeed")
	require.NotContains(t, err.Error(), "secret")
}
