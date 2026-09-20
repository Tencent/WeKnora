package parity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryRerankVendorResolvesToAKnownProtocol walks the catalog, so a newly
// added vendor is covered without touching this file.
func TestEveryRerankVendorResolvesToAKnownProtocol(t *testing.T) {
	vendors := catalog.ListByType(types.ModelTypeRerank)
	require.NotEmpty(t, vendors, "no vendor serves rerank")

	for _, v := range vendors {
		t.Run(v.ID, func(t *testing.T) {
			model := "some-rerank-model"
			if catalogued := v.ModelsByType(types.ModelTypeRerank); len(catalogued) > 0 {
				model = catalogued[0].ID
			}
			resolved, err := catalog.Resolve(catalog.Ref{
				Provider: v.ID, Model: model, ModelType: types.ModelTypeRerank,
			})
			require.NoError(t, err)
			assert.True(t, resolved.RerankAPI.Known(),
				"unknown rerank protocol %q", resolved.RerankAPI)
			// The catch-all vendor has no endpoint of its own: the operator
			// supplies one, and Validate requires it.
			if v.ID != catalog.GenericID {
				assert.NotEmpty(t, resolved.BaseURL, "rerank vendors need a default endpoint")
			}

			// A vendor that declares a ceiling must declare a usable one.
			assert.GreaterOrEqual(t, resolved.Rerank.MaxDocuments, 0)
			assert.GreaterOrEqual(t, resolved.Rerank.MaxDocumentChars, 0)
			if resolved.Rerank.ScoreScale != "" {
				assert.Contains(t,
					[]api.ScoreScale{api.ScoreProbability, api.ScoreLogit},
					resolved.Rerank.ScoreScale)
			}
		})
	}
}

// TestRerankProtocolAssignment pins which dialect each vendor speaks. The
// wire shapes are mutually incompatible, so a silent reassignment breaks
// every rerank call for that vendor.
func TestRerankProtocolAssignment(t *testing.T) {
	for id, want := range map[string]api.RerankAPI{
		"aliyun":       api.RerankDashScope,
		"nvidia":       api.RerankNIM,
		"lkeap":        api.RerankTencentLKEAP,
		"volcengine":   api.RerankVolcengineKnowledge,
		"zhipu":        api.RerankCohere,
		"jina":         api.RerankCohere,
		"siliconflow":  api.RerankCohere,
		"qianfan":      api.RerankCohere,
		"gpustack":     api.RerankCohere,
		"generic":      api.RerankCohere,
		"openai":       api.RerankCohere,
		"weknoracloud": api.RerankCohere,
	} {
		t.Run(id, func(t *testing.T) {
			resolved, err := catalog.Resolve(catalog.Ref{
				Provider: id, Model: "m", ModelType: types.ModelTypeRerank,
			})
			require.NoError(t, err)
			assert.Equal(t, want, resolved.RerankAPI)
		})
	}
}

// TestRerankOutboundShapePerProtocol builds the real client through
// rerank.NewReranker and asserts what each dialect puts on the wire.
func TestRerankOutboundShapePerProtocol(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		model    string
		assert   func(t *testing.T, path string, body map[string]any)
		reply    string
	}{
		{
			name: "cohere shape", provider: "siliconflow", model: "BAAI/bge-reranker-v2-m3",
			reply: `{"results":[{"index":0,"relevance_score":0.5}]}`,
			assert: func(t *testing.T, path string, body map[string]any) {
				assert.Equal(t, "/v1/rerank", path)
				assert.Equal(t, "BAAI/bge-reranker-v2-m3", body["model"])
				assert.Equal(t, "q", body["query"])
				assert.Equal(t, []any{"d0"}, body["documents"])
			},
		},
		{
			name: "dashscope shape", provider: "aliyun", model: "gte-rerank-v2",
			reply: `{"output":{"results":[{"index":0,"relevance_score":0.5,"document":{"text":"d0"}}]}}`,
			assert: func(t *testing.T, _ string, body map[string]any) {
				input, ok := body["input"].(map[string]any)
				require.True(t, ok, "DashScope wraps the query and documents in input")
				assert.Equal(t, "q", input["query"])
				assert.Contains(t, body, "parameters")
				assert.NotContains(t, body, "documents", "documents live under input")
			},
		},
		{
			name: "nim shape", provider: "nvidia", model: "nvidia/nv-rerankqa-mistral-4b-v3",
			reply: `{"rankings":[{"index":0,"logit":0.0}]}`,
			assert: func(t *testing.T, _ string, body map[string]any) {
				query, ok := body["query"].(map[string]any)
				require.True(t, ok, "NIM sends the query as an object")
				assert.Equal(t, "q", query["text"])
				assert.Equal(t, []any{map[string]any{"text": "d0"}}, body["passages"])
				assert.Equal(t, "END", body["truncate"], "NIM fails over-long input unless told to cut")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.reply))
			}))
			defer server.Close()

			reranker, err := rerank.NewReranker(&rerank.RerankerConfig{
				Provider: tc.provider, ModelName: tc.model, APIKey: "k",
				BaseURL: server.URL + rerankPathFor(t, tc.provider),
			})
			require.NoError(t, err)

			_, err = reranker.Rerank(context.Background(), "q", []string{"d0"})
			require.NoError(t, err)
			tc.assert(t, gotPath, gotBody)
		})
	}
}

// rerankPathFor keeps the loopback base URL shaped like the vendor's own, so
// the test exercises the same path handling production does.
func rerankPathFor(t *testing.T, provider string) string {
	t.Helper()
	switch provider {
	case "aliyun":
		return "/api/v1/services/rerank/text-rerank/text-rerank"
	case "nvidia":
		return "/v1/retrieval/nvidia/reranking"
	default:
		return "/v1"
	}
}

// TestNvidiaRerankScoresAreComparable is the end-to-end form of the unit test
// in the rerank package: a NIM logit must reach the retrieval pipeline on the
// same 0..1 scale as every other vendor, because they all meet one
// RerankThreshold.
func TestNvidiaRerankScoresAreComparable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"rankings":[{"index":0,"logit":-1.171875},{"index":1,"logit":4.0}]}`))
	}))
	defer server.Close()

	reranker, err := rerank.NewReranker(&rerank.RerankerConfig{
		Provider: "nvidia", ModelName: "nvidia/nv-rerankqa-mistral-4b-v3", APIKey: "k",
		BaseURL: server.URL + "/v1/retrieval/nvidia/reranking",
	})
	require.NoError(t, err)

	got, err := reranker.Rerank(context.Background(), "q", []string{"d0", "d1"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	for _, item := range got {
		assert.GreaterOrEqual(t, item.RelevanceScore, 0.0)
		assert.LessOrEqual(t, item.RelevanceScore, 1.0)
	}
	assert.Less(t, got[0].RelevanceScore, got[1].RelevanceScore, "order must survive the conversion")
}
