package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

func TestLKEAPRerankerCallsRunRerankAndSortsScoreList(t *testing.T) {
	var gotAction string
	var gotVersion string
	var gotAuth string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction = r.Header.Get("X-TC-Action")
		gotVersion = r.Header.Get("X-TC-Version")
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"req-1","ScoreList":[-9.006162,2.8577538]}}`))
	}))
	defer server.Close()

	r, err := NewReranker(&RerankerConfig{
		Provider:  string(provider.ProviderLKEAP),
		BaseURL:   server.URL,
		ModelName: "lke-reranker-base",
		APIKey:    "secret-id",
		AppSecret: "secret-key",
	})
	if err != nil {
		t.Fatalf("NewReranker: %v", err)
	}

	results, err := r.Rerank(context.Background(), "知识引擎大模型", []string{"混元大模型", "腾讯知识引擎"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}

	if gotAction != "RunRerank" {
		t.Fatalf("X-TC-Action = %q, want RunRerank", gotAction)
	}
	if gotVersion != "2024-05-22" {
		t.Fatalf("X-TC-Version = %q, want 2024-05-22", gotVersion)
	}
	if gotAuth == "" {
		t.Fatal("Authorization header was not signed")
	}
	if gotBody["Query"] != "知识引擎大模型" {
		t.Fatalf("Query = %v", gotBody["Query"])
	}
	if gotBody["Model"] != "lke-reranker-base" {
		t.Fatalf("Model = %v", gotBody["Model"])
	}
	if !reflect.DeepEqual(gotBody["Docs"], []any{"混元大模型", "腾讯知识引擎"}) {
		t.Fatalf("Docs = %#v", gotBody["Docs"])
	}

	want := []RankResult{
		{Index: 1, Document: DocumentInfo{Text: "腾讯知识引擎"}, RelevanceScore: 2.8577538},
		{Index: 0, Document: DocumentInfo{Text: "混元大模型"}, RelevanceScore: -9.006162},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results = %#v, want %#v", results, want)
	}
}

func TestLKEAPRerankerRespectsRunRerankInputLimits(t *testing.T) {
	docs := make([]string, 61)
	for i := range docs {
		docs[i] = "abcdef"
	}
	docs[0] = strings.Repeat("你", 3000)

	query, limitedDocs, indexMap, err := prepareLKEAPInputs("query", docs)
	if err != nil {
		t.Fatalf("prepareLKEAPInputs: %v", err)
	}
	if query != "query" {
		t.Fatalf("query = %q", query)
	}
	if len(limitedDocs) != 1 {
		t.Fatalf("len(limitedDocs) = %d, want 1", len(limitedDocs))
	}
	if len([]rune(query))+totalRunes(limitedDocs) > 2000 {
		t.Fatalf("query + docs exceeded RunRerank limit")
	}
	if len([]rune(limitedDocs[0])) != 1995 {
		t.Fatalf("first doc len = %d, want 1995", len([]rune(limitedDocs[0])))
	}
	if !reflect.DeepEqual(indexMap, []int{0}) {
		t.Fatalf("indexMap = %#v", indexMap)
	}
}

func TestLKEAPRerankerReturnsOriginalDocumentAfterInputTruncation(t *testing.T) {
	longDoc := strings.Repeat("你", 3000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var gotBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		docs, ok := gotBody["Docs"].([]any)
		if !ok || len(docs) != 1 {
			t.Fatalf("Docs = %#v", gotBody["Docs"])
		}
		doc, ok := docs[0].(string)
		if !ok {
			t.Fatalf("Docs[0] = %#v", docs[0])
		}
		if len([]rune(doc)) != 1995 {
			t.Fatalf("sent doc len = %d, want 1995", len([]rune(doc)))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Response":{"RequestId":"req-1","ScoreList":[0.5]}}`))
	}))
	defer server.Close()

	r, err := NewLKEAPReranker(&RerankerConfig{
		BaseURL:   server.URL,
		ModelName: "lke-reranker-base",
		APIKey:    "secret-id",
		AppSecret: "secret-key",
	})
	if err != nil {
		t.Fatalf("NewLKEAPReranker: %v", err)
	}

	results, err := r.Rerank(context.Background(), "query", []string{longDoc})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Document.Text != longDoc {
		t.Fatal("result document was truncated")
	}
}
