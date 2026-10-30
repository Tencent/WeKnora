package web_fetch

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMarkdownResolvesDocumentBase(t *testing.T) {
	for _, tc := range []struct{ name, base, root string }{
		{"absolute", `<base href="https://cdn.example.org/reference/">`, "https://cdn.example.org/reference/"},
		{"relative", `<base href="../assets/">`, "https://example.com/assets/"},
		{
			"first href wins",
			`<base target="_blank"><base href="/first/"><base href="/second/">`,
			"https://example.com/first/",
		},
		{"no base", "", "https://example.com/docs/"},
		{"unsupported scheme", `<base href="javascript:alert(1)">`, "https://example.com/docs/"},
	} {
		for _, longArticle := range []bool{false, true} {
			name := tc.name + "/short"
			if longArticle {
				name = tc.name + "/article"
			}
			t.Run(name, func(t *testing.T) {
				prose := "Reference content."
				if longArticle {
					prose = strings.Repeat("This technical reference explains the complete configuration. ", 30)
				}
				source := "<html><head><title>Reference</title>" + tc.base + "</head><body><main><article><p>" +
					prose + ` Read <a href="manual.html">manual</a>.</p>` +
					`<img src="diagram.png" alt="diagram"></article></main></body></html>`
				md, err := htmlToMarkdown(source, "https://example.com/docs/page.html")
				if err != nil {
					t.Fatal(err)
				}
				for _, path := range []string{"manual.html", "diagram.png"} {
					if !strings.Contains(md, tc.root+path) {
						t.Fatalf("missing %s in %s", tc.root+path, md)
					}
				}
			})
		}
	}
}

func TestFetcherResolvesBaseAgainstRedirectDestination(t *testing.T) {
	finalURL, err := url.Parse("https://example.com/redirected/docs/page.html")
	if err != nil {
		t.Fatal(err)
	}
	source := `<html><head><base href="../assets/"></head><body><main>` +
		`<p>Read <a href="manual.html">manual</a> for the reference.</p></main></body></html>`
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		finalRequest := req.Clone(req.Context())
		finalRequest.URL = finalURL
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/html"}},
			Body: io.NopCloser(strings.NewReader(source)), Request: finalRequest,
		}, nil
	})}
	fetcher := newTestFetcher(client)
	fetcher.markdown = true
	md, err := fetcher.Fetch(t.Context(), "https://example.com/original")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "https://example.com/redirected/assets/manual.html") {
		t.Fatalf("link used request URL instead of final response URL: %s", md)
	}
	if calls != 1 {
		t.Fatalf("HTTP requests = %d, want only the page request", calls)
	}
}
