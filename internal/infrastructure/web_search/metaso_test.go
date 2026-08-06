package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateMetasoParameters(t *testing.T) {
	tests := []struct {
		name    string
		params  types.WebSearchProviderParameters
		wantErr bool
	}{
		{name: "defaults", params: types.WebSearchProviderParameters{APIKey: "key"}},
		{
			name: "custom scope",
			params: types.WebSearchProviderParameters{
				APIKey: "key",
				ExtraConfig: map[string]string{
					"scope":          "scholar",
					"conciseSnippet": "false",
					"includeSummary": "false",
				},
			},
		},
		{name: "missing key", params: types.WebSearchProviderParameters{}, wantErr: true},
		{
			name: "invalid scope",
			params: types.WebSearchProviderParameters{
				APIKey:      "key",
				ExtraConfig: map[string]string{"scope": "unknown"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMetasoParameters(tt.params)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateMetasoParameters() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMetasoOptions(t *testing.T) {
	// Defaults when extra_config is empty.
	scope, concise, summary := metasoOptions(nil)
	if scope != defaultMetasoScope || !concise || !summary {
		t.Fatalf("defaults: scope=%q concise=%v summary=%v", scope, concise, summary)
	}
	// Honor explicit overrides, including "false".
	scope, concise, summary = metasoOptions(map[string]string{
		"scope":          "podcast",
		"conciseSnippet": "false",
		"includeSummary": "false",
	})
	if scope != "podcast" || concise || summary {
		t.Fatalf("overrides: scope=%q concise=%v summary=%v", scope, concise, summary)
	}
}

func TestMetasoProviderSearchWebpage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q, want application/json", got)
		}
		var request metasoSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request.Query != "大模型推理优化" {
			t.Errorf("query = %q", request.Query)
		}
		if request.Scope != "webpage" {
			t.Errorf("scope = %q, want webpage", request.Scope)
		}
		if request.Size != "3" {
			t.Errorf("size = %q, want 3", request.Size)
		}
		if !request.ConciseSnippet || !request.IncludeSummary {
			t.Errorf("conciseSnippet=%v includeSummary=%v, want both true", request.ConciseSnippet, request.IncludeSummary)
		}

		// webpage scope returns "webpages" array; with includeSummary the
		// items carry "summary" instead of "snippet".
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"webpages": []map[string]any{
				{
					"title":    "大模型推理优化技术",
					"link":     "https://example.com/1",
					"summary":  "推理过程、性能指标、模型压缩和Transformer结构优化。",
					"date":     "2024年11月15日",
					"position": 1,
				},
			},
		})
	}))
	defer srv.Close()

	provider := &MetasoProvider{
		client:         srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test-key",
		scope:          "webpage",
		conciseSnippet: true,
		includeSummary: true,
	}
	results, err := provider.Search(context.Background(), "大模型推理优化", 3, true)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Snippet == "" {
		t.Errorf("snippet empty; expected summary fallback to populate it")
	}
	if results[0].Source != "metaso" {
		t.Errorf("source = %q, want metaso", results[0].Source)
	}
	if results[0].PublishedAt == nil {
		t.Errorf("published_at not parsed from 2024年11月15日")
	}
}

func TestMetasoProviderSearchScholar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// scholar scope returns "scholars" array with "snippet".
		_ = json.NewEncoder(w).Encode(map[string]any{
			"scholars": []map[string]any{
				{
					"title":   "Attention is All you Need",
					"link":    "https://arxiv.org/abs/1706.03762",
					"snippet": "The dominant sequence transduction models are based on complex recurrent networks.",
					"date":    "2017-06-12",
				},
			},
		})
	}))
	defer srv.Close()

	provider := &MetasoProvider{
		client:         srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test-key",
		scope:          "scholar",
		conciseSnippet: true,
		includeSummary: true,
	}
	results, err := provider.Search(context.Background(), "transformer", 5, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if !strings.Contains(results[0].Snippet, "transduction") {
		t.Errorf("snippet = %q, want transduction", results[0].Snippet)
	}
	if results[0].PublishedAt != nil {
		t.Errorf("published_at should be nil when includeDate=false")
	}
}

func TestMetasoProviderSearchImage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// image scope returns "images" array with "imageUrl" instead of "link".
		_ = json.NewEncoder(w).Encode(map[string]any{
			"images": []map[string]any{
				{
					"title":    "Scottish Straight Kittens",
					"imageUrl": "https://example.com/cat.jpg",
					"position": 1,
				},
			},
		})
	}))
	defer srv.Close()

	provider := &MetasoProvider{
		client:         srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test-key",
		scope:          "image",
		conciseSnippet: true,
		includeSummary: true,
	}
	results, err := provider.Search(context.Background(), "猫", 2, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].URL != "https://example.com/cat.jpg" {
		t.Errorf("url = %q, want imageUrl fallback", results[0].URL)
	}
}

func TestMetasoProviderSearchEmpty(t *testing.T) {
	// Response missing the scope-specific array key should yield zero
	// results rather than an error.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"credits":0,"searchParameters":{"q":"x"}}`))
	}))
	defer srv.Close()

	provider := &MetasoProvider{
		client:         srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test-key",
		scope:          "webpage",
		conciseSnippet: true,
		includeSummary: true,
	}
	results, err := provider.Search(context.Background(), "test", 3, false)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len(results) = %d, want 0", len(results))
	}
}

func TestMetasoProviderSearchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	provider := &MetasoProvider{
		client:         srv.Client(),
		baseURL:        srv.URL,
		apiKey:         "test-key",
		scope:          "webpage",
		conciseSnippet: true,
		includeSummary: true,
	}
	_, err := provider.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("Search() error = %v, want 429 status", err)
	}
}

func TestParseMetasoDate(t *testing.T) {
	for _, value := range []string{
		"2017-06-12",
		"2024年11月15日",
		"2024年1月2日",
	} {
		if _, ok := parseMetasoDate(value); !ok {
			t.Errorf("parseMetasoDate(%q) failed", value)
		}
	}
	if _, ok := parseMetasoDate("not-a-date"); ok {
		t.Error("parseMetasoDate(not-a-date) unexpectedly succeeded")
	}
}

// TestNormalizeMetasoQuery guards against panics on long multibyte input.
func TestNormalizeMetasoQuery(t *testing.T) {
	long := strings.Repeat("智", maxMetasoQueryRunes+5)
	out := normalizeMetasoQuery(long)
	if len([]rune(out)) != maxMetasoQueryRunes {
		t.Fatalf("normalized rune count = %d, want %d", len([]rune(out)), maxMetasoQueryRunes)
	}
	if normalizeMetasoQuery("  hi  ") != "hi" {
		t.Error("trimming failed")
	}
	if normalizeMetasoQuery("") != "" {
		t.Error("empty should stay empty")
	}
}

// ensure time import is referenced even when the build tags strip tests.
var _ = time.Time{}

// ensure context import is referenced in case other tests are stripped.
var _ = context.Background
