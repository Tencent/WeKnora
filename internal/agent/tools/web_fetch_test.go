package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/types"
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

func TestWebFetchSchemaExposesOnlyPageReadParameters(t *testing.T) {
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"items"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(NewWebFetchTool().Parameters(), &schema))
	item := schema.Properties["items"].Items
	require.Len(t, item.Properties, 3)
	assert.Contains(t, item.Properties, "url")
	assert.Contains(t, item.Properties, "offset")
	assert.Contains(t, item.Properties, "limit")
	assert.NotContains(t, item.Properties, "prompt")
	assert.Equal(t, []string{"url"}, item.Required)
}

func TestWebFetchToolReturnsPageWithoutSummaryModel(t *testing.T) {
	const rawURL = "https://example.com/specs"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "official specifications"}, nil)
	tool := newWebFetchTool(fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: rawURL},
	))

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, result.Data["successful_count"])
	items := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "success", items[0]["status"])
	assert.NotContains(t, items[0], "summary_status")
	assert.Equal(t, "official specifications", items[0]["raw_content"])
}

func TestWebFetchToolPreservesPartialSuccess(t *testing.T) {
	const successURL = "https://example.com/success"
	const failedURL = "https://example.com/forbidden"
	fetcher := newStubWebContentFetcher(
		map[string]string{successURL: "verified page content"},
		map[string]error{failedURL: fetchFailure(webfetch.ErrorHTTP403, false, "access denied")},
	)
	tool := newWebFetchTool(fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: successURL},
		WebFetchItem{URL: failedURL},
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
	tool := newWebFetchTool(fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: firstURL},
		WebFetchItem{URL: secondURL},
	))

	require.NoError(t, err)
	require.False(t, result.Success, "all-failed batches should not report tool success")
	assert.Equal(t, true, result.Data["all_failed"])
	assert.Equal(t, 0, result.Data["successful_count"])
	assert.Contains(t, result.Output, "use another relevant source")
}

func TestWebFetchToolDeduplicatesURLsWithinBatch(t *testing.T) {
	const rawURL = "https://example.com/page#section"
	const duplicateURL = "https://example.com/page"
	fetcher := newStubWebContentFetcher(map[string]string{duplicateURL: "page content"}, nil)
	tool := newWebFetchTool(fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: rawURL},
		WebFetchItem{URL: duplicateURL},
	))

	require.NoError(t, err)
	assert.Equal(t, 0, fetcher.callCount[rawURL])
	assert.Equal(t, 1, fetcher.callCount[duplicateURL])
	assert.Equal(t, 1, result.Data["skipped_count"])
	items := result.Data["results"].([]map[string]interface{})
	assert.Equal(t, "duplicate_url", items[1]["error_code"])
}

func TestWebFetchToolDeduplicatesGitHubBlobAndRawURLs(t *testing.T) {
	const blobURL = "https://github.com/org/repo/blob/main/README.md"
	const rawURL = "https://raw.githubusercontent.com/org/repo/main/README.md"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "readme content"}, nil)
	tool := newWebFetchTool(fetcher)

	result, err := tool.Execute(context.Background(), webFetchArgs(
		WebFetchItem{URL: blobURL},
		WebFetchItem{URL: rawURL},
	))

	require.NoError(t, err)
	assert.Equal(t, 1, fetcher.callCount[rawURL]+fetcher.callCount[blobURL])
	assert.Equal(t, 1, result.Data["skipped_count"])
}

func TestWebFetchToolUnwrapsDoubleEncodedItemsString(t *testing.T) {
	const rawURL = "https://example.com/article"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "article content"}, nil)
	tool := newWebFetchTool(fetcher)

	// Some models emit {"items":"[{\"url\":...}]"} instead of a real array.
	itemsJSON, _ := json.Marshal([]WebFetchItem{{URL: rawURL}})
	encoded, _ := json.Marshal(map[string]string{"items": string(itemsJSON)})

	result, err := tool.Execute(context.Background(), encoded)

	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 1, fetcher.callCount[rawURL])
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

func TestWebFetchPaginationUsesSnapshotAndUnicodeOffsets(t *testing.T) {
	const rawURL = "https://example.com/page"
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "你好世界abcdef"}, nil)
	tool := newWebFetchTool(fetcher)
	registry := NewToolRegistry()
	registry.RegisterTool(tool)
	first, err := registry.ExecuteTool(t.Context(), ToolWebFetch, webFetchArgs(WebFetchItem{URL: rawURL, Limit: 3}))
	require.NoError(t, err)
	require.True(t, first.Success, first.Error)
	row := first.Data["results"].([]map[string]interface{})[0]
	assert.Equal(t, "你好世", row["raw_content"])
	assert.Equal(t, 3, row["next_offset"])
	fetcher.contents[rawURL] = "changed page"
	second, err := registry.ExecuteTool(t.Context(), ToolWebFetch, webFetchArgs(WebFetchItem{URL: rawURL, Offset: 3}))
	require.NoError(t, err)
	require.True(t, second.Success, second.Error)
	row = second.Data["results"].([]map[string]interface{})[0]
	assert.Equal(t, "界abcdef", row["raw_content"])
	assert.Equal(t, false, row["truncated"])
	assert.Equal(t, 1, fetcher.callCount[rawURL])
}

