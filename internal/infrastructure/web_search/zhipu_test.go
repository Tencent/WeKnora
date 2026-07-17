package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateZhipuParameters(t *testing.T) {
	tests := []struct {
		name    string
		params  types.WebSearchProviderParameters
		wantErr bool
	}{
		{name: "defaults", params: types.WebSearchProviderParameters{APIKey: "key"}},
		{
			name: "custom options",
			params: types.WebSearchProviderParameters{
				APIKey: "key",
				ExtraConfig: map[string]string{
					"search_engine": "search_pro_sogou",
					"content_size":  "high",
				},
			},
		},
		{name: "missing key", params: types.WebSearchProviderParameters{}, wantErr: true},
		{
			name: "invalid engine",
			params: types.WebSearchProviderParameters{
				APIKey:      "key",
				ExtraConfig: map[string]string{"search_engine": "unknown"},
			},
			wantErr: true,
		},
		{
			name: "invalid content size",
			params: types.WebSearchProviderParameters{
				APIKey:      "key",
				ExtraConfig: map[string]string{"content_size": "large"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateZhipuParameters(tt.params)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateZhipuParameters() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestZhipuProviderSearch(t *testing.T) {
	query := strings.Repeat("智", maxZhipuQueryRunes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var request zhipuSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if got := utf8.RuneCountInString(request.SearchQuery); got != maxZhipuQueryRunes {
			t.Errorf("query rune count = %d, want %d", got, maxZhipuQueryRunes)
		}
		if request.SearchEngine != "search_pro" {
			t.Errorf("search_engine = %q, want search_pro", request.SearchEngine)
		}
		if request.ContentSize != "high" {
			t.Errorf("content_size = %q, want high", request.ContentSize)
		}
		if request.SearchIntent {
			t.Error("search_intent = true, want false")
		}
		if request.Count != maxZhipuResults {
			t.Errorf("count = %d, want %d", request.Count, maxZhipuResults)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":         "search-id",
			"request_id": "request-id",
			"search_result": []map[string]any{
				{
					"title":        "Result 1",
					"link":         "https://example.com/1",
					"content":      "Summary 1",
					"publish_date": "2026-07-16",
				},
				{
					"title":   "Result 2",
					"link":    "https://example.com/2",
					"content": "Summary 2",
				},
			},
		})
	}))
	defer srv.Close()

	provider := &ZhipuProvider{
		client:       srv.Client(),
		baseURL:      srv.URL,
		apiKey:       "test-key",
		searchEngine: "search_pro",
		contentSize:  "high",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := provider.Search(ctx, query, maxZhipuResults+1, true)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Snippet != "Summary 1" || results[0].Content != "" {
		t.Errorf("first result content mapping = %+v", results[0])
	}
	if results[0].Source != "zhipu" {
		t.Errorf("source = %q, want zhipu", results[0].Source)
	}
	if results[0].PublishedAt == nil || results[0].PublishedAt.Format("2006-01-02") != "2026-07-16" {
		t.Errorf("published_at = %v, want 2026-07-16", results[0].PublishedAt)
	}
}

func TestZhipuProviderSearchDefaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request zhipuSearchRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.SearchEngine != defaultZhipuSearchEngine {
			t.Errorf("search_engine = %q, want %q", request.SearchEngine, defaultZhipuSearchEngine)
		}
		if request.ContentSize != defaultZhipuContentSize {
			t.Errorf("content_size = %q, want %q", request.ContentSize, defaultZhipuContentSize)
		}
		if request.Count != defaultZhipuResults {
			t.Errorf("count = %d, want %d", request.Count, defaultZhipuResults)
		}
		_, _ = w.Write([]byte(`{"search_result":[]}`))
	}))
	defer srv.Close()

	provider := &ZhipuProvider{
		client:       srv.Client(),
		baseURL:      srv.URL,
		apiKey:       "test-key",
		searchEngine: defaultZhipuSearchEngine,
		contentSize:  defaultZhipuContentSize,
	}
	if _, err := provider.Search(context.Background(), "test", 0, false); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
}

func TestZhipuProviderSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"1302","message":"rate limited"}}`))
	}))
	defer srv.Close()

	provider := &ZhipuProvider{
		client:       srv.Client(),
		baseURL:      srv.URL,
		apiKey:       "test-key",
		searchEngine: defaultZhipuSearchEngine,
		contentSize:  defaultZhipuContentSize,
	}
	_, err := provider.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("Search() error = %v, want rate limited error", err)
	}
}

func TestParseZhipuDate(t *testing.T) {
	for _, value := range []string{
		"2026-07-16",
		"2026-07-16 12:30",
		"2026-07-16 12:30:45",
		"2026-07-16T12:30:45Z",
	} {
		if _, ok := parseZhipuDate(value); !ok {
			t.Errorf("parseZhipuDate(%q) failed", value)
		}
	}
	if _, ok := parseZhipuDate("not-a-date"); ok {
		t.Error("parseZhipuDate(not-a-date) unexpectedly succeeded")
	}
}
