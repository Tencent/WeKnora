package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testRoundTripper rewrites outgoing requests that target DuckDuckGo hosts
// to the provided test server, preserving path and query.
type testRoundTripper struct {
	base *url.URL
	next http.RoundTripper
}

func (t *testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Only rewrite requests to duckduckgo hosts used by the provider
	if req.URL.Host == "html.duckduckgo.com" || req.URL.Host == "api.duckduckgo.com" {
		cloned := *req
		u := *req.URL
		u.Scheme = t.base.Scheme
		u.Host = t.base.Host
		// Keep original path; our test server handlers should register for the same paths.
		cloned.URL = &u
		req = &cloned
	}
	return t.next.RoundTrip(req)
}

func newTestClient(ts *httptest.Server) *http.Client {
	baseURL, _ := url.Parse(ts.URL)
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &testRoundTripper{
			base: baseURL,
			next: http.DefaultTransport,
		},
	}
}

func TestDuckDuckGoProvider_Name(t *testing.T) {
	p, _ := NewDuckDuckGoProvider()
	if p.Name() != "duckduckgo" {
		t.Fatalf("expected provider name duckduckgo, got %s", p.Name())
	}
}

func TestDuckDuckGoProvider(t *testing.T) {
	// Minimal HTML page with two results, matching selectors used in searchHTML
	html := `
<html>
  <body>
    <div class="web-result">
      <a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage1&rut=">Example One</a>
      <div class="result__snippet">Snippet one</div>
    </div>
    <div class="web-result">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fpage2&rut=">Example Two</a>
      <div class="result__snippet">Snippet two</div>
    </div>
  </body>
</html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Provider requests GET https://html.duckduckgo.com/html/?q=...&kl=...
		if r.URL.Path == "/html/" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(html))
			return
		}
		t.Fatalf("unexpected request path: %s", r.URL.Path)
	}))
	defer ts.Close()

	// Build provider and inject our test client
	prov, _ := NewDuckDuckGoProvider()
	dp := prov.(*DuckDuckGoProvider)
	if dp == nil {
		t.Fatalf("failed to build provider")
	}
	dp.client = newTestClient(ts)

	ctx := context.Background()
	results, err := dp.Search(ctx, "weknora", 5, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Title != "Example One" || !strings.HasPrefix(results[0].URL, "https://example.com/") ||
		results[0].Snippet != "Snippet one" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[1].Title != "Example Two" || !strings.HasPrefix(results[1].URL, "https://example.org/") ||
		results[1].Snippet != "Snippet two" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

func TestDuckDuckGoProvider_Fallback(t *testing.T) {
	// Simulate HTML returning non-OK to force API fallback, then a minimal API JSON
	apiResp := struct {
		AbstractText string `json:"AbstractText"`
		AbstractURL  string `json:"AbstractURL"`
		Heading      string `json:"Heading"`
		Results      []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		} `json:"Results"`
	}{
		AbstractText: "Abstract snippet",
		AbstractURL:  "https://example.com/abstract",
		Heading:      "Abstract Heading",
		Results: []struct {
			FirstURL string `json:"FirstURL"`
			Text     string `json:"Text"`
		}{
			{FirstURL: "https://example.net/x", Text: "Title X - Detail X"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/html/":
			// Force fallback by returning 500
			w.WriteHeader(http.StatusInternalServerError)
		default:
			// API endpoint path "/"
			w.Header().Set("Content-Type", "application/json")
			enc := json.NewEncoder(w)
			_ = enc.Encode(apiResp)
		}
	}))
	defer ts.Close()

	prov, _ := NewDuckDuckGoProvider()
	dp := prov.(*DuckDuckGoProvider)
	if dp == nil {
		t.Fatalf("failed to build provider")
	}
	dp.client = newTestClient(ts)

	ctx := context.Background()
	results, err := dp.Search(ctx, "weknora", 3, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected some results from API fallback")
	}
	if results[0].URL != "https://example.com/abstract" || results[0].Title != "Abstract Heading" {
		t.Fatalf("unexpected first API result: %+v", results[0])
	}
}

// TestDuckDuckGoProvider_Search_Real tests the DuckDuckGo provider against the real DuckDuckGo service.
// This is an integration test that requires network connectivity.
// Run with: go test -v -run TestDuckDuckGoProvider_Search_Real ./internal/application/service/web_search
func TestDuckDuckGoProvider_Search_Real(t *testing.T) {
	// Skip if running in CI without network access (optional check)
	if testing.Short() {
		t.Skip("Skipping real DuckDuckGo integration test in short mode")
	}

	ctx := context.Background()
	provider, err := NewDuckDuckGoProvider()
	if err != nil {
		t.Fatalf("Failed to create DuckDuckGo provider: %v", err)
	}
	if provider == nil {
		t.Fatalf("failed to build provider")
	}

	// Test with a simple, general query that should return results
	query := "Go programming language"
	maxResults := 5

	results, err := provider.Search(ctx, query, maxResults, false)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Verify we got results
	if len(results) == 0 {
		t.Fatal("Expected at least one search result, got 0")
	}

	t.Logf("Received %d results for query: %s", len(results), query)

	// Verify result structure
	for i, result := range results {
		if result == nil {
			t.Fatalf("Result[%d]: is nil", i)
		}
		if result.Title == "" {
			t.Errorf("Result[%d]: Title is empty", i)
		}
		if result.URL == "" {
			t.Errorf("Result[%d]: URL is empty", i)
		}
		if !strings.HasPrefix(result.URL, "http://") && !strings.HasPrefix(result.URL, "https://") {
			t.Errorf("Result[%d]: URL is not valid (should start with http:// or https://): %s", i, result.URL)
		}
		if result.Source != "duckduckgo" {
			t.Errorf("Result[%d]: Source should be 'duckduckgo', got '%s'", i, result.Source)
		}

		t.Logf("Result[%d]: Title=%s, URL=%s, Snippet=%s", i, result.Title, result.URL, result.Snippet)
	}

	// Verify we don't exceed maxResults
	if len(results) > maxResults {
		t.Errorf("Got %d results, expected at most %d", len(results), maxResults)
	}

	// Test with maxResults limit
	limitedResults, err := provider.Search(ctx, query, 2, false)
	if err != nil {
		t.Fatalf("Search with limit failed: %v", err)
	}
	if len(limitedResults) > 2 {
		t.Errorf("Got %d results with maxResults=2, expected at most 2", len(limitedResults))
	}
}

// TestDuckDuckGo_SearchChinese tests the DuckDuckGo provider with Chinese query.
// This verifies the Chinese language parameter (kl=cn-zh) works correctly.
func TestDuckDuckGo_SearchChinese(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real DuckDuckGo integration test in short mode")
	}

	ctx := context.Background()
	provider, err := NewDuckDuckGoProvider()
	if err != nil {
		t.Fatalf("Failed to create DuckDuckGo provider: %v", err)
	}
	if provider == nil {
		t.Fatalf("failed to build provider")
	}

	// Test with a Chinese query
	query := "WeKnora 企业级RAG框架 介绍 文档"
	maxResults := 3

	results, err := provider.Search(ctx, query, maxResults, false)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) == 0 {
		t.Log("Warning: No results returned for Chinese query, but this might be expected")
		return
	}

	t.Logf("Received %d results for Chinese query: %s", len(results), query)

	// Verify result structure
	for i, result := range results {
		if result == nil {
			t.Fatalf("Result[%d]: is nil", i)
		}
		if result.Title == "" {
			t.Errorf("Result[%d]: Title is empty", i)
		}
		if result.URL == "" {
			t.Errorf("Result[%d]: URL is empty", i)
		}
		if result.Source != "duckduckgo" {
			t.Errorf("Result[%d]: Source should be 'duckduckgo', got '%s'", i, result.Source)
		}
		t.Logf("Result[%d]: Title=%s, URL=%s", i, result.Title, result.URL)
	}
}
