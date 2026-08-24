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

func TestMetasoProviderSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer mk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		var request metasoSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "WeKnora" || request.Scope != "scholar" || request.Size != 2 {
			t.Fatalf("unexpected request: %+v", request)
		}
		if !request.IncludeSummary || request.IncludeRawContent || !request.ConciseSnippet {
			t.Fatalf("unexpected content options: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webpages":[
			{"title":"First","link":"https://example.com/1","summary":"Summary","snippet":"Snippet","date":"2026-08-09"},
			{"title":"Second","link":"https://example.com/2","snippet":"Fallback snippet","date":"invalid"},
			{"title":"Third","link":"https://example.com/3","snippet":"must be capped"}
		]}`))
	}))
	defer server.Close()

	metaso := &MetasoProvider{client: server.Client(), baseURL: server.URL, apiKey: "mk-test", scope: "scholar"}
	results, err := metaso.Search(context.Background(), " WeKnora ", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Snippet != "Summary" || results[1].Snippet != "Fallback snippet" {
		t.Fatalf("unexpected snippets: %q, %q", results[0].Snippet, results[1].Snippet)
	}
	if results[0].Source != "metaso" || results[0].PublishedAt == nil {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if want := time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC); !results[0].PublishedAt.Equal(want) {
		t.Fatalf("date = %v, want %v", results[0].PublishedAt, want)
	}
	if results[1].PublishedAt != nil {
		t.Fatalf("invalid date should be ignored: %v", results[1].PublishedAt)
	}
}

func TestValidateMetasoParameters(t *testing.T) {
	if err := ValidateMetasoParameters(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	if err := ValidateMetasoParameters(types.WebSearchProviderParameters{APIKey: "mk-test", ExtraConfig: map[string]string{"scope": "unknown"}}); err == nil {
		t.Fatal("expected invalid scope error")
	}
	if err := ValidateMetasoParameters(types.WebSearchProviderParameters{APIKey: "mk-test"}); err != nil {
		t.Fatalf("default parameters: %v", err)
	}
}

func TestMetasoProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid API key"}`))
	}))
	defer server.Close()
	metaso := &MetasoProvider{client: server.Client(), baseURL: server.URL, apiKey: "bad", scope: defaultMetasoScope}
	_, err := metaso.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Fatalf("error = %v", err)
	}
}
