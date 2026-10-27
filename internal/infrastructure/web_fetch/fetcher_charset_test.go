package web_fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestFetcherDecodesHTMLCharset(t *testing.T) {
	const text = "腾讯知识库网页正文"
	gbk := func(html string) []byte {
		encoded, err := simplifiedchinese.GBK.NewEncoder().String(html)
		require.NoError(t, err)
		return []byte(encoded)
	}
	// An ASCII-only first KB with no declared charset must still be read as UTF-8.
	asciiPrefix := "<!-- " + strings.Repeat("x", 1100) + " -->"

	for _, tc := range []struct {
		name        string
		contentType string
		body        []byte
	}{
		{
			name:        "gbk header",
			contentType: "text/html; charset=gbk",
			body:        gbk("<html><body><main>" + text + "</main></body></html>"),
		},
		{
			name:        "gbk meta only",
			contentType: "text/html",
			body:        gbk(`<html><head><meta charset="gbk"></head><body><main>` + text + "</main></body></html>"),
		},
		{
			name:        "utf-8 header",
			contentType: "text/html; charset=utf-8",
			body:        []byte("<html><body><main>" + text + "</main></body></html>"),
		},
		{
			name:        "undeclared utf-8",
			contentType: "text/html",
			body:        []byte("<html>" + asciiPrefix + "<body><main>" + text + "</main></body></html>"),
		},
	} {
		for _, markdown := range []bool{false, true} {
			name := tc.name + "/pipeline"
			if markdown {
				name = tc.name + "/agent"
			}
			t.Run(name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", tc.contentType)
					_, _ = writer.Write(tc.body)
				}))
				defer server.Close()

				fetcher := newTestFetcher(server.Client())
				if markdown {
					fetcher.markdown = true
					fetcher.maxBodySize = maxAgentBodySize
				}
				content, err := fetcher.Fetch(context.Background(), server.URL)

				require.NoError(t, err)
				assert.Contains(t, content, text)
			})
		}
	}
}
