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

type deepSeekTransport func(*http.Request) (*http.Response, error)

func (f deepSeekTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// deepSeekStub builds a provider whose HTTP client answers with the given body.
func deepSeekStub(t *testing.T, status int, body string) (*DeepSeekProvider, *deepSeekSearchRequest) {
	t.Helper()
	var captured deepSeekSearchRequest
	transport := deepSeekTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, defaultDeepSeekSearchURL, r.URL.String())
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &captured))
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	return &DeepSeekProvider{
		client:        &http.Client{Transport: transport},
		baseURL:       defaultDeepSeekSearchURL,
		apiKey:        "test-key",
		snippetSource: deepSeekSnippetShort,
	}, &captured
}

func TestDeepSeekSearchMapsResults(t *testing.T) {
	body := `{"results":[
		{"title":"WeKnora","url":"https://example.com/a","snippet":"short","summary":"long",
		 "site_name":"Example","date":"2026-09-19T16:00:00+00:00","authority":3,"rerank_score":2.07},
		{"title":"","url":"","snippet":"dropped"},
		{"title":"Second","url":"https://example.com/b","snippet":"s2","date":null}
	]}`
	p, captured := deepSeekStub(t, http.StatusOK, body)
	results, err := p.Search(context.Background(), "WeKnora", 5, true)
	require.NoError(t, err)
	require.Len(t, results, 2)
	require.Equal(t, "WeKnora", results[0].Title)
	require.Equal(t, "https://example.com/a", results[0].URL)
	require.Equal(t, "short", results[0].Snippet)
	require.Equal(t, "deepseek", results[0].Source)
	require.NotNil(t, results[0].PublishedAt)
	require.Equal(t, 2026, results[0].PublishedAt.Year())
	require.Nil(t, results[1].PublishedAt, "null date stays unset")

	require.Equal(t, "WeKnora", captured.Query)
	require.Equal(t, 5, captured.Count)
	require.False(t, captured.NeedContent)
}

func TestDeepSeekSearchClampsCount(t *testing.T) {
	for _, tc := range []struct{ requested, want int }{
		{0, defaultDeepSeekResults},
		{-3, defaultDeepSeekResults},
		{maxDeepSeekResults + 100, maxDeepSeekResults},
	} {
		p, captured := deepSeekStub(t, http.StatusOK, `{"results":[]}`)
		_, err := p.Search(context.Background(), "query", tc.requested, false)
		require.NoError(t, err)
		require.Equal(t, tc.want, captured.Count, "requested %d", tc.requested)
	}
}

func TestDeepSeekSearchTruncatesLongQuery(t *testing.T) {
	// 128 runes is the API maximum; the cut must be rune-based so a Chinese
	// query is never sliced mid-character.
	query := strings.Repeat("知", maxDeepSeekQueryRunes+20)
	p, captured := deepSeekStub(t, http.StatusOK, `{"results":[]}`)
	_, err := p.Search(context.Background(), "  "+query+"  ", 3, false)
	require.NoError(t, err)
	require.Len(t, []rune(captured.Query), maxDeepSeekQueryRunes)
	require.True(t, strings.TrimSpace(captured.Query) == captured.Query, "query is trimmed")
}

func TestDeepSeekSearchRejectsEmptyQuery(t *testing.T) {
	called := false
	p := &DeepSeekProvider{
		client: &http.Client{Transport: deepSeekTransport(func(*http.Request) (*http.Response, error) {
			called = true
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})},
		baseURL: defaultDeepSeekSearchURL, apiKey: "k", snippetSource: deepSeekSnippetShort,
	}
	_, err := p.Search(context.Background(), "   ", 5, false)
	require.ErrorContains(t, err, "query is empty")
	require.False(t, called, "no request is sent for a blank query")
}

func TestDeepSeekSnippetSource(t *testing.T) {
	body := `{"results":[{"title":"T","url":"https://e.com","snippet":"short","summary":"long"}]}`
	p, _ := deepSeekStub(t, http.StatusOK, body)
	p.snippetSource = deepSeekSnippetLong
	results, err := p.Search(context.Background(), "q", 1, false)
	require.NoError(t, err)
	require.Equal(t, "long", results[0].Snippet)

	// A missing summary must fall back to the snippet rather than yielding "".
	p, _ = deepSeekStub(t, http.StatusOK, `{"results":[{"title":"T","url":"https://e.com","snippet":"short"}]}`)
	p.snippetSource = deepSeekSnippetLong
	results, err = p.Search(context.Background(), "q", 1, false)
	require.NoError(t, err)
	require.Equal(t, "short", results[0].Snippet)
}

