package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestNewSerpAPIProvider(t *testing.T) {
	if _, err := NewSerpAPIProvider(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	if _, err := NewSerpAPIProvider(types.WebSearchProviderParameters{
		APIKey: "test", ExtraConfig: map[string]string{"engine": "unknown"},
	}); err == nil {
		t.Fatal("expected unsupported engine error")
	}

	provider, err := NewSerpAPIProvider(types.WebSearchProviderParameters{APIKey: "test"})
	if err != nil {
		t.Fatalf("NewSerpAPIProvider: %v", err)
	}
	if provider.Name() != "serpapi" {
		t.Fatalf("Name() = %q, want serpapi", provider.Name())
	}
}

func TestSerpAPIProviderSearchOrganicResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("engine"); got != "google_scholar" {
			t.Errorf("engine = %q, want google_scholar", got)
		}
		if got := r.URL.Query().Get("q"); got != "knowledge graph" {
			t.Errorf("q = %q, want knowledge graph", got)
		}
		if got := r.URL.Query().Get("api_key"); got != "secret" {
			t.Errorf("api_key = %q, want secret", got)
		}
		if got := r.URL.Query().Get("num"); got != "2" {
			t.Errorf("num = %q, want 2", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organic_results": []map[string]any{
				{"title": "Paper 1", "link": "https://example.com/1", "snippet": "S1", "date": "2026-07-19"},
				{"title": "Paper 2", "link": "https://example.com/2", "snippet": "S2"},
				{"title": "Paper 3", "link": "https://example.com/3", "snippet": "S3"},
			},
		})
	}))
	defer srv.Close()

	p := &SerpAPIProvider{
		client: srv.Client(), baseURL: srv.URL,
		apiKey: "secret", engine: "google_scholar",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := p.Search(ctx, "knowledge graph", 2, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Title != "Paper 1" || results[0].URL != "https://example.com/1" || results[0].Snippet != "S1" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].Source != "serpapi" || results[0].PublishedAt == nil {
		t.Fatalf("source/date not mapped: %+v", results[0])
	}
}

func TestSerpAPIProviderSearchAlternateResultSets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"news_results": []map[string]any{
				{"title": "News", "link": "https://example.com/news", "description": "Description"},
				{"title": "No URL"},
			},
		})
	}))
	defer srv.Close()

	p := &SerpAPIProvider{client: srv.Client(), baseURL: srv.URL, apiKey: "secret", engine: "google_news"}
	results, err := p.Search(context.Background(), "q", 5, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Snippet != "Description" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestSerpAPIProviderSearchErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "http status", status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`},
		{name: "payload error", status: http.StatusOK, body: `{"error":"invalid api key"}`},
		{name: "invalid json", status: http.StatusOK, body: `{`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p := &SerpAPIProvider{client: srv.Client(), baseURL: srv.URL, apiKey: "secret", engine: "google"}
			if _, err := p.Search(context.Background(), "q", 1, false); err == nil {
				t.Fatal("expected search error")
			}
		})
	}
}

func TestNormalizeSerpAPIEngine(t *testing.T) {
	if got, err := normalizeSerpAPIEngine(""); err != nil || got != "google" {
		t.Fatalf("default engine = %q, err=%v", got, err)
	}
	if got, err := normalizeSerpAPIEngine(" GOOGLE_SCHOLAR "); err != nil || got != "google_scholar" {
		t.Fatalf("normalized engine = %q, err=%v", got, err)
	}
}
