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

func TestExaProviderSearchMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "exa-test" {
			t.Fatalf("x-api-key = %q", got)
		}
		var request exaSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "hello" || request.NumResults != 2 ||
			!request.Contents.Highlights || !request.Contents.Text {
			t.Fatalf("request body = %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(exaSearchResponse{Results: []exaResult{
			{
				Title:         "One",
				URL:           "https://example.com/1",
				Highlights:    []string{"first", "second"},
				Text:          "body",
				PublishedDate: "2026-05-01T00:00:00Z",
			},
			{Title: "Two", URL: "https://example.com/2"},
			{Title: "Three", URL: "https://example.com/3"},
		}})
	}))
	defer srv.Close()

	p := &ExaProvider{client: srv.Client(), baseURL: srv.URL + "/search", apiKey: "exa-test", includeText: true}
	results, err := p.Search(context.Background(), "hello", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Snippet != "first\nsecond" ||
		results[0].Content != "body" || results[0].Source != "exa" {
		t.Fatalf("unexpected results: %+v", results)
	}
	if results[0].PublishedAt == nil || !results[0].PublishedAt.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected date: %v", results[0].PublishedAt)
	}
	if results[1].Snippet != "" {
		t.Fatalf("unexpected empty-result fallback: %+v", results[1])
	}
}

func TestExaProviderSupportsConnectionTestProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "exa-test" {
			t.Fatalf("x-api-key = %q", got)
		}
		var request exaSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "test" || request.NumResults != 1 {
			t.Fatalf("connection test request = %+v", request)
		}
		if !request.Contents.Highlights || request.Contents.Text {
			t.Fatalf("connection test should request highlights without page text: %+v", request.Contents)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(exaSearchResponse{Results: []exaResult{
			{Title: "Exa", URL: "https://exa.ai/"},
		}})
	}))
	defer srv.Close()

	p := &ExaProvider{
		client:  srv.Client(),
		baseURL: srv.URL + "/search",
		apiKey:  "exa-test",
	}
	results, err := p.Search(context.Background(), "test", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
}

func TestExaProviderValidationAndStatus(t *testing.T) {
	if _, err := NewExaProvider(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()
	p := &ExaProvider{client: srv.Client(), baseURL: srv.URL, apiKey: "key"}
	if _, err := p.Search(context.Background(), "q", 1, false); err == nil {
		t.Fatal("expected status error")
	}
	if _, err := p.Search(context.Background(), " ", 1, false); err == nil {
		t.Fatal("expected empty query error")
	}
}
