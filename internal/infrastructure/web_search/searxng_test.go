package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateSearxngBaseURL(t *testing.T) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer os.Unsetenv("SSRF_WHITELIST")

	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{name: "empty", url: "", wantErr: true},
		{name: "no scheme", url: "searxng:8080", wantErr: true},
		{name: "bad scheme", url: "ftp://searxng:8080", wantErr: true},
		{name: "with query", url: "http://127.0.0.1:8080/?x=1", wantErr: true},
		{name: "with fragment", url: "http://127.0.0.1:8080/#frag", wantErr: true},
		{name: "loopback ok via whitelist", url: "http://127.0.0.1:8888", wantErr: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSearxngBaseURL(tc.url)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateSearxngBaseURL(%q) err=%v wantErr=%v", tc.url, err, tc.wantErr)
			}
		})
	}
}

func TestParseSearxngDate(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"", false},
		{"2024-05-01", true},
		{"2024-05-01T12:30:45", true},
		{"2024-05-01T12:30:45Z", true},
		{"2024-05-01T12:30:45.123456789Z", true},
		{"2024-05-01 12:30:45", true},
		{"Wed, 01 May 2024 12:30:45 GMT", true},
		{"not-a-date", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			_, ok := parseSearxngDate(tc.in)
			if ok != tc.ok {
				t.Fatalf("parseSearxngDate(%q) ok=%v want=%v", tc.in, ok, tc.ok)
			}
		})
	}
}

func TestSearxngProvider_Search(t *testing.T) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer os.Unsetenv("SSRF_WHITELIST")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("expected format=json, got %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "hello" {
			t.Errorf("expected q=hello, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"title": "T1", "url": "https://e/1", "content": "c1", "publishedDate": "2024-05-01"},
				{"title": "", "url": "https://e/skip"},
				{"title": "T2", "url": "https://e/2", "content": "c2"},
			},
		})
	}))
	defer srv.Close()

	provider, err := NewSearxngProvider(types.WebSearchProviderParameters{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewSearxngProvider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := provider.Search(ctx, "hello", 5, true)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (one skipped), got %d", len(results))
	}
	if results[0].PublishedAt == nil {
		t.Fatalf("expected first result PublishedAt to be set")
	}
	if got := results[0].Source; got != "searxng" {
		t.Fatalf("unexpected source: %q", got)
	}
}
