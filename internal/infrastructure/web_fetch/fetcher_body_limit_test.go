package web_fetch

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bodyLimitTailMarker is the last text node of every synthetic oversize page
// built below. It is the canary for "the tail of the body never made it".
const bodyLimitTailMarker = "WEKNORA_BODY_LIMIT_TAIL_MARKER"

// htmlPageOfSize builds an HTML page of roughly bodyBytes whose final text
// node is bodyLimitTailMarker.
func htmlPageOfSize(bodyBytes int) string {
	const paragraph = "<p>Reference paragraph kept for size-limit coverage.</p>"
	var page strings.Builder
	page.WriteString("<html><body><main>")
	for page.Len() < bodyBytes {
		page.WriteString(paragraph)
	}
	page.WriteString("<p>" + bodyLimitTailMarker + "</p></main></body></html>")
	return page.String()
}

// fetcherServingBody returns a fetcher configured like a production branch
// (same markdown flag and body limit) whose transport replays body verbatim.
func fetcherServingBody(markdown bool, limit int64, body string) *Fetcher {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}
	fetcher := newTestFetcher(client)
	fetcher.markdown, fetcher.maxBodySize = markdown, limit
	return fetcher
}

// An oversize body must fail on both fetch branches instead of being cut down
// to the limit and reported as a successful fetch: a truncated body loses the
// tail of the page without any signal to the caller. The markdown branch
// already rejects; the pipeline branch used to return the first N bytes.
func TestOversizeBodyIsRejectedOnBothFetchBranches(t *testing.T) {
	const limit = 1024
	body := htmlPageOfSize(limit + 4096)
	require.Greater(t, len(body), limit)

	for _, test := range []struct {
		name     string
		markdown bool
	}{
		{name: "pipeline", markdown: false},
		{name: "markdown", markdown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, err := fetcherServingBody(test.markdown, limit, body).
				Fetch(context.Background(), "https://example.com/large")

			require.Error(t, err, "an oversize body must not be reported as a successful fetch")
			code, retryable, _ := ErrorDetails(err)
			assert.Equal(t, ErrorBodyTooLarge, code)
			assert.False(t, retryable)
			assert.NotContains(t, content, bodyLimitTailMarker)
		})
	}
}

// The chat pipeline keeps its own 100 KiB limit. A page above that limit must
// surface body_too_large so the caller can fall back to the search snippet
// rather than indexing a page whose second half is missing.
func TestPipelineFetcherRejectsPageOverProductionLimit(t *testing.T) {
	body := htmlPageOfSize(int(maxBodySize) + 64*1024)
	require.Greater(t, len(body), int(maxBodySize))

	fetcher := NewPipelineFetcher()
	fetcher.validateURL = func(string) error { return nil }
	fetcher.client = fetcherServingBody(false, maxBodySize, body).client

	content, err := fetcher.Fetch(context.Background(), "https://example.com/huge")

	require.Error(t, err)
	code, retryable, _ := ErrorDetails(err)
	assert.Equal(t, ErrorBodyTooLarge, code)
	assert.False(t, retryable)
	assert.NotContains(t, content, bodyLimitTailMarker)
}

// The limit stays exclusive-below: a page that fits must still be returned
// whole on both branches, with its tail intact.
func TestBodyWithinLimitIsAcceptedWholeOnBothFetchBranches(t *testing.T) {
	const limit = 4096
	body := htmlPageOfSize(2048)
	require.Less(t, len(body), limit)

	for _, markdown := range []bool{false, true} {
		content, err := fetcherServingBody(markdown, limit, body).
			Fetch(context.Background(), "https://example.com/fits")

		require.NoError(t, err, "markdown=%v", markdown)
		// markdown escapes underscores, so compare unescaped text.
		assert.Contains(t, strings.ReplaceAll(content, "\\", ""), bodyLimitTailMarker, "markdown=%v", markdown)
	}
}
