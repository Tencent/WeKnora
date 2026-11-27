package pairwiserank

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, base string, settings api.RerankSettings) *Client {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	return New(Config{
		Endpoint: api.Endpoint{
			BaseURL: base, Model: "Qwen/Qwen3-Reranker-4B", Auth: api.BearerAuth("key"),
		},
		Settings: settings,
	})
}

func TestRequestRepeatsQueryForEveryDocument(t *testing.T) {
	c := newClient(t, "https://example.com/v1/inference", api.RerankSettings{
		SendTopN: true, SendReturnDocs: true, Truncate: "END",
		AcceptsTruncatePromptTokens: true, TruncatePromptTokens: 100,
	})
	assert.Equal(t, map[string]any{
		"queries":   []string{"where?", "where?", "where?"},
		"documents": []string{"first", "second", "first"},
	}, c.BuildRequestBody("where?", []string{"first", "second", "first"}))
}

func TestExtraBodyCannotReplacePairs(t *testing.T) {
	extra := map[string]any{
		"queries": []string{"wrong"}, "documents": []string{"wrong"},
		"instruction": "Rank relevance", "custom_option": false,
	}
	c := New(Config{Settings: api.RerankSettings{ExtraBody: extra}})
	assert.Equal(t, map[string]any{
		"queries": []string{"q", "q"}, "documents": []string{"a", "b"},
		"instruction": "Rank relevance", "custom_option": false,
	}, c.BuildRequestBody("q", []string{"a", "b"}))
	assert.Equal(t, []string{"wrong"}, extra["queries"], "building a request must not modify configured extensions")
}

func TestBuildEmptyRequestUsesArrays(t *testing.T) {
	c := New(Config{})
	for _, docs := range [][]string{nil, {}} {
		body, err := json.Marshal(c.BuildRequestBody("q", docs))
		require.NoError(t, err)
		assert.JSONEq(t, `{"queries":[],"documents":[]}`, string(body))
	}
}

func TestURLOverridesAndInferenceModelPath(t *testing.T) {
	const model = "Qwen/Qwen3-Reranker-4B"
	for _, tc := range []struct {
		name, base, url, path, want string
	}{
		{"inference base", "https://example.com/v1/inference", "", "", "https://example.com/v1/inference/" + model},
		{"trailing slash", "https://example.com/v1/inference/", "", "", "https://example.com/v1/inference/" + model},
		{
			"pasted full endpoint", "https://example.com/v1/inference/" + model, "", "",
			"https://example.com/v1/inference/" + model,
		},
		{
			"pasted full endpoint trailing slash", "https://example.com/v1/inference/" + model + "/", "", "",
			"https://example.com/v1/inference/" + model,
		},
		{"custom path", "https://example.com", "", "/custom/rank", "https://example.com/custom/rank"},
		{
			"custom path already present", "https://example.com/custom/rank", "", "/custom/rank",
			"https://example.com/custom/rank",
		},
		{
			"full URL overrides path", "https://ignored.example", "https://example.com/full?existing=yes", "/ignored",
			"https://example.com/full?existing=yes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Config{
				Endpoint: api.Endpoint{BaseURL: tc.base, URL: tc.url, Model: model},
				Settings: api.RerankSettings{Path: tc.path},
			})
			assert.Equal(t, tc.want, c.url())
			c.cfg.Endpoint.Query = map[string]string{"version": "1"}
			separator := "?"
			if tc.url != "" {
				separator = "&"
			}
			assert.Equal(t, tc.want+separator+"version=1", c.url())
		})
	}
}

func TestModelPathEscapesSegmentsWithoutChangingURLMeaning(t *testing.T) {
	for _, tc := range []struct{ model, escapedPath string }{
		{"Qwen/model?revision=1#weights", "/v1/inference/Qwen/model%3Frevision=1%23weights"},
		{"Qwen?tenant=other/model#variant", "/v1/inference/Qwen%3Ftenant=other/model%23variant"},
		{"Qwen/model name%", "/v1/inference/Qwen/model%20name%25"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			c := New(Config{Endpoint: api.Endpoint{
				BaseURL: "https://example.com/v1/inference", Model: tc.model,
				Query: map[string]string{"version": "1"},
			}})
			target, err := url.Parse(c.url())
			require.NoError(t, err)
			assert.Equal(t, tc.escapedPath, target.EscapedPath())
			assert.Equal(t, "/v1/inference/"+tc.model, target.Path)
			assert.Equal(t, "version=1", target.RawQuery)
			assert.Empty(t, target.Fragment)
			c.cfg.Endpoint.BaseURL = "https://example.com" + tc.escapedPath
			assert.Equal(t, target.String(), c.url(), "a full escaped endpoint is not doubled")
		})
	}
}

