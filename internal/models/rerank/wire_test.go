package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorded is one request as the upstream saw it.
type recorded struct {
	path   string
	header http.Header
	body   map[string]any
}

// upstream is a stand-in for every rerank vendor at once. It answers in
// whichever shape the request belongs to, scoring each document by its
// length so that the expected ranking is known: longer is more relevant.
// Probability vendors get length/100, logit vendors length-3, which puts the
// shortest documents below zero the way a real NIM reply does.
type upstream struct {
	mu       sync.Mutex
	requests []recorded
	url      string
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	withRerankSSRFWhitelist(t, "127.0.0.1")
	u := &upstream{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		u.mu.Lock()
		u.requests = append(u.requests, recorded{r.URL.Path, r.Header.Clone(), body})
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer(r, body)))
	}))
	t.Cleanup(server.Close)
	u.url = server.URL

	// LKEAP's SDK always calls the public API host; point it here.
	savedEndpoint, savedScheme := lkeapEndpoint, lkeapScheme
	lkeapEndpoint, lkeapScheme = strings.TrimPrefix(server.URL, "http://"), "HTTP"
	t.Cleanup(func() { lkeapEndpoint, lkeapScheme = savedEndpoint, savedScheme })
	return u
}

// texts pulls the documents out of whichever shape carried them.
func texts(v any) []string {
	var out []string
	for _, item := range v.([]any) {
		switch d := item.(type) {
		case string:
			out = append(out, d)
		case map[string]any: // NIM passages, Volcengine datas
			if text, ok := d["text"].(string); ok {
				out = append(out, text)
			} else {
				out = append(out, d["content"].(string))
			}
		}
	}
	return out
}

func probability(doc string) float64 { return float64(utf8.RuneCountInString(doc)) / 100 }
func logit(doc string) float64       { return float64(utf8.RuneCountInString(doc)) - 3 }

// answer replies in the protocol the request belongs to. The indexed shapes
// come back in reverse order so that placement by index is exercised.
func answer(r *http.Request, body map[string]any) string {
	var parts []string
	switch {
	case r.Header.Get("X-TC-Action") == "RunRerank": // LKEAP: scores in input order
		for _, doc := range texts(body["Docs"]) {
			parts = append(parts, fmt.Sprint(probability(doc)))
		}
		return `{"Response":{"ScoreList":[` + strings.Join(parts, ",") + `],"RequestId":"r"}}`
	case strings.HasSuffix(r.URL.Path, "/api/knowledge/service/rerank"): // Volcengine
		for _, doc := range texts(body["datas"]) {
			parts = append(parts, fmt.Sprint(probability(doc)))
		}
		return `{"code":0,"message":"success","data":{"scores":[` + strings.Join(parts, ",") + `]}}`
	case strings.HasSuffix(r.URL.Path, "/reranking"): // NIM
		docs := texts(body["passages"])
		for i := len(docs) - 1; i >= 0; i-- {
			parts = append(parts, fmt.Sprintf(`{"index":%d,"logit":%v}`, i, logit(docs[i])))
		}
		return `{"rankings":[` + strings.Join(parts, ",") + `]}`
	case strings.Contains(r.URL.Path, "/text-rerank"): // DashScope
		docs := texts(body["input"].(map[string]any)["documents"])
		for i := len(docs) - 1; i >= 0; i-- {
			parts = append(parts, fmt.Sprintf(`{"index":%d,"relevance_score":%v}`, i, probability(docs[i])))
		}
		return `{"output":{"results":[` + strings.Join(parts, ",") + `]}}`
	default: // Cohere
		docs := texts(body["documents"])
		score := probability
		if r.Header.Get("X-Test-Scale") == "logit" {
			score = logit
		}
		for i := len(docs) - 1; i >= 0; i-- {
			parts = append(parts, fmt.Sprintf(`{"index":%d,"relevance_score":%v}`, i, score(docs[i])))
		}
		return `{"results":[` + strings.Join(parts, ",") + `]}`
	}
}

func documents(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = strings.Repeat("d", i%7+1) + fmt.Sprint(i)
	}
	return out
}

