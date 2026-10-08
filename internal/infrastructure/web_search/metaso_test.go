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
		// scope=scholar 时 Metaso 把结果放在 scholars 数组里（不是 webpages）
		_, _ = w.Write([]byte(`{"total":3,"scholars":[
			{"title":"First","link":"https://example.com/1","score":"high","position":1,
			 "summary":"Summary","snippet":"Snippet","date":"2026-08-09","authors":["张三"]},
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

// TestMetasoProviderScopeArrays 覆盖"响应键名随 scope 变化"这一关键行为。
//
// fixture 刻意保留各 scope 真实响应里的全部键（score/position/authors/duration/
// coverImage/imageWidth/imageHeight/total…），其中大部分结构体并未建模——这正是要
// 验证的：多出来的键必须被忽略，而不是让整次搜索失败。
func TestMetasoProviderScopeArrays(t *testing.T) {
	fixtures := map[string]string{
		"webpage": `{"total":1,"webpages":[{"title":"W","link":"https://example.com/w",
			"score":"high","position":1,"summary":"网页摘要","date":"2023年04月03日"}]}`,
		"document": `{"total":1,"documents":[{"title":"D","link":"https://example.com/d",
			"score":"high","position":1,"snippet":"文档片段"}]}`,
		"scholar": `{"total":1,"scholars":[{"title":"S","link":"https://example.com/s",
			"score":"high","position":1,"snippet":"学术片段","authors":["罗庚兴"],"date":"2011-10-05"}]}`,
		"podcast": `{"total":1,"podcasts":[{"title":"P","link":"https://example.com/p",
			"score":"medium","position":1,"snippet":"播客片段","authors":["主播"],"date":"2022-10-13",
			"duration":"299"}]}`,
		"video": `{"total":1,"videos":[{"title":"V","link":"https://example.com/v",
			"score":"medium","position":1,"snippet":"视频片段","authors":["UP"],"date":"2024-06-17",
			"duration":"497","coverImage":"https://example.com/v.jpg"}]}`,
		"image": `{"total":1,"images":[{"title":"I","score":"low","position":1,
			"imageUrl":"https://example.com/i.jpg","imageWidth":800,"imageHeight":600}]}`,
	}
	for scope, payload := range fixtures {
		t.Run(scope, func(t *testing.T) {
			body := payload
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
			if len(results) != 1 {
				t.Fatalf("scope=%s len(results) = %d, want 1", scope, len(results))
			}
			if results[0].Title == "" {
				t.Fatalf("scope=%s title is empty", scope)
			}
			if results[0].URL == "" {
				t.Fatalf("scope=%s URL is empty", scope)
			}
			// image scope 的条目按设计只有 imageUrl，没有 snippet/summary，
			// 不该要求它非空（URL 非空已在上一步断言）。
			if scope != "image" && results[0].Snippet == "" {
				t.Fatalf("scope=%s snippet is empty", scope)
			}
		})
	}
}

// TestMetasoProviderImageScopeURL image scope 没有 link 字段，只能用 imageUrl，
// 否则会被 agent 侧的 URL 校验全部丢弃，表现为"有结果却返回 0 条"。
func TestMetasoProviderImageScopeURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"images":[{"title":"I","score":"low","position":1,
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
	if results[0].URL != "https://example.com/i.jpg" {
		t.Fatalf("URL = %q, want imageUrl fallback", results[0].URL)
	}
}

// TestMetasoProviderUnknownScopeFallback 未知 scope 时回退到第一个非空数组，尽量不丢结果。
func TestMetasoProviderUnknownScopeFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scholars":[{"title":"S","link":"https://example.com/s",
			"snippet":"x"}]}`))
	}))
	defer server.Close()

	metaso := &MetasoProvider{
		client: server.Client(), baseURL: server.URL,
		apiKey: "mk-test", scope: "not-a-real-scope",
	}
	results, err := metaso.Search(context.Background(), "q", 5, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1 (fallback)", len(results))
	}
}

// TestMetasoProviderToleratesUnmodelledFields 锁死"只建模真正会被读取的字段"这一约束。
//
// 回归背景：曾为记一行日志给响应加了 Total string，而线上返回的是 JSON 数字，
// json.Unmarshal 直接报错，整次搜索返回 0 条。未知键、以及类型与我们的假设不符的
// 键，都不该影响结果读取。
func TestMetasoProviderToleratesUnmodelledFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// total 是数字、score 是数字、position 是字符串：全部刻意与"顺手建模"的
		// 猜测相反，用来证明这些键确实没有被声明。
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

func TestMetasoParseDate(t *testing.T) {
	cases := map[string]bool{
		"2026-08-09":           true,
		"2026-08-09T10:11:12Z": true,
		"2023年04月03日":          true, // webpage scope 的真实格式
		"2011-10-05":           true,
		"invalid":              false,
		"":                     false,
	}
	for input, wantOK := range cases {
		if _, ok := parseMetasoDate(input); ok != wantOK {
			t.Fatalf("parseMetasoDate(%q) ok = %v, want %v", input, ok, wantOK)
		}
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