func TestWebFetchBatchRetainsEveryPageWithinBudget(t *testing.T) {
	contents := map[string]string{}
	items := []WebFetchItem{}
	for i := 0; i < 8; i++ {
		u := fmt.Sprintf("https://example.com/%d", i)
		contents[u] = strings.Repeat("页面内容", 5000)
		items = append(items, WebFetchItem{URL: u})
	}
	tool := newWebFetchTool(newStubWebContentFetcher(contents, nil))
	registry := NewToolRegistry()
	registry.SetMaxToolOutputSize(12000)
	registry.RegisterTool(tool)
	result, err := registry.ExecuteTool(t.Context(), ToolWebFetch, webFetchArgs(items...))
	require.NoError(t, err)
	require.True(t, result.Success)
	require.LessOrEqual(t, utf8.RuneCountInString(result.Output), 12000)
	for _, row := range result.Data["results"].([]map[string]interface{}) {
		assert.NotEmpty(t, row["raw_content"])
		assert.Contains(t, result.Output, row["url"])
		assert.Contains(t, result.Output, fmt.Sprintf("offset=%d", row["next_offset"]))
	}
}

func TestWebFetchRejectsInvalidRequestsWithoutNetwork(t *testing.T) {
	fetcher := newStubWebContentFetcher(nil, nil)
	tool := newWebFetchTool(fetcher)
	for _, item := range []WebFetchItem{
		{URL: "w123"},
		{URL: "file:///etc/passwd"},
		{URL: "https://example.com", Offset: -1},
		{URL: "https://example.com", Limit: 8001},
		{URL: "https://example.com", Offset: 2},
	} {
		result, err := tool.Execute(t.Context(), webFetchArgs(item))
		require.NoError(t, err)
		assert.False(t, result.Success)
	}
	result, err := tool.Execute(t.Context(), webFetchArgs(make([]WebFetchItem, 9)...))
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Empty(t, fetcher.callCount)
}

func TestWebFetchHandleRoundTripAndContinuation(t *testing.T) {
	const rawURL = "https://example.com/guide"
	source := modelcontext.NewRegistry(true)
	source.RegisterWeb(rawURL, "Guide")
	fetcher := newStubWebContentFetcher(map[string]string{rawURL: "first second"}, nil)
	registry := NewToolRegistry()
	registry.RegisterTool(newWebFetchTool(fetcher))
	// Exercise double-encoded items through the real model-context and registry boundaries.
	raw := `{"items":"[{\"url\":\"w1\",\"limit\":5}]"}`
	calls := []types.LLMToolCall{{Function: types.FunctionCall{Name: ToolWebFetch, Arguments: raw}}}
	source.DecodeToolCalls(calls)
	assert.Equal(t, raw, calls[0].ModelArguments)
	result, err := registry.ExecuteTool(t.Context(), ToolWebFetch, json.RawMessage(calls[0].Function.Arguments))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	output := source.ModelToolResultForTool(ToolWebFetch, result)
	assert.Contains(t, output, "first")
	assert.Contains(t, output, `url="w1" next_offset="5"`)
	assert.NotContains(t, output, rawURL)
	assert.Equal(t, 1, fetcher.callCount[rawURL])
}

func TestNormalizeGitHubURLDoesNotRewriteLookalikeHosts(t *testing.T) {
	for _, u := range []string{
		"https://evilgithub.com/o/r/blob/main/file",
		"https://example.com/github.com/o/r/blob/main/file",
	} {
		assert.Equal(t, u, normalizeGitHubURL(u))
	}
}

