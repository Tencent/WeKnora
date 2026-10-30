package web_search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type yuanbaoTransport func(*http.Request) (*http.Response, error)

func (f yuanbaoTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestYuanbaoSearch(t *testing.T) {
	searcher, err := NewYuanbaoProvider(types.WebSearchProviderParameters{SecretID: "id", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	provider := searcher.(*YuanbaoProvider)
	provider.client.WithHttpTransport(yuanbaoTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != yuanbaoEndpoint || r.Method != http.MethodPost || r.Header.Get("X-TC-Action") != "SearchPro" {
			t.Errorf("unexpected request: %s %s action=%s", r.Method, r.URL, r.Header.Get("X-TC-Action"))
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "TC3-HMAC-SHA256 ") {
			t.Errorf("missing TC3 signature")
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["Query"] != "WeKnora" || len(body) != 1 {
			t.Errorf("unexpected body: %v, %v", body, err)
		}
		payload := `{"Response":{"Pages":["{\"title\":\"First\",\"url\":\"https://example.com/1\",\"passage\":\"Summary\",\"content\":\"Full\",\"date\":\"2025/02/26 09:37:00\"}","bad json","{\"title\":\"Second\",\"url\":\"https://example.com/2\"}"],"RequestId":"test"}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	}))
	results, err := provider.Search(context.Background(), types.WebSearchRequest{Query: " WeKnora ", MaxResults: 1, IncludeDate: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "First" || results[0].Snippet != "Summary" || results[0].Content != "Full" || results[0].Age != "2025/02/26 09:37:00" {
		t.Fatalf("unexpected results: %+v", results)
	}
}

func TestYuanbaoValidation(t *testing.T) {
	for _, params := range []types.WebSearchProviderParameters{{}, {SecretID: "id"}, {APIKey: "key"}} {
		if err := ValidateYuanbaoParameters(params); err == nil {
			t.Fatalf("accepted incomplete credentials: %+v", params)
		}
	}
}