func TestDeepSeekNeedContentPopulatesContent(t *testing.T) {
	body := `{"results":[{"title":"T","url":"https://e.com","snippet":"s","content":"# page"}]}`
	p, captured := deepSeekStub(t, http.StatusOK, body)
	p.needContent = true
	results, err := p.Search(context.Background(), "q", 1, false)
	require.NoError(t, err)
	require.Equal(t, "# page", results[0].Content)
	require.True(t, captured.NeedContent)
}

func TestDeepSeekEmptyResultsIsNotAnError(t *testing.T) {
	p, _ := deepSeekStub(t, http.StatusOK, `{"results":[]}`)
	results, err := p.Search(context.Background(), "no hits here", 5, false)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestDeepSeekErrorHints(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{
			http.StatusUnauthorized, `{"error":{"message":"Authentication Fails","type":"invalid_request_error"}}`,
			"API key is missing or invalid",
		},
		{http.StatusPaymentRequired, `{"error":{"message":"Insufficient balance"}}`, "insufficient balance"},
		{http.StatusTooManyRequests, `{"error":{"message":"rate limit"}}`, "rate limit exceeded"},
		{http.StatusGatewayTimeout, `downstream timed out`, "request timed out"},
		{http.StatusBadRequest, `{"error":{"message":"'count' must be between 1 and 50"}}`, "HTTP 400"},
	} {
		p, _ := deepSeekStub(t, tc.status, tc.body)
		_, err := p.Search(context.Background(), "query", 5, false)
		require.ErrorContains(t, err, tc.want)
	}
}

func TestDeepSeekErrorEnvelopeOnHTTP200(t *testing.T) {
	p, _ := deepSeekStub(t, http.StatusOK,
		`{"error":{"message":"boom","type":"invalid_request_error","code":"invalid_request_error"}}`)
	_, err := p.Search(context.Background(), "query", 5, false)
	require.ErrorContains(t, err, "boom")
	require.ErrorContains(t, err, "invalid_request_error")
}

func TestDeepSeekHTTPErrorHasNoBody(t *testing.T) {
	p, _ := deepSeekStub(t, http.StatusInternalServerError, "")
	_, err := p.Search(context.Background(), "query", 5, false)
	require.ErrorContains(t, err, "DeepSeek Search service error (HTTP 500)")
}

func TestDeepSeekMalformedResponse(t *testing.T) {
	p, _ := deepSeekStub(t, http.StatusOK, `not json`)
	_, err := p.Search(context.Background(), "query", 5, false)
	require.ErrorContains(t, err, "failed to unmarshal DeepSeek response")
}

func TestNewDeepSeekProviderValidation(t *testing.T) {
	_, err := NewDeepSeekProvider(types.WebSearchProviderParameters{})
	require.ErrorContains(t, err, "API key is required")

	_, err = NewDeepSeekProvider(types.WebSearchProviderParameters{APIKey: "   "})
	require.ErrorContains(t, err, "API key is required")

	_, err = NewDeepSeekProvider(types.WebSearchProviderParameters{
		APIKey:      "k",
		ExtraConfig: map[string]string{"snippet_source": "highlight"},
	})
	require.ErrorContains(t, err, "invalid DeepSeek snippet source")

	provider, err := NewDeepSeekProvider(types.WebSearchProviderParameters{
		APIKey:      "k",
		ExtraConfig: map[string]string{"snippet_source": "summary", "need_content": "true"},
	})
	require.NoError(t, err)
	deepSeek := provider.(*DeepSeekProvider)
	require.Equal(t, deepSeekSnippetLong, deepSeek.snippetSource)
	require.True(t, deepSeek.needContent)
	// The API key must never travel to a redirect target.
	require.ErrorIs(t, deepSeek.client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestDeepSeekOptionsDefaults(t *testing.T) {
	source, needContent := deepSeekOptions(nil)
	require.Equal(t, deepSeekSnippetShort, source)
	require.False(t, needContent)

	source, needContent = deepSeekOptions(map[string]string{"need_content": "TRUE"})
	require.Equal(t, deepSeekSnippetShort, source)
	require.True(t, needContent)
}

func TestParseDeepSeekDate(t *testing.T) {
	for value, wantOK := range map[string]bool{
		"2026-09-19T16:00:00+00:00": true,
		"2026-09-19T16:00:00Z":      true,
		"2026-09-19":                true,
		"null":                      false,
		"":                          false,
		"not a date":                false,
	} {
		_, ok := parseDeepSeekDate(value)
		require.Equal(t, wantOK, ok, "date %q", value)
	}
}
