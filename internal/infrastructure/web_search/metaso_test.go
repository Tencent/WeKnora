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

// TestMetasoProviderSearch pins the regression this package fixed: Metaso puts
// scholar results under "scholars", so a provider that only reads "webpages"
// returns zero results for scope=scholar. The fixture deliberately mirrors a
// real scholar response (total/score/position/authors/date) rather than the
// "webpages" key the previous fixture used.
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
		_, _ = w.Write([]byte(`{"credits":3,"searchParameters":{"q":"WeKnora","scope":"scholar"},
			"total":3,"scholars":[
			{"title":"First","link":"https://example.com/1","score":"high","position":1,
			 "summary":"Summary","snippet":"Snippet","date":"2026-08-09","authors":["Author"]},
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

// TestMetasoProviderScopeArrays covers the "response key follows the scope"
// behaviour for every selectable scope.
//
// The document and scholar payloads are taken from real Metaso responses with
// domains and snippets sanitised; the remaining scopes use handwritten
// fixtures. Key names and JSON types are kept verbatim (total is a number,
// score a string, position/imageWidth/imageHeight numbers, authors an array,
// searchParameters an object) because those types are exactly what a naive
// struct declaration gets wrong.
func TestMetasoProviderScopeArrays(t *testing.T) {
	fixtures := map[string]struct {
		payload string
		want    int
	}{
		"webpage": {
			payload: `{"total":1,"webpages":[{"title":"W","link":"https://example.com/w",
				"score":"high","position":1,"summary":"网页摘要","date":"2026-08-09"}]}`,
			want: 1,
		},
		"document": {
			// The second link keeps the literal space a real document response
			// contained: the provider must pass links through untouched.
			payload: `{"credits":3,"searchParameters":{"q":"1","scope":"document"},"total":15,
				"documents":[
				{"title":"文档标题一","link":"https://example.com/doc/1","score":"high",
				 "snippet":"文档片段一","position":1,"authors":["作者甲"]},
				{"title":"文档标题二","link":"https://example.com/doc/2 with space","score":"medium",
				 "snippet":"文档片段二","position":2,"authors":["作者乙"]}
			]}`,
			want: 2,
		},
		"scholar": {
			// The second entry mirrors a real one that carried an empty snippet
			// and no authors: it must still be returned.
			payload: `{"credits":3,"searchParameters":{"q":"1","scope":"scholar"},"total":43,
				"scholars":[
				{"title":"学术标题一","link":"https://example.com/paper?q=1","score":"high",
				 "snippet":"学术片段一","position":1,"authors":["作者甲"],"date":"2012-10-15"},
				{"title":"学术标题二","link":"https://example.com/paper?q=2","score":"medium",
				 "snippet":"","position":2,"date":"2011-10-05"}
			]}`,
			want: 2,
		},
		"podcast": {
			payload: `{"total":1,"podcasts":[{"title":"P","link":"https://example.com/p",
				"score":"medium","position":1,"snippet":"播客片段","authors":["主播"],
				"date":"2022-10-13","duration":"299"}]}`,
			want: 1,
		},
		"video": {
			payload: `{"total":1,"videos":[{"title":"V","link":"https://example.com/v",
				"score":"medium","position":1,"snippet":"视频片段","authors":["UP"],
				"date":"2024-06-17","duration":"497","coverImage":"https://example.com/v.jpg"}]}`,
			want: 1,
		},
	}
	for scope, fixture := range fixtures {
		t.Run(scope, func(t *testing.T) {
			body := fixture.payload
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			metaso := &MetasoProvider{
				client: server.Client(), baseURL: server.URL,
				apiKey: "mk-test", scope: scope,
			}
			results, err := metaso.Search(context.Background(), "q", 5, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != fixture.want {
				t.Fatalf("scope=%s len(results) = %d, want %d", scope, len(results), fixture.want)
			}
			if results[0].Title == "" {
				t.Fatalf("scope=%s title is empty", scope)
			}
			if results[0].URL == "" {
				t.Fatalf("scope=%s URL is empty", scope)
			}
			if results[0].Snippet == "" {
				t.Fatalf("scope=%s snippet is empty", scope)
			}
		})
	}
}

// TestMetasoProviderImageScopeHasNoLink records a known limitation rather than
// a guarantee: image entries carry imageUrl but no link, and this provider
// reads only link, so image scope yields results with an empty URL and the
// agent-side URL check drops them. That is the same outcome main produced.
func TestMetasoProviderImageScopeHasNoLink(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"credits":3,"searchParameters":{"q":"1","scope":"image"},"total":31,
			"images":[{"title":"图片一","score":"low","position":1,
			 "imageUrl":"https://example.com/i.jpg","imageWidth":800,"imageHeight":600}]}`))
	}))
	defer server.Close()

	metaso := &MetasoProvider{
		client: server.Client(), baseURL: server.URL,
		apiKey: "mk-test", scope: "image",
	}
	results, err := metaso.Search(context.Background(), "q", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Title == "" {
		t.Fatalf("title is empty: %+v", results[0])
	}
	if results[0].URL != "" {
		t.Fatalf("URL = %q, want empty: image entries have no link", results[0].URL)
	}
}

// TestMetasoProviderToleratesNonStringURLFields guards the field list from the
// other direction: "url" and "imageUrl" must stay undeclared. A real response
// can carry them as objects, and declaring either as a string would fail the
// whole unmarshal and return zero results.
func TestMetasoProviderToleratesNonStringURLFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"webpages":[{"title":"W","link":"https://example.com/w",
			"snippet":"片段","url":{"href":"https://example.com/obj"},
			"imageUrl":{"href":"https://example.com/img"}}]}`))
	}))
	defer server.Close()

	metaso := &MetasoProvider{
		client: server.Client(), baseURL: server.URL,
		apiKey: "mk-test", scope: "webpage",
	}
	results, err := metaso.Search(context.Background(), "q", 5, false)
	if err != nil {
		t.Fatalf("non-string url fields must not fail the search: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/w" {
		t.Fatalf("results = %+v, want one result for https://example.com/w", results)
	}
}

// TestMetasoProviderToleratesUnmodelledFields pins the "declare only what is
// read" constraint.
//
// Regression background: the response once carried a Total string declaration
// while the live payload returned a JSON number, so json.Unmarshal failed and
// the whole search returned zero results. Unknown keys, and keys whose types
// contradict our assumptions, must not affect result parsing.
func TestMetasoProviderToleratesUnmodelledFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// total is a number, score is a number, position is a string: all
		// deliberately opposite to what "convenient" declarations would guess,
		// proving these keys are genuinely undeclared.
		_, _ = w.Write([]byte(`{"total":3,"pageSize":10,"webpages":[
			{"title":"W","link":"https://example.com/w","snippet":"片段",
			 "score":123,"position":"1","authors":null,"unknownNested":{"a":[1,2]}}]}`))
	}))
	defer server.Close()

	metaso := &MetasoProvider{
		client: server.Client(), baseURL: server.URL,
		apiKey: "mk-test", scope: "webpage",
	}
	results, err := metaso.Search(context.Background(), "q", 5, false)
	if err != nil {
		t.Fatalf("unmodelled fields must not fail the search: %v", err)
	}
	if len(results) != 1 || results[0].URL != "https://example.com/w" {
		t.Fatalf("results = %+v, want one result for https://example.com/w", results)
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