func anyStrings(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// TestRerankWireFormatPerVendor pins, for every rerank vendor in the catalog,
// the request the factory actually sends — URL, credential and the complete
// body — and that the answer comes back ranked on the 0..1 scale the
// retrieval threshold assumes. Each expectation traces to the vendor's
// reference as cited in its vendor.go.
func TestRerankWireFormatPerVendor(t *testing.T) {
	const query = "q"
	three := []string{"a", "bbb", "cc"}
	cohere := func(model string, docs []string, extra map[string]any) map[string]any {
		body := map[string]any{"model": model, "query": query, "documents": anyStrings(docs)}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	const volcInstruction = "Whether the document answers the query or matches the content retrieval intent"

	cases := []struct {
		name       string
		provider   string
		model      string
		base       string // appended to the upstream URL
		extra      map[string]string
		appID      string
		appSecret  string
		docs       []string
		logitScale bool // the protocol answers raw logits (NIM)
		// cohereLogs marks a Cohere-shape vendor that declares a logit scale
		// (GPUStack). The shape cannot tell the stub which scale to answer
		// in, so the case asks for logits through a test-only header.
		cohereLogs bool

		wantPath     string
		wantAuth     [2]string // header, a substring its value must contain
		wantRequests int
		// wantBody is one request's body, exactly. Batches go out
		// concurrently, so it is matched against every request rather than
		// the first to arrive.
		wantBody map[string]any
	}{
		{
			name: "jina echoes documents", provider: "jina", model: "jina-reranker-v3", base: "/v1",
			wantPath: "/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("jina-reranker-v3", three, map[string]any{"return_documents": true}),
		},
		{
			name: "zhipu on its full rerank URL", provider: "zhipu", model: "rerank", base: "/api/paas/v4/rerank",
			wantPath: "/api/paas/v4/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("rerank", three, map[string]any{"return_documents": true}),
		},
		{
			name: "zhipu splits at 128 documents", provider: "zhipu", model: "rerank", base: "/api/paas/v4/rerank",
			docs: documents(129), wantRequests: 2,
			wantPath: "/api/paas/v4/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("rerank", documents(128), map[string]any{"return_documents": true}),
		},
		{
			name: "siliconflow", provider: "siliconflow", model: "BAAI/bge-reranker-v2-m3", base: "/v1",
			wantPath: "/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("BAAI/bge-reranker-v2-m3", three, nil),
		},
		{
			name: "qianfan splits at 64 documents", provider: "qianfan", model: "bce-reranker-base", base: "/v2",
			docs: documents(65), wantRequests: 2,
			wantPath: "/v2/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("bce-reranker-base", documents(64), nil),
		},
		{
			name: "gpustack sends the row's truncation budget and answers logits", provider: "gpustack",
			model: "bge-reranker-v2-m3", base: "/v1",
			extra:      map[string]string{models.ExtraTruncatePromptTokens: "256"},
			cohereLogs: true,
			wantPath:   "/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("bge-reranker-v2-m3", three, map[string]any{"truncate_prompt_tokens": float64(256)}),
		},
		{
			name: "generic", provider: "generic", model: "bge-reranker-v2-m3", base: "/v1",
			wantPath: "/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("bge-reranker-v2-m3", three, nil),
		},
		{
			name: "novita", provider: "novita", model: "baai/bge-reranker-v2-m3", base: "/openai/v1",
			wantPath: "/openai/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("baai/bge-reranker-v2-m3", three, nil),
		},
		{
			name: "openrouter", provider: "openrouter", model: "cohere/rerank-v3.5", base: "/api/v1",
			wantPath: "/api/v1/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("cohere/rerank-v3.5", three, nil),
		},
		{
			name: "litellm on the proxy root", provider: "litellm", model: "cohere/rerank-v3.5",
			wantPath: "/rerank", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: cohere("cohere/rerank-v3.5", three, nil),
		},
		{
			name: "weknoracloud signs its own path", provider: "weknoracloud", model: "rerank",
			appID: "app", appSecret: "s",
			wantPath: "/api/v1/rerank", wantAuth: [2]string{"X-APPID", "app"},
			wantBody: cohere("rerank", three, nil),
		},
		{
			name: "aliyun wraps input and parameters", provider: "aliyun", model: "gte-rerank-v2",
			base:     "/api/v1/services/rerank/text-rerank/text-rerank",
			wantPath: "/api/v1/services/rerank/text-rerank/text-rerank",
			wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: map[string]any{
				"model":      "gte-rerank-v2",
				"input":      map[string]any{"query": query, "documents": anyStrings(three)},
				"parameters": map[string]any{"return_documents": true, "top_n": float64(3)},
			},
		},
		{
			name: "nvidia sends passages and answers logits", provider: "nvidia",
			model: "nvidia/nv-rerankqa-mistral-4b-v3", base: "/v1/retrieval/nvidia/reranking", logitScale: true,
			wantPath: "/v1/retrieval/nvidia/reranking", wantAuth: [2]string{"Authorization", "Bearer k"},
			wantBody: map[string]any{
				"model": "nvidia/nv-rerankqa-mistral-4b-v3",
				"query": map[string]any{"text": query},
				"passages": []any{
					map[string]any{"text": "a"}, map[string]any{"text": "bbb"}, map[string]any{"text": "cc"},
				},
				"truncate": "END",
			},
		},
		{
			name: "volcengine through the Knowledge Service SDK", provider: "volcengine", model: "doubao-seed-rerank",
			appSecret: "secret-test",
			wantPath:  "/api/knowledge/service/rerank", wantAuth: [2]string{"Authorization", "Credential=k/"},
			wantBody: map[string]any{
				"rerank_model":       "doubao-seed-rerank",
				"rerank_instruction": volcInstruction,
				"datas": []any{
					map[string]any{"query": query, "content": "a"},
					map[string]any{"query": query, "content": "bbb"},
					map[string]any{"query": query, "content": "cc"},
				},
			},
		},
		{
			name: "lkeap through the Tencent Cloud SDK", provider: "lkeap", model: "lke-reranker-base",
			appSecret: "secret-test",
			wantPath:  "/", wantAuth: [2]string{"Authorization", "Credential=k/"},
			wantBody: map[string]any{"Query": query, "Docs": anyStrings(three), "Model": "lke-reranker-base"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newUpstream(t)
			docs := tc.docs
			if docs == nil {
				docs = three
			}
			headers := map[string]string{}
			if tc.cohereLogs {
				headers["X-Test-Scale"] = "logit"
			}
			reranker, err := NewReranker(&RerankerConfig{
				Source: types.ModelSourceRemote, Provider: tc.provider, ModelName: tc.model,
				BaseURL: up.url + tc.base, APIKey: "k", ExtraConfig: tc.extra,
				AppID: tc.appID, AppSecret: tc.appSecret, CustomHeaders: headers,
			})
			require.NoError(t, err)

			got, err := reranker.Rerank(context.Background(), query, docs)
			require.NoError(t, err)

			wantRequests := tc.wantRequests
			if wantRequests == 0 {
				wantRequests = 1
			}
			require.Len(t, up.requests, wantRequests)
			bodies := make([]map[string]any, 0, len(up.requests))
			for _, req := range up.requests {
				assert.Equal(t, tc.wantPath, req.path)
				assert.Contains(t, req.header.Get(tc.wantAuth[0]), tc.wantAuth[1])
				if tc.appSecret != "" {
					assert.NotContains(t, req.header.Get("Authorization"), tc.appSecret,
						"a secret key signs the request and must never travel")
				}
				bodies = append(bodies, req.body)
			}
			assert.Contains(t, bodies, tc.wantBody)

			// Every document comes back once, ranked by the upstream's score,
			// and on the probability scale.
			require.Len(t, got, len(docs))
			want := append([]string(nil), docs...)
			sort.SliceStable(want, func(i, j int) bool {
				return utf8.RuneCountInString(want[i]) > utf8.RuneCountInString(want[j])
			})
			for i, r := range got {
				assert.Equal(t, docs[r.Index], r.Document.Text, "result %d carries the wrong text", i)
				assert.Equal(t, utf8.RuneCountInString(want[i]), utf8.RuneCountInString(r.Document.Text),
					"result %d is out of rank", i)
				assert.GreaterOrEqual(t, r.RelevanceScore, 0.0)
				assert.LessOrEqual(t, r.RelevanceScore, 1.0)
			}
			if tc.logitScale || tc.cohereLogs {
				// length 1 is a logit of -2: well below one half once converted.
				last := got[len(got)-1]
				assert.InDelta(t, 1/(1+math.Exp(2)), last.RelevanceScore, 1e-9,
					"a logit must be converted, not passed through")
			}
		})
	}
}

// OpenAI has no rerank API, so the vendor does not offer the type.
func TestOpenAIDoesNotOfferRerank(t *testing.T) {
	v, ok := modelruntime.Get("openai")
	require.True(t, ok)
	assert.False(t, v.SupportsType(types.ModelTypeRerank))
}
