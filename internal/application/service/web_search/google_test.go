package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
)

func TestNewGoogleProvider(t *testing.T) {
	testCases := []struct {
		name     string
		cfg      config.WebSearchProviderConfig
		expected error
	}{
		{
			name: "valid config",
			cfg: config.WebSearchProviderConfig{
				APIURL: "https://customsearch.googleapis.com/customsearch/v1?api_key=test&engine_id=test",
			},
			expected: nil,
		},
		{
			name: "missing engine id",
			cfg: config.WebSearchProviderConfig{
				APIURL: "https://customsearch.googleapis.com/customsearch/v1?api_key=test",
			},
			expected: fmt.Errorf("engine_id is empty"),
		},
		{
			name: "missing api key",
			cfg: config.WebSearchProviderConfig{
				APIURL: "https://customsearch.googleapis.com/customsearch/v1?engine_id=test",
			},
			expected: fmt.Errorf("api_key is empty"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGoogleProvider(tc.cfg)
			if tc.expected == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.expected)
				}
				if !strings.Contains(err.Error(), tc.expected.Error()) {
					t.Fatalf("expected error %v, got %v", tc.expected, err)
				}
			}
		})
	}
}

func TestGoogleProvider_Name(t *testing.T) {
	cfg := config.WebSearchProviderConfig{
		APIURL: "https://customsearch.googleapis.com/customsearch/v1?api_key=test&engine_id=test",
	}
	p, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}
	if p.Name() != "google" {
		t.Fatalf("expected provider name google, got %s", p.Name())
	}
}

func TestGoogleProvider_Search(t *testing.T) {
	mockResponse := map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"title":   "Example Search Result One",
				"link":    "https://example.com/page1",
				"snippet": "This is the first search result snippet describing the content.",
			},
			{
				"title":   "Example Search Result Two",
				"link":    "https://example.org/page2",
				"snippet": "This is the second search result snippet with more details.",
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/customsearch/v1" {
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}

		query := r.URL.Query().Get("q")
		if query != "weknora" {
			t.Fatalf("unexpected query: %s", query)
		}

		cx := r.URL.Query().Get("cx")
		if cx != "test-engine-id" {
			t.Fatalf("unexpected engine ID: %s", cx)
		}

		num := r.URL.Query().Get("num")
		if num != "5" {
			t.Fatalf("unexpected num parameter: %s", num)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		_ = enc.Encode(mockResponse)
	}))
	defer ts.Close()

	cfg := config.WebSearchProviderConfig{
		APIURL: fmt.Sprintf("%s/customsearch/v1?api_key=test-key&engine_id=test-engine-id", ts.URL),
	}
	prov, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	gp := prov.(*GoogleProvider)
	if gp == nil {
		t.Fatalf("failed to cast to GoogleProvider")
	}

	ctx := context.Background()
	results, err := prov.Search(ctx, "weknora", 5, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Title != "Example Search Result One" ||
		results[0].URL != "https://example.com/page1" ||
		results[0].Snippet != "This is the first search result snippet describing the content." ||
		results[0].Source != "google" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}

	if results[1].Title != "Example Search Result Two" ||
		results[1].URL != "https://example.org/page2" ||
		results[1].Snippet != "This is the second search result snippet with more details." ||
		results[1].Source != "google" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

func TestGoogleProvider_Search_EmptyQuery(t *testing.T) {
	cfg := config.WebSearchProviderConfig{
		APIURL: "https://customsearch.googleapis.com/customsearch/v1?api_key=test&engine_id=test",
	}
	prov, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	ctx := context.Background()
	results, err := prov.Search(ctx, "", 5, false)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
	if !strings.Contains(err.Error(), "query is empty") {
		t.Fatalf("expected 'query is empty' error, got: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil results for empty query, got: %v", results)
	}
}

func TestGoogleProvider_Search_NoResults(t *testing.T) {
	mockResponse := map[string]interface{}{
		"items": []map[string]interface{}{},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		_ = enc.Encode(mockResponse)
	}))
	defer ts.Close()

	cfg := config.WebSearchProviderConfig{
		APIURL: fmt.Sprintf("%s/customsearch/v1?api_key=test-key&engine_id=test-engine-id", ts.URL),
	}
	prov, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	ctx := context.Background()
	results, err := prov.Search(ctx, "nonexistent", 5, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestGoogleProvider_Search_ErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer ts.Close()

	cfg := config.WebSearchProviderConfig{
		APIURL: fmt.Sprintf("%s/customsearch/v1?api_key=test-key&engine_id=test-engine-id", ts.URL),
	}
	prov, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	ctx := context.Background()
	results, err := prov.Search(ctx, "test", 5, false)
	if err == nil {
		t.Fatal("expected error for server error response, got nil")
	}
	if results != nil {
		t.Fatalf("expected nil results for error response, got: %v", results)
	}
}

func TestGoogleProvider_Search_MaxResults(t *testing.T) {
	mockResponse := map[string]interface{}{
		"items": []map[string]interface{}{
			{"title": "Result 1", "link": "https://example.com/1", "snippet": "Snippet 1"},
			{"title": "Result 2", "link": "https://example.com/2", "snippet": "Snippet 2"},
			{"title": "Result 3", "link": "https://example.com/3", "snippet": "Snippet 3"},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		num := r.URL.Query().Get("num")
		if num != "2" {
			t.Fatalf("expected num=2, got %s", num)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		_ = enc.Encode(mockResponse)
	}))
	defer ts.Close()

	cfg := config.WebSearchProviderConfig{
		APIURL: fmt.Sprintf("%s/customsearch/v1?api_key=test-key&engine_id=test-engine-id", ts.URL),
	}
	prov, err := NewGoogleProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create Google provider: %v", err)
	}

	ctx := context.Background()
	results, err := prov.Search(ctx, "test", 2, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	if results[0].Title != "Result 1" || results[1].Title != "Result 2" || results[2].Title != "Result 3" {
		t.Fatalf("unexpected results order or content")
	}
}