func TestRerankPreservesInputOrderTextsAndRawScores(t *testing.T) {
	var path, auth, contentType, method, customHeader string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		contentType, method = r.Header.Get("Content-Type"), r.Method
		customHeader = r.Header.Get("X-Custom")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"scores":[-2.5,0,3.25]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL+"/v1/inference", api.RerankSettings{})
	c.cfg.Endpoint.Headers = map[string]string{"X-Custom": "value"}
	got, err := c.Rerank(context.Background(), "q", []string{"same", "other", "same"})
	require.NoError(t, err)
	assert.Equal(t, "/v1/inference/Qwen/Qwen3-Reranker-4B", path)
	assert.Equal(t, "Bearer key", auth)
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "value", customHeader)
	assert.Equal(t, map[string]any{
		"queries": []any{"q", "q", "q"}, "documents": []any{"same", "other", "same"},
	}, body)
	assert.Equal(t, []api.RerankResult{
		{Index: 0, Score: -2.5, Text: "same"},
		{Index: 1, Score: 0, Text: "other"},
		{Index: 2, Score: 3.25, Text: "same"},
	}, got, "the wire client does not normalize or sort scores")
}

func TestRejectsIncompleteOrMalformedScores(t *testing.T) {
	for _, tc := range []struct{ name, reply, want string }{
		{"missing scores", `{}`, "no scores array"},
		{"null response", `null`, "no scores array"},
		{"null scores", `{"scores":null}`, "no scores array"},
		{"empty scores", `{"scores":[]}`, "0 scores for 2 documents"},
		{"short scores", `{"scores":[0.5]}`, "1 scores for 2 documents"},
		{"extra scores", `{"scores":[0.5,0.4,0.3]}`, "3 scores for 2 documents"},
		{"null score", `{"scores":[0.5,null]}`, "score is missing for index 1"},
		{"string score", `{"scores":[0.5,"0.4"]}`, "decode response"},
		{"wrong scores type", `{"scores":{}}`, "decode response"},
		{"overflow", `{"scores":[0.5,1e9999]}`, "decode response"},
		{"NaN", `{"scores":[0.5,NaN]}`, "decode response"},
		{"infinity", `{"scores":[0.5,Infinity]}`, "decode response"},
		{"truncated JSON", `{"scores":[0.5`, "decode response"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()
			c := newClient(t, server.URL, api.RerankSettings{})
			got, err := c.Rerank(context.Background(), "q", []string{"a", "b"})
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, got, "partial scores must not be returned on failure")
		})
	}
}

func TestRejectsNonFiniteDecodedScores(t *testing.T) {
	// JSON itself rejects these values. Check the result validator directly so
	// a future decoder change cannot accidentally bypass the finite-score rule.
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		scores := []*float64{&value}
		got, err := (response{Scores: &scores}).results([]string{"d"})
		require.ErrorContains(t, err, "non-finite for index 0")
		assert.Nil(t, got)
	}
}

func TestEmptyDocumentsSkipNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	for _, docs := range [][]string{nil, {}} {
		got, err := c.Rerank(context.Background(), "q", docs)
		require.NoError(t, err)
		assert.Equal(t, []api.RerankResult{}, got)
	}
	assert.Zero(t, requests.Load())
}

func TestCancellationIsPreserved(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"scores":[0.5]}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := c.Rerank(ctx, "q", []string{"d"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
	assert.Nil(t, got)
	assert.Zero(t, requests.Load())
}

func TestCancellationStopsAnInFlightRequest(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := c.Rerank(ctx, "q", []string{"d"})
		finished <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not reach the local server")
	}
	cancel()
	select {
	case err := <-finished:
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(5 * time.Second):
		t.Fatal("request did not stop after cancellation")
	}
}

func TestHTTPErrorPreservesStatusBodyAndRetryHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "12")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"quota exceeded"}`))
	}))
	defer server.Close()
	c := newClient(t, server.URL, api.RerankSettings{})
	got, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.ErrorContains(t, err, "quota exceeded")
	var httpErr *api.HTTPError
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, http.StatusTooManyRequests, httpErr.StatusCode)
	assert.Equal(t, `{"error":"quota exceeded"}`, httpErr.Body)
	assert.Equal(t, "12", httpErr.Header.Get("Retry-After"))
	assert.Nil(t, got)
}

func TestInvalidExtraBodyReturnsMarshalError(t *testing.T) {
	c := newClient(t, "https://example.com/v1/inference", api.RerankSettings{
		ExtraBody: map[string]any{"invalid": make(chan int)},
	})
	got, err := c.Rerank(context.Background(), "q", []string{"d"})
	require.ErrorContains(t, err, "marshal request")
	assert.Nil(t, got)
}