func TestWebFetchDoesNotCacheFailuresAndBoundsSnapshots(t *testing.T) {
	const rawURL = "https://example.com/retry"
	fetcher := newStubWebContentFetcher(
		map[string]string{rawURL: "available again"},
		map[string]error{rawURL: fetchFailure(webfetch.ErrorHTTP429, true, "retry later")},
	)
	tool := newWebFetchTool(fetcher)
	first, err := tool.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: rawURL}))
	require.NoError(t, err)
	assert.False(t, first.Success)
	delete(fetcher.errors, rawURL)
	second, err := tool.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: rawURL}))
	require.NoError(t, err)
	assert.True(t, second.Success)
	assert.Equal(t, 2, fetcher.callCount[rawURL])
	for i := 0; i < 8; i++ {
		u := fmt.Sprintf("https://example.com/new/%d", i)
		fetcher.contents[u] = "another page"
		_, err := tool.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: u}))
		require.NoError(t, err)
	}
	assert.Len(t, tool.pages, 8)
	expired, err := tool.Execute(t.Context(), webFetchArgs(WebFetchItem{URL: rawURL, Offset: 2}))
	require.NoError(t, err)
	assert.False(t, expired.Success)
	assert.Contains(t, expired.Output, "snapshot_expired")
	assert.Contains(t, expired.Output, "Retryable: true")
	assert.Equal(t, 2, fetcher.callCount[rawURL], "must not splice a new page into a continuation")
}

type gatingWebContentFetcher struct {
	*stubWebContentFetcher
	started, release chan struct{}
}

func (f *gatingWebContentFetcher) Fetch(ctx context.Context, rawURL string) (string, error) {
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	select {
	case <-f.release:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return f.stubWebContentFetcher.Fetch(ctx, rawURL)
}

func TestWebFetchBatchContinuationWaitsForInFlightSnapshot(t *testing.T) {
	const rawURL = "https://example.com/page"
	fetcher := &gatingWebContentFetcher{
		stubWebContentFetcher: newStubWebContentFetcher(map[string]string{rawURL: "abcdefghij"}, nil),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	tool := newWebFetchTool(fetcher)
	done := make(chan *types.ToolResult, 1)
	go func() {
		result, err := tool.Execute(t.Context(), webFetchArgs(
			WebFetchItem{URL: rawURL, Limit: 4},
			WebFetchItem{URL: rawURL, Offset: 4},
		))
		require.NoError(t, err)
		done <- result
	}()
	<-fetcher.started
	close(fetcher.release)
	result := <-done
	require.True(t, result.Success, result.Error)
	items := result.Data["results"].([]map[string]interface{})
	require.Len(t, items, 2)
	assert.Equal(t, "abcd", items[0]["raw_content"])
	assert.Equal(t, "efghij", items[1]["raw_content"])
	assert.Equal(t, 1, fetcher.callCount[rawURL])
}

func TestWebFetchContinuationDoesNotCancelSharedFetch(t *testing.T) {
	const rawURL = "https://example.com/shared"
	fetcher := &gatingWebContentFetcher{
		stubWebContentFetcher: newStubWebContentFetcher(map[string]string{rawURL: "shared page body"}, nil),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	tool := newWebFetchTool(fetcher)
	longDone := make(chan *types.ToolResult, 1)
	go func() {
		result, err := tool.Execute(context.Background(), webFetchArgs(WebFetchItem{URL: rawURL}))
		require.NoError(t, err)
		longDone <- result
	}()
	<-fetcher.started
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	shortResult, err := tool.Execute(short, webFetchArgs(WebFetchItem{URL: rawURL, Offset: 2}))
	require.NoError(t, err)
	require.False(t, shortResult.Success)
	assert.Contains(t, shortResult.Output, "connection_timeout")
	close(fetcher.release)
	longResult := <-longDone
	require.True(t, longResult.Success, longResult.Error)
	assert.Equal(t, 1, fetcher.callCount[rawURL])
}

func TestWebFetchOwnerTimeoutDoesNotCancelSharedFetch(t *testing.T) {
	const rawURL = "https://example.com/owner"
	fetcher := &gatingWebContentFetcher{
		stubWebContentFetcher: newStubWebContentFetcher(map[string]string{rawURL: "owner page body"}, nil),
		started:               make(chan struct{}),
		release:               make(chan struct{}),
	}
	tool := newWebFetchTool(fetcher)
	longDone := make(chan *types.ToolResult, 1)
	go func() {
		result, err := tool.Execute(context.Background(), webFetchArgs(WebFetchItem{URL: rawURL}))
		require.NoError(t, err)
		longDone <- result
	}()
	<-fetcher.started
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	shortResult, err := tool.Execute(short, webFetchArgs(WebFetchItem{URL: rawURL}))
	require.NoError(t, err)
	require.False(t, shortResult.Success)
	assert.Contains(t, shortResult.Output, "connection_timeout")
	close(fetcher.release)
	longResult := <-longDone
	require.True(t, longResult.Success, longResult.Error)
	assert.Equal(t, "owner page body", longResult.Data["results"].([]map[string]interface{})[0]["raw_content"])
	assert.Equal(t, 1, fetcher.callCount[rawURL])
}
