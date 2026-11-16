package web_search

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestDeepSeekProviderSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != deepseekSearchPath {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		var request deepseekSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "WeKnora" || request.Count != 2 {
			t.Fatalf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title": "First", "url": "https://example.com/1", "snippet": "Short",
				"summary": "Long summary", "content": "", "date": "2026-06-01T00:00:00Z"},
			{"title":"Second","url":"https://example.com/2","snippet":"Only snippet","summary":"","date":"not-a-date"},
			{"title":"Dropped","url":"","snippet":"no url"},
			{"title":"Third","url":"https://example.com/3","snippet":"capped"}
		]}`))
	}))
	defer server.Close()

	provider := &DeepSeekProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test"}
	results, err := provider.Search(context.Background(), types.WebSearchRequest{
		Query: " WeKnora ", MaxResults: 2, IncludeDate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Snippet != "Long summary" || results[0].Source != "deepseek" || results[0].PublishedAt == nil {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if want := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC); !results[0].PublishedAt.Equal(want) {
		t.Fatalf("date = %v, want %v", results[0].PublishedAt, want)
	}
	if results[1].Snippet != "Only snippet" || results[1].PublishedAt != nil {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

func TestDeepSeekProviderSearchTruncatesQuery(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request deepseekSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		got = request.Query
		_, _ = w.Write([]byte(`{"results":[{"title":"Hit","url":"https://example.com"}]}`))
	}))
	defer server.Close()

	query := strings.Repeat("查", maxDeepSeekQueryRunes+8)
	provider := &DeepSeekProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test"}
	_, err := provider.Search(context.Background(), types.WebSearchRequest{Query: query, MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(got) != maxDeepSeekQueryRunes {
		t.Fatalf("query runes = %d, want %d", utf8.RuneCountInString(got), maxDeepSeekQueryRunes)
	}
}

func TestDeepSeekProviderSearchHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key","type":"authentication_error"}}`))
	}))
	defer server.Close()
	provider := &DeepSeekProvider{client: server.Client(), baseURL: server.URL, apiKey: "bad"}
	_, err := provider.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
	if err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeepSeekProviderFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != deepseekFetchPath {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-env" {
			t.Fatalf("Authorization = %q", got)
		}
		var request deepseekFetchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.URL != "https://example.com/guide" {
			t.Fatalf("url = %s", request.URL)
		}
		_, _ = w.Write([]byte(`{"title":"Guide","content":"# Guide\n\nBody"}`))
	}))
	defer server.Close()

	provider := &DeepSeekProvider{
		client: server.Client(), baseURL: server.URL, apiKey: "sk-env", checkTarget: allowAnyTarget,
	}
	page, err := provider.Fetch(context.Background(), " https://example.com/guide ")
	if err != nil {
		t.Fatal(err)
	}
	if page.Content != "# Guide\n\nBody" || page.Title != "Guide" || page.Source != "deepseek" {
		t.Fatalf("page = %+v", page)
	}
}

func TestDeepSeekProviderFetchRejectsCredentialsInURL(t *testing.T) {
	provider := &DeepSeekProvider{apiKey: "sk-test"}
	_, err := provider.Fetch(context.Background(), "https://user:secret@example.com/private")
	var fetchErr *webfetch.FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != webfetch.ErrorInvalidURL || fetchErr.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func allowAnyTarget(string) error { return nil }

func TestDeepSeekProviderFetchRejectsPrivateTargets(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"content":"leaked"}`))
	}))
	defer server.Close()
	provider := &DeepSeekProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test"}
	for _, target := range []string{"http://127.0.0.1/admin", "http://10.0.0.8/wiki", "http://localhost:8080/"} {
		_, err := provider.Fetch(context.Background(), target)
		var fetchErr *webfetch.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != webfetch.ErrorSSRFRejected || fetchErr.Retryable {
			t.Fatalf("%s: error = %#v", target, err)
		}
	}
	if calls != 0 {
		t.Fatalf("private targets reached DeepSeek %d times", calls)
	}
}

func TestDeepSeekProviderFetchStatus(t *testing.T) {
	for _, tc := range []struct {
		status    int
		body      string
		code      webfetch.ErrorCode
		retryable bool
		message   string
	}{
		{
			http.StatusTooManyRequests,
			`{"error":{"message":"rate limited"}}`,
			webfetch.ErrorHTTP429,
			true,
			"rate limited",
		},
		{http.StatusUnauthorized, `{"error":"Invalid API key"}`, webfetch.ErrorProviderAuth, false, "Invalid API key"},
		{http.StatusForbidden, `{"error":{"message":"no access"}}`, webfetch.ErrorProviderAuth, false, "API key"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		provider := &DeepSeekProvider{
			client: server.Client(), baseURL: server.URL, apiKey: "sk-test", checkTarget: allowAnyTarget,
		}
		_, err := provider.Fetch(context.Background(), "https://example.com")
		server.Close()
		var fetchErr *webfetch.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != tc.code || fetchErr.Retryable != tc.retryable {
			t.Fatalf("status %d: error = %#v", tc.status, err)
		}
		if !strings.Contains(err.Error(), tc.message) {
			t.Fatalf("status %d: error = %v", tc.status, err)
		}
	}
}

func TestDeepSeekSearchToleratesStringErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[],"error":"quota exceeded"}`))
	}))
	defer server.Close()
	provider := &DeepSeekProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test"}
	_, err := provider.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
	if err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateDeepSeekParameters(t *testing.T) {
	if err := ValidateDeepSeekParameters(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	if err := ValidateDeepSeekParameters(types.WebSearchProviderParameters{APIKey: "sk-test"}); err != nil {
		t.Fatalf("saved key: %v", err)
	}
	if _, err := NewDeepSeekProvider(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	provider, err := NewDeepSeekProvider(types.WebSearchProviderParameters{APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if name := provider.(*DeepSeekProvider).Name(); name != "deepseek" {
		t.Fatalf("name = %s", name)
	}
}

func TestNewDeepSeekProviderUsesSavedKey(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"results":[{"title":"Hit","url":"https://example.com"}]}`))
	}))
	defer server.Close()

	created, err := NewDeepSeekProvider(types.WebSearchProviderParameters{APIKey: " sk-saved "})
	if err != nil {
		t.Fatal(err)
	}
	provider := created.(*DeepSeekProvider)
	provider.client = server.Client()
	provider.baseURL = server.URL
	_, err = provider.Search(context.Background(), types.WebSearchRequest{Query: "test", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bearer sk-saved" {
		t.Fatalf("Authorization = %q", got)
	}
}
