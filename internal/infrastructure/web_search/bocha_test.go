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

func TestBochaProviderSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		var request bochaSearchRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Query != "WeKnora" || request.Freshness != "oneWeek" || !request.Summary || request.Count != 2 {
			t.Fatalf("unexpected request: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"log_id":"x","msg":null,"data":{"webPages":{"value":[
			{"name":"First","url":"https://example.com/1","summary":"Summary","snippet":"Snippet","dateLastCrawled":"2026-08-09T08:18:30Z"},
			{"name":"Second","url":"https://example.com/2","snippet":"Fallback snippet","dateLastCrawled":"invalid"},
			{"name":"Third","url":"https://example.com/3","snippet":"must be capped"}
		]}}}`))
	}))
	defer server.Close()

	bocha := &BochaProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test", freshness: "oneWeek", summary: true}
	results, err := bocha.Search(context.Background(), " WeKnora ", 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Snippet != "Summary" || results[1].Snippet != "Fallback snippet" {
		t.Fatalf("unexpected snippets: %q, %q", results[0].Snippet, results[1].Snippet)
	}
	if results[0].Source != "bocha" || results[0].PublishedAt == nil {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if want := time.Date(2026, 8, 9, 8, 18, 30, 0, time.UTC); !results[0].PublishedAt.Equal(want) {
		t.Fatalf("date = %v, want %v", results[0].PublishedAt, want)
	}
	if results[1].PublishedAt != nil {
		t.Fatalf("invalid date should be ignored: %v", results[1].PublishedAt)
	}
}

func TestValidateBochaParameters(t *testing.T) {
	if err := ValidateBochaParameters(types.WebSearchProviderParameters{}); err == nil {
		t.Fatal("expected missing API key error")
	}
	if err := ValidateBochaParameters(types.WebSearchProviderParameters{APIKey: "sk-test", ExtraConfig: map[string]string{"freshness": "oneHour"}}); err == nil {
		t.Fatal("expected invalid freshness error")
	}
	if err := ValidateBochaParameters(types.WebSearchProviderParameters{APIKey: "sk-test"}); err != nil {
		t.Fatalf("default parameters: %v", err)
	}
}

func TestBochaProviderHTTPError(t *testing.T) {
	// Error body captured from the live API (2026-09-05): the message field is
	// "message" and the code is a JSON string.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"log_id":"4a995aed60e4088e","message":"Invalid API KEY","code":"401"}`))
	}))
	defer server.Close()
	bocha := &BochaProvider{client: server.Client(), baseURL: server.URL, apiKey: "bad", freshness: defaultBochaFreshness, summary: true}
	_, err := bocha.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "Invalid API KEY") {
		t.Fatalf("error = %v", err)
	}
}

func TestBochaProviderAPIErrorWithHTTPOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"429","message":"rate limited"}`))
	}))
	defer server.Close()
	bocha := &BochaProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test", freshness: defaultBochaFreshness, summary: true}
	_, err := bocha.Search(context.Background(), "test", 1, false)
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("error = %v", err)
	}
}

func TestBochaProviderSearchAcceptsStringAndNumericCode(t *testing.T) {
	for _, body := range []string{
		`{"code":"200","data":{"webPages":{"value":[{"name":"A","url":"https://example.com/a","snippet":"s1"}]}}}`,
		`{"code":200,"data":{"webPages":{"value":[{"name":"B","url":"https://example.com/b","snippet":"s2"}]}}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		bocha := &BochaProvider{client: server.Client(), baseURL: server.URL, apiKey: "sk-test", freshness: defaultBochaFreshness, summary: true}
		results, err := bocha.Search(context.Background(), "test", 1, false)
		if err != nil || len(results) != 1 {
			t.Fatalf("body %s: results = %v, err = %v", body, results, err)
		}
		server.Close()
	}
}
