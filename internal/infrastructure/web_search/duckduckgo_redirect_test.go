package web_search

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type ddgRedirectTransport func(*http.Request) (*http.Response, error)

func (f ddgRedirectTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDuckDuckGoSearchResolvesRedirectQuery(t *testing.T) {
	target := "https://example.com/docs/C%2B%2B?q=a+b&next=%2Fguide#part"
	for _, prefix := range []string{"//duckduckgo.com/l/", "https://duckduckgo.com/l/"} {
		for _, query := range []string{
			"uddg=" + url.QueryEscape(target),
			"rut=tracking&uddg=" + url.QueryEscape(target),
			"uddg=" + url.QueryEscape(target) + "&rut=tracking",
		} {
			href := prefix + "?" + query
			t.Run(href, func(t *testing.T) {
				provider := &DuckDuckGoProvider{client: &http.Client{Transport: ddgRedirectTransport(
					func(r *http.Request) (*http.Response, error) {
						if r.URL.Host != "html.duckduckgo.com" {
							t.Errorf("unexpected API fallback: %s", r.URL)
						}
						body := `<div class="web-result"><a class="result__a" href="` + html.EscapeString(href) +
							`">Guide</a><div class="result__snippet">Details</div></div>`
						return &http.Response{
							StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)),
						}, nil
					},
				)}}
				results, err := provider.Search(t.Context(), types.WebSearchRequest{Query: "guide", MaxResults: 1})
				if err != nil {
					t.Fatal(err)
				}
				if len(results) != 1 {
					t.Fatalf("result count = %d, want 1", len(results))
				}
				if results[0].URL != target {
					t.Fatalf("URL = %q, want %q", results[0].URL, target)
				}
			})
		}
	}
}

func TestCleanDDGURLKeepsDirectAndUnrelatedLinks(t *testing.T) {
	for _, link := range []string{
		"https://example.com/a%2Fb?q=C%2B%2B#section",
		"https://other.example/l/?uddg=https%3A%2F%2Fexample.com",
		"https://duckduckgo.com/about",
	} {
		if got := cleanDDGURL(link); got != link {
			t.Fatalf("URL = %q, want %q", got, link)
		}
	}
}
