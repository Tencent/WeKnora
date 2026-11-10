package web_search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestCustomSearchEndpoints(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "hello" {
			t.Errorf("missing query: %s", r.URL)
		}
		switch r.URL.Path {
		case "/bing":
			if r.Header.Get("Ocp-Apim-Subscription-Key") != "key" {
				t.Error("missing Bing key")
			}
			_, _ = w.Write([]byte(`{"webPages":{"value":[]}}`))
		case "/customsearch/v1":
			if r.URL.Query().Get("cx") != "engine" {
				t.Error("missing Google engine")
			}
			_, _ = w.Write([]byte(`{"items":[]}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	for _, tc := range []struct {
		name, base string
		create     func(types.WebSearchProviderParameters) error
	}{
		{"bing", srv.URL + "/bing", func(p types.WebSearchProviderParameters) error {
			s, e := NewBingProvider(p)
			if e != nil {
				return e
			}
			_, e = s.Search(context.Background(), types.WebSearchRequest{Query: "hello"})
			return e
		}},
		{"google", srv.URL, func(p types.WebSearchProviderParameters) error {
			s, e := NewGoogleProvider(p)
			if e != nil {
				return e
			}
			_, e = s.Search(context.Background(), types.WebSearchRequest{Query: "hello"})
			return e
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.create(types.WebSearchProviderParameters{APIKey: "key", EngineID: "engine", BaseURL: tc.base}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOptionalSearchBaseURL(t *testing.T) {
	utils.ResetSSRFWhitelistForTest()
	t.Setenv("SSRF_WHITELIST", "")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, tc := range []struct {
		url string
		bad bool
	}{{"", false}, {"ftp://example.com", true}, {"http://127.0.0.1", true}, {"https://example.com/?q=x", true}, {"https://example.com/#fragment", true}} {
		if err := ValidateOptionalSearchBaseURL(tc.url); (err != nil) != tc.bad {
			t.Errorf("%q: %v", tc.url, err)
		}
	}
}

func TestSearchEndpointsDefault(t *testing.T) {
	bing, err := NewBingProvider(types.WebSearchProviderParameters{APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	if got := bing.(*BingProvider).baseURL; got != defaultBingSearchURL {
		t.Fatalf("Bing default: %q", got)
	}
	google, err := NewGoogleProvider(types.WebSearchProviderParameters{APIKey: "key", EngineID: "engine"})
	if err != nil {
		t.Fatal(err)
	}
	if got := google.(*GoogleProvider).srv.BasePath; got != "https://customsearch.googleapis.com/" {
		t.Fatalf("Google default: %q", got)
	}
}
