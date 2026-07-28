package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubWebContentFetcher struct {
	mu        sync.Mutex
	contents  map[string]string
	errors    map[string]error
	callCount map[string]int
}

func (fetcher *stubWebContentFetcher) Fetch(_ context.Context, rawURL string) (string, error) {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	fetcher.callCount[rawURL]++
	if err := fetcher.errors[rawURL]; err != nil {
		return "", err
	}
	return fetcher.contents[rawURL], nil
}

func TestWebFetchToolSingleURLSuccessSurvivesSummaryFailure(t *testing.T) {
	const rawURL = "https://example.com/specs"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "official specifications"}, nil)
	tool := newWebFetchTool(nil, fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: rawURL, Prompt: "extract specifications"},
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, result.Data["successful_count"])
	items := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "success", items[0]["status"])
	assert.Equal(t, "failed", items[0]["summary_status"])
	assert.Equal(t, "official specifications", items[0]["raw_content"])
}

func TestWebFetchToolPreservesPartialSuccess(t *testing.T) {
	const successURL = "https://example.com/success"
	const failedURL = "https://example.com/forbidden"
	fetcher := newStubWebContentFetcher(
		map[string]string{successURL: "verified page content"},
		map[string]error{failedURL: fetchFailure(webfetch.ErrorHTTP403, false, "access denied")},
	)
	tool := newWebFetchTool(nil, fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: successURL, Prompt: "extract facts"},
		WebFetchItem{URL: failedURL, Prompt: "extract facts"},
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, result.Data["successful_count"])
	assert.Equal(t, 1, result.Data["failed_count"])
	assert.Equal(t, false, result.Data["all_failed"])
	items := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "success", items[0]["status"])
	assert.Equal(t, "failed", items[1]["status"])
	assert.Equal(t, "http_403", items[1]["error_code"])
	assert.Equal(t, false, items[1]["retryable"])
}

func TestWebFetchToolAllFailuresReturnStructuredFallback(t *testing.T) {
	const firstURL = "https://example.com/dns"
	const secondURL = "https://example.com/rate-limit"
	fetcher := newStubWebContentFetcher(nil, map[string]error{
		firstURL:  fetchFailure(webfetch.ErrorDNS, true, "DNS lookup failed"),
		secondURL: fetchFailure(webfetch.ErrorHTTP429, true, "rate limited"),
	})
	tool := newWebFetchTool(nil, fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: firstURL, Prompt: "extract facts"},
		WebFetchItem{URL: secondURL, Prompt: "extract facts"},
	))

	require.NoError(t, err)
	require.False(t, result.Success, "all-failed batches should not report tool success")
	assert.Equal(t, true, result.Data["all_failed"])
	assert.Equal(t, 0, result.Data["successful_count"])
	assert.Contains(t, result.Output, "answer from existing web_search titles, URLs, and snippets")
}

func TestWebFetchToolDeduplicatesURLsWithinBatch(t *testing.T) {
	const rawURL = "https://example.com/page#section"
	const duplicateURL = "https://example.com/page"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "page content"}, nil)
	tool := newWebFetchTool(nil, fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: rawURL, Prompt: "extract facts"},
		WebFetchItem{URL: duplicateURL, Prompt: "extract facts"},
	))

	require.NoError(t, err)
	assert.Equal(t, 1, fetcher.callCount[rawURL])
	assert.Equal(t, 0, fetcher.callCount[duplicateURL])
	assert.Equal(t, 1, result.Data["skipped_count"])
	items := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "duplicate_url", items[1]["error_code"])
}

func TestWebFetchToolDeduplicatesGitHubBlobAndRawURLs(t *testing.T) {
	const blobURL = "https://github.com/org/repo/blob/main/README.md"
	const rawURL = "https://raw.githubusercontent.com/org/repo/main/README.md"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "readme content"}, nil)
	tool := newWebFetchTool(nil, fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: blobURL, Prompt: "extract facts"},
		WebFetchItem{URL: rawURL, Prompt: "extract facts"},
	))

	require.NoError(t, err)
	assert.Equal(t, 1, fetcher.callCount[rawURL]+fetcher.callCount[blobURL])
	assert.Equal(t, 1, result.Data["skipped_count"])
}

func newStubWebContentFetcher(contents map[string]string, failures map[string]error) *stubWebContentFetcher {
	return &stubWebContentFetcher{
		contents:  contents,
		errors:    failures,
		callCount: make(map[string]int),
	}
}

func fetchFailure(code webfetch.ErrorCode, retryable bool, message string) error {
	return &webfetch.FetchError{Code: code, Retryable: retryable, Err: errors.New(message)}
}

func webFetchArgs(items ...WebFetchItem) json.RawMessage {
	encoded, _ := json.Marshal(WebFetchInput{Items: items})
	return encoded
}
