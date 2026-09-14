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

// The response fixture mirrors the live API's shape (captured 2026-09-14):
// Result.WebResults with Title/SiteName/Url/Snippet/Summary/Content and an
// ISO-8601 PublishTime carrying an explicit +08:00 offset.
func TestDoubaoProviderSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		var request doubaoSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "WeKnora" || request.SearchType != "web" || request.Count != 2 ||
			request.TimeRange != "2026-09-12..2026-09-14" || !request.Filter.NeedContent ||
			!request.Filter.NeedUrl || request.ContentFormats != "markdown" || request.QueryControl.QueryRewrite {
			t.Fatalf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"ResultCount":3,"WebResults":[
			{"Title":"First","SiteName":"财富号","Url":"https://example.com/1","Snippet":"Snippet","Summary":"Longer summary","Content":"# Full markdown","PublishTime":"2026-09-14T18:39:00+08:00"},
			{"Title":"Second","Url":"https://example.com/2","Snippet":"Fallback snippet","Content":"","PublishTime":"invalid"},
			{"Title":"Third","Url":"https://example.com/3","Snippet":"must be capped"}
		]}}`))
	}))
	defer server.Close()

	doubao := &DoubaoProvider{
		client: server.Client(), baseURL: server.URL, apiKey: "sk-test",
		timeRange: "2026-09-12..2026-09-14", needContent: true,
	}
	results, err := doubao.Search(context.Background(), " WeKnora ", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Snippet != "Longer summary" || results[1].Snippet != "Fallback snippet" {
		t.Fatalf("unexpected snippets: %q, %q", results[0].Snippet, results[1].Snippet)
	}
	if results[0].Content != "# Full markdown" || results[1].Content != "" {
		t.Fatalf("unexpected content: %q, %q", results[0].Content, results[1].Content)
	}
	if results[0].Source != "doubao" || results[0].Title != "First" || results[0].URL != "https://example.com/1" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if want := time.Date(2026, 9, 14, 10, 39, 0, 0, time.UTC); results[0].PublishedAt == nil || !results[0].PublishedAt.Equal(want) {
		t.Fatalf("date = %v, want %v", results[0].PublishedAt, want)
	}
	if results[1].PublishedAt != nil {
		t.Fatalf("invalid date should be ignored: %v", results[1].PublishedAt)
	}
}

func TestDoubaoProviderSearchDates(t *testing.T) {
	for _, tt := range []struct {
		name        string
		publishTime string
		includeDate bool
		want        string
	}{
		{"explicit offset", "2026-09-14T18:39:00+08:00", true, "2026-09-14T10:39:00Z"},
		{"date only tolerated", "2026-09-14", true, "2026-09-14T00:00:00Z"},
		{"invalid", "invalid", true, ""},
		{"date excluded", "2026-09-14T18:39:00+08:00", false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				var response doubaoSearchResponse
				response.Result.WebResults = []doubaoWebResult{{Title: "Result", Url: "https://example.com", PublishTime: tt.publishTime}}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(response); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			doubao := &DoubaoProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test"}
			results, err := doubao.Search(context.Background(), "test", 1, tt.includeDate)
			if err != nil || len(results) != 1 {
				t.Fatalf("results = %v, err = %v", results, err)
			}
			got := results[0].PublishedAt
			if tt.want == "" {
				if got != nil {
					t.Fatalf("date = %v, want nil", got)
				}
				return
			}
			if got == nil || got.UTC().Format(time.RFC3339Nano) != tt.want {
				t.Fatalf("date = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestValidateDoubaoParameters(t *testing.T) {
	if err := ValidateDoubaoParameters(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	for _, bad := range []string{
		"2026-09-12", "2026-9-12..2026-09-14", "2026-09-14..2026-09-12", "a..b",
	} {
		if err := ValidateDoubaoParameters(types.WebSearchProviderParameters{APIKey: "sk-test", ExtraConfig: map[string]string{"time_range": bad}}); err == nil {
			t.Fatalf("expected invalid time_range error for %q", bad)
		}
	}
	if err := ValidateDoubaoParameters(types.WebSearchProviderParameters{APIKey: "sk-test"}); err != nil {
		t.Fatalf("default parameters: %v", err)
	}
	if err := ValidateDoubaoParameters(types.WebSearchProviderParameters{APIKey: "sk-test", ExtraConfig: map[string]string{"time_range": "2026-09-12..2026-09-14"}}); err != nil {
		t.Fatalf("valid time_range: %v", err)
	}
}

func TestDoubaoProviderHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid Authentication"}`))
	}))
	defer server.Close()
	doubao := &DoubaoProvider{client: server.Client(), baseURL: server.URL, apiKey: "bad"}
	_, err := doubao.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid Authentication") {
		t.Fatalf("error = %v", err)
	}
}

func TestDoubaoProviderOmitsTimeRangeWhenUnset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request doubaoSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.TimeRange != "" {
			t.Fatalf("TimeRange = %q, want empty", request.TimeRange)
		}
		if !request.Filter.NeedContent {
			t.Fatal("NeedContent should default to true")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Result":{"ResultCount":0,"WebResults":[]}}`))
	}))
	defer server.Close()
	// Constructed directly (like the Bocha tests) to point at the test server;
	// needContent mirrors the constructor's default.
	doubao := &DoubaoProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test", needContent: true}
	results, err := doubao.Search(context.Background(), "test", 5, false)
	if err != nil || len(results) != 0 {
		t.Fatalf("results = %v, err = %v", results, err)
	}
}
