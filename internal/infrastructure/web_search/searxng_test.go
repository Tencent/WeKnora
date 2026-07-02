package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestValidateSearxngBaseURL(t *testing.T) {
	// utils.ValidateURLForSSRF caches the parsed SSRF_WHITELIST via
	// sync.Once on first call. An alphabetically-earlier test in this
	// binary (TestValidateProxyURL) triggers ValidateURLForSSRF with an
	// empty whitelist and caches an empty config, so the later setenv
	// here would otherwise be ignored. Reset the singleton on both
	// entry and exit to keep the env / singleton in sync.
	utils.ResetSSRFWhitelistForTest()
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer func() {
		os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	}()

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
	// See TestValidateSearxngBaseURL comment — reset the SSRF whitelist
	// singleton so the setenv below is actually observed by the cached
	// ssrfWhitelistConfig in internal/utils.
	utils.ResetSSRFWhitelistForTest()
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer func() {
		os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	}()

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

func TestSearxngProvider_Search_EmptyWithUnresponsiveEngines(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	defer func() {
		os.Unsetenv("SSRF_WHITELIST")
		utils.ResetSSRFWhitelistForTest()
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results":              []any{},
			"unresponsive_engines": [][]string{{"google", "timeout"}},
		})
	}))
	defer srv.Close()

	provider, err := NewSearxngProvider(types.WebSearchProviderParameters{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("NewSearxngProvider: %v", err)
	}
	sp := provider.(*SearxngProvider)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := sp.Search(ctx, "test", 1, false)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
	if got := sp.EmptyResultDiagnostics(); !strings.Contains(got, "google (timeout)") {
		t.Fatalf("EmptyResultDiagnostics() = %q", got)
	}
}
