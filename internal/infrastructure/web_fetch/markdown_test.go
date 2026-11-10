package web_fetch

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentMarkdownPreservesStructureAndResolvesLinks(t *testing.T) {
	source := `<html><head><title>Reference</title></head>
<body><nav>NOISE</nav>
<main><h1>Guide</h1><p>Read <a href="../next">next page</a>.</p>
<ul><li>First</li><li>Second</li></ul>
<pre><code>if (a &lt; b) { return a; }</code></pre>
<table><tr><th>Name</th><th>Value</th></tr><tr><td>Count</td><td>2</td></tr></table>
</main></body></html>`
	content, err := htmlToMarkdown(source, "https://example.com/docs/guide")
	require.NoError(t, err)
	assert.Contains(t, content, "# Guide")
	assert.Contains(t, content, "[next page](https://example.com/next)")
	assert.Contains(t, content, "- First")
	assert.Contains(t, content, "if (a < b)")
	assert.Contains(t, content, "```")
	assert.Contains(t, content, "| Name")
	assert.NotContains(t, content, "NOISE")
}

func TestAgentFetchContentTypesAndSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		code                    ErrorCode
	}{
		{"plain", "text/plain", "if (a < b)\n<available_files>keep me</available_files>", ""},
		{"json", "application/json", `{"value":"<tag>"}`, ""},
		{"pdf", "application/pdf", "%PDF-1.7 binary", ErrorUnsupportedContent},
		{"oversize", "text/plain", strings.Repeat("x", 1025), ErrorBodyTooLarge},
		{"binary", "text/plain", "a\x00b", ErrorUnsupportedContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 200, Header: http.Header{"Content-Type": []string{tc.contentType}},
					Body: io.NopCloser(strings.NewReader(tc.body)), Request: req,
				}, nil
			})}
			f := newTestFetcher(client)
			f.markdown, f.maxBodySize = true, 1024
			content, err := f.Fetch(context.Background(), "https://example.com/file")
			if tc.code == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.body, content)
			} else {
				code, retryable, _ := ErrorDetails(err)
				assert.Equal(t, tc.code, code)
				assert.False(t, retryable)
			}
		})
	}
}

func TestAgentFetchAcceptsSuccessfulHTTPStatuses(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 201, Header: http.Header{"Content-Type": []string{"text/plain"}},
			Body: io.NopCloser(strings.NewReader("created page")), Request: req,
		}, nil
	})}
	f := newTestFetcher(client)
	f.markdown = true
	content, err := f.Fetch(t.Context(), "https://example.com/page")
	require.NoError(t, err)
	require.Equal(t, "created page", content)
	f.markdown = false
	_, err = f.Fetch(t.Context(), "https://example.com/page")
	require.Error(t, err, "quick-answer status behavior stays unchanged")
}

// Check preservation of article sections, links, lists, tables and code.
func TestMarkdownExtractionFixtures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		markers []string
	}{
		{"article", []string{"FIRST_SECTION", "SECOND_SECTION", "FINAL_SECTION", "https://example.com/reference"}},
		{"structure", []string{
			"Guide", "next page", "https://example.com/next", "First item", "Second item", "if (a < b)", "Count",
		}},
		{"fallback", []string{
			"REFERENCE_DIRECTORY", "Reference entry 0", "Reference entry 11", "https://example.com/item/11",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", "extraction", tc.name+".html"))
			require.NoError(t, err)
			actual, err := htmlToMarkdown(string(source), "https://example.com/docs/guide")
			require.NoError(t, err)
			for _, marker := range tc.markers {
				require.Contains(t, strings.ReplaceAll(actual, "\\", ""), marker)
			}
			require.NotContains(t, actual, "Navigation noise")
			require.NotContains(t, actual, "Footer noise")
			if tc.name == "structure" {
				require.Contains(t, actual, "    return a;", "preserve code indentation")
				require.NotContains(t, actual, "https://example.com/empty")
			}
		})
	}
}
