package web_search

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type youTransport func(*http.Request) (*http.Response, error)

func (f youTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func youResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func youEndpoint(r *http.Request) string {
	return r.URL.Scheme + "://" + r.URL.Host + r.URL.Path
}

func TestYouSearchKeylessByDefault(t *testing.T) {
	p := &YouProvider{client: &http.Client{Transport: youTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, youKeylessSearchURL, youEndpoint(r))
			require.Equal(t, "rust & go", r.URL.Query().Get("query"))
			require.Equal(t, "2", r.URL.Query().Get("count"))
			require.Empty(t, r.Header.Get("X-API-Key"), "keyless requests carry no key")
			require.Equal(t, "WeKnora/1.0 youdotcom-integration/tencent-weknora", r.Header.Get("User-Agent"))
			return youResponse(200, `{"results":{"web":[
			 {"url":"https://example.com/one","title":"One","description":"Short",
			  "snippets":["First excerpt","Second excerpt"],"page_age":"2026-08-14T00:00:00"},
			 {"title":"No url","description":"dropped"},
			 {"url":"https://example.com/two","title":"Two","snippets":["Only excerpt"]},
			 {"url":"https://example.com/extra","title":"Extra"}]}}`), nil
		},
	)}}
	results, err := p.Search(t.Context(), "rust & go", 2, true)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "you", results[0].Source)
	require.Equal(t, "Short", results[0].Snippet)
	require.Equal(t, "First excerpt\nSecond excerpt", results[0].Content)
	require.NotNil(t, results[0].PublishedAt)
	require.Equal(t, time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC), *results[0].PublishedAt)
	require.Equal(t, "https://example.com/two", results[1].URL)
	require.Equal(t, "Only excerpt", results[1].Snippet, "snippet falls back to the excerpts")
	require.Nil(t, results[1].PublishedAt)
}

func TestYouSearchUsesKeyedEndpointWithKey(t *testing.T) {
	provider, err := NewYouProvider(types.WebSearchProviderParameters{APIKey: "  ydc-test  "})
	require.NoError(t, err)
	p := provider.(*YouProvider)
	p.client.Transport = youTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, youKeyedSearchURL, youEndpoint(r))
		require.Equal(t, "ydc-test", r.Header.Get("X-API-Key"))
		require.Equal(t, "20", r.URL.Query().Get("count"), "count is capped at the tool maximum")
		return youResponse(200, `{"results":{"web":[]}}`), nil
	})
	results, err := p.Search(t.Context(), "query", 99, false)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestYouSearchDefaultsAndDateOptOut(t *testing.T) {
	p := &YouProvider{client: &http.Client{Transport: youTransport(
		func(r *http.Request) (*http.Response, error) {
			require.Equal(t, "5", r.URL.Query().Get("count"))
			row := `{"url":"https://example.com","page_age":"2026-08-14T00:00:00Z"}`
			return youResponse(200, `{"results":{"web":[`+row+`]}}`), nil
		},
	)}}
	results, err := p.Search(context.Background(), "query", 0, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Nil(t, results[0].PublishedAt, "dates are only parsed when includeDate is set")

	_, err = p.Search(context.Background(), "   ", 5, false)
	require.ErrorContains(t, err, "query is empty")
}

func TestYouCapsLongText(t *testing.T) {
	long := strings.Repeat("检索", maxYouSnippetRunes)
	p := &YouProvider{client: &http.Client{Transport: youTransport(
		func(*http.Request) (*http.Response, error) {
			row := `{"url":"https://example.com","description":"` + long + `"}`
			return youResponse(200, `{"results":{"web":[`+row+`]}}`), nil
		},
	)}}
	results, err := p.Search(t.Context(), "query", 1, false)
	require.NoError(t, err)
	require.Len(t, []rune(results[0].Snippet), maxYouSnippetRunes)
}

func TestYouErrorsDoNotEchoUpstreamBody(t *testing.T) {
	for _, tc := range []struct {
		key    string
		status int
		want   string
	}{
		{"", 429, "HTTP 429 (keyless limit reached; add an API key for higher limits)"},
		{"k", 429, "HTTP 429 (rate limited)"},
		{"k", 401, "HTTP 401 (invalid API key)"},
		{"k", 402, "HTTP 402 (out of credits)"},
		{"", 500, "HTTP 500"},
	} {
		p := &YouProvider{apiKey: tc.key, client: &http.Client{Transport: youTransport(
			func(*http.Request) (*http.Response, error) {
				return youResponse(tc.status, `{"error":"secret upstream diagnostics"}`), nil
			},
		)}}
		_, err := p.Search(t.Context(), "query", 3, false)
		require.ErrorContains(t, err, tc.want)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestYouRejectsOversizedResponses(t *testing.T) {
	p := &YouProvider{client: &http.Client{Transport: youTransport(
		func(*http.Request) (*http.Response, error) {
			return youResponse(200, strings.Repeat(" ", maxJSONResponseBytes+1)), nil
		},
	)}}
	_, err := p.Search(t.Context(), "query", 3, false)
	require.ErrorContains(t, err, "exceeds")
}

func TestNewYouProviderIsKeylessAndRefusesRedirects(t *testing.T) {
	provider, err := NewYouProvider(types.WebSearchProviderParameters{})
	require.NoError(t, err)
	p := provider.(*YouProvider)
	require.Empty(t, p.apiKey)
	require.Equal(t, "you", p.Name())
	require.ErrorIs(t, p.client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}
