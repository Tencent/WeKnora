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

func TestKeenableProvider_Search_Keyless(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No key configured => keyless public endpoint, no X-API-Key.
		if r.URL.Path != "/v1/search/public" {
			t.Errorf("path = %q, want /v1/search/public", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "" {
			t.Errorf("X-API-Key = %q, want empty for keyless", got)
		}
		if got := r.Header.Get("X-Keenable-Title"); got != "WeKnora" {
			t.Errorf("X-Keenable-Title = %q, want WeKnora", got)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "hello" || body["mode"] != "pro" {
			t.Errorf("body = %v, want query=hello mode=pro", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query": "hello",
			"results": []map[string]any{
				{"title": "T1", "url": "https://e/1", "description": "d1", "published_at": "2026-05-01T00:00:00Z"},
				{"title": "T2", "url": "https://e/2", "description": "d2"},
				{"title": "T3", "url": "https://e/3", "description": "d3"},
			},
		})
	}))
	defer srv.Close()

	p := &KeenableProvider{client: srv.Client(), baseURL: srv.URL}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := p.Search(ctx, "hello", 2, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (maxResults trim), got %d", len(results))
	}
	if results[0].Source != "keenable" {
		t.Errorf("source = %q, want keenable", results[0].Source)
	}
	if results[0].Snippet != "d1" || results[0].URL != "https://e/1" {
		t.Errorf("unexpected first result: %+v", results[0])
	}
	if results[0].PublishedAt == nil {
		t.Errorf("expected PublishedAt to be set on the first result")
	}
}

func TestKeenableProvider_Search_SnippetMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				// Has both: description is the short summary, snippet the longer excerpt.
				{"title": "T1", "url": "https://e/1", "description": "short", "snippet": "long excerpt"},
				// Only snippet present: it must fall back into Snippet so we don't drop text.
				{"title": "T2", "url": "https://e/2", "snippet": "only long"},
			},
		})
	}))
	defer srv.Close()

	p := &KeenableProvider{client: srv.Client(), baseURL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := p.Search(ctx, "q", 5, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Snippet != "short" || results[0].Content != "long excerpt" {
		t.Errorf("result[0] snippet/content mapping wrong: %+v", results[0])
	}
	if results[1].Snippet != "only long" {
		t.Errorf("result[1] should fall back to snippet when description is empty: %+v", results[1])
	}
}

func TestKeenableProvider_Search_Keyed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A configured key => authenticated endpoint + X-API-Key.
		if r.URL.Path != "/v1/search" {
			t.Errorf("path = %q, want /v1/search", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "keen_test" {
			t.Errorf("X-API-Key = %q, want keen_test", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}})
	}))
	defer srv.Close()

	p := &KeenableProvider{client: srv.Client(), baseURL: srv.URL, apiKey: "keen_test"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Search(ctx, "hi", 5, false); err != nil {
		t.Fatalf("Search: %v", err)
	}
}

func TestKeenableProvider_Search_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"rate limited"}`))
	}))
	defer srv.Close()

	p := &KeenableProvider{client: srv.Client(), baseURL: srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := p.Search(ctx, "x", 5, false); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestKeenableProvider_Name(t *testing.T) {
	p, err := NewKeenableProvider(types.WebSearchProviderParameters{})
	if err != nil {
		t.Fatalf("NewKeenableProvider (keyless): %v", err)
	}
	if p.Name() != "keenable" {
		t.Errorf("Name() = %q, want keenable", p.Name())
	}
}
