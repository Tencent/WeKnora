package parity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		"weknoracloud": api.RerankCohere,
		"novita":       api.RerankCohere,
		"openrouter":   api.RerankCohere,
		"litellm":      api.RerankCohere,
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
	// Results come back ranked, so the logit of 4.0 leads and the negative
	// one follows: the conversion is monotonic and the ranking is preserved.
	assert.Equal(t, 1, got[0].Index)
	assert.Greater(t, got[0].RelevanceScore, got[1].RelevanceScore)
}

// TestSignedRerankVendorRequiresItsIdentityPair keeps the pre-catalog
// contract: WeKnora Cloud rerank is signed with an AppID / AppSecret pair, and
// a row without one must fail at construction rather than send unsigned
// requests that the far end rejects with something less readable.
func TestSignedRerankVendorRequiresItsIdentityPair(t *testing.T) {
	_, err := rerank.NewReranker(&rerank.RerankerConfig{
		Provider: "weknoracloud", ModelName: "rerank",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AppID")

	_, err = rerank.NewReranker(&rerank.RerankerConfig{
		Provider: "weknoracloud", ModelName: "rerank", AppID: "app",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AppSecret")
}

// TestTruncatePromptTokensOnlyReachesVLLMClassVendors pins the gate on the
// vLLM extension restored from the pre-catalog client. It is opt-in per row,
// but the row can only opt into it on a runtime that implements it: no
// managed vendor documents the field, and sending it to one is how an
// undocumented parameter ends up on every request.
func TestTruncatePromptTokensOnlyReachesVLLMClassVendors(t *testing.T) {
	optIn := map[string]string{catalog.ExtraTruncatePromptTokens: "512"}

	for _, id := range []string{"generic", "gpustack"} {
		t.Run(id+" accepts it", func(t *testing.T) {
			resolved, err := catalog.Resolve(catalog.Ref{
				Provider: id, Model: "m", ModelType: types.ModelTypeRerank,
				BaseURL: "http://127.0.0.1:9/v1", Extra: optIn,
			})
			require.NoError(t, err)
			assert.Equal(t, 512, resolved.Rerank.TruncatePromptTokens)
		})
	}

	for _, id := range []string{"jina", "zhipu", "siliconflow", "qianfan", "weknoracloud", "aliyun", "nvidia"} {
		t.Run(id+" rejects it", func(t *testing.T) {
			_, err := catalog.Resolve(catalog.Ref{
				Provider: id, Model: "m", ModelType: types.ModelTypeRerank, Extra: optIn,
			})
			require.Error(t, err, "a managed vendor must not silently accept a vLLM extension")
			assert.Contains(t, err.Error(), "vLLM extension")
		})
	}
}

func TestTruncatePromptTokensRejectsAnInvalidValue(t *testing.T) {
	for _, raw := range []string{"0", "-1", "abc"} {
		_, err := catalog.Resolve(catalog.Ref{
			Provider: "generic", Model: "m", ModelType: types.ModelTypeRerank,
			BaseURL: "http://127.0.0.1:9/v1",
			Extra:   map[string]string{catalog.ExtraTruncatePromptTokens: raw},
		})
		require.Error(t, err, "value %q", raw)
	}
}

// TestRerankCeilingsAreTheDocumentedOnes pins the numbers the shared batching
// layer enforces. They used to live inside each client, where a dedicated
// test watched them; now they are declarations, so they need their own guard —
// TestEveryRerankVendorResolvesToAKnownProtocol only checks they are sane.
func TestRerankCeilingsAreTheDocumentedOnes(t *testing.T) {
	for id, want := range map[string]catalog.RerankSettings{
		// docs.bigmodel.cn: 最多 128 条，query 与单条文档各 4096 字符
		"zhipu": {MaxDocuments: 128, MaxQueryChars: 4096, MaxDocumentChars: 4096},
		// cloud.tencent.com/document/product/1772: RunRerank 60 docs,
		// Query + Docs together 2000 characters, one request at a time.
		"lkeap": {MaxDocuments: 60, MaxRequestChars: 2000, MaxConcurrency: 1},
		// VikingDB Knowledge Service rerank: datas "数组长度不超过 200".
		"volcengine": {MaxDocuments: 200, MaxConcurrency: 4},
		// NIM reranking: passages is capped at 512 items.
		"nvidia": {MaxDocuments: 512},
		// cloud.baidu.com/doc/qianfan-api: 文本数量不超过64, query 不超过
		// 1600 个字符, 每条 document 不超过 4096 个字符.
		"qianfan": {MaxDocuments: 64, MaxQueryChars: 1600, MaxDocumentChars: 4096},
		// help.aliyun.com text-rerank: 500 documents per request. Its length
		// limits are stated in tokens, which runes cannot express.
		"aliyun": {MaxDocuments: 500},
	} {
		t.Run(id, func(t *testing.T) {
			resolved, err := catalog.Resolve(catalog.Ref{
				Provider: id, Model: "m", ModelType: types.ModelTypeRerank,
			})
			require.NoError(t, err)
			got := resolved.Rerank
			assert.Equal(t, want.MaxDocuments, got.MaxDocuments, "max documents")
			assert.Equal(t, want.MaxQueryChars, got.MaxQueryChars, "max query characters")
			assert.Equal(t, want.MaxDocumentChars, got.MaxDocumentChars, "max document characters")
			assert.Equal(t, want.MaxRequestChars, got.MaxRequestChars, "max request characters")
			assert.Equal(t, want.MaxConcurrency, got.MaxConcurrency, "max concurrency")
		})
	}
}

// TestWeKnoraCloudRerankKeepsItsDeadline pins the one rerank vendor that has
// ever had a client-level timeout. Without it a hung endpoint holds the
// retrieval stage open for as long as the caller's context allows.
func TestWeKnoraCloudRerankKeepsItsDeadline(t *testing.T) {
	resolved, err := catalog.Resolve(catalog.Ref{
		Provider: "weknoracloud", Model: "rerank", ModelType: types.ModelTypeRerank,
	})
	require.NoError(t, err)
	assert.Equal(t, 60, resolved.Rerank.RequestTimeout)

	// Everyone else has always run without one and relies on the caller.
	for _, id := range []string{"zhipu", "jina", "siliconflow", "aliyun", "nvidia", "generic"} {
		other, err := catalog.Resolve(catalog.Ref{
			Provider: id, Model: "m", ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err)
		assert.Zero(t, other.Rerank.RequestTimeout, "%s did not have a client deadline before", id)
	}
}

// TestTruncatePromptTokensIsReachableFromTheEditor closes the loop between the
// vendor accepting the extension and an operator being able to turn it on.
// Before the catalog it was readable from extra_config but had no input in the
// model editor, so the only way to set it was the API or the database.
// GET /models/providers renders extra fields dynamically, so declaring one is
// all it takes — and it must be declared exactly where the extension is
// accepted, or the form offers a switch the vendor will reject.
func TestTruncatePromptTokensIsReachableFromTheEditor(t *testing.T) {
	for _, v := range catalog.ListByType(types.ModelTypeRerank) {
		var field *catalog.ExtraField
		for i := range v.ExtraFields {
			if v.ExtraFields[i].Key == catalog.ExtraTruncatePromptTokens {
				field = &v.ExtraFields[i]
			}
		}
		resolved, err := catalog.Resolve(catalog.Ref{
			Provider: v.ID, Model: "m", ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err)

		if !resolved.Rerank.AcceptsTruncatePromptTokens {
			assert.Nil(t, field, "%s does not accept the extension, so it must not offer the input", v.ID)
			continue
		}
		require.NotNil(t, field, "%s accepts the extension but offers no way to set it", v.ID)
		assert.Equal(t, "number", field.Type)
		assert.False(t, field.Required, "the extension is opt-in")
		assert.Equal(t, []types.ModelType{types.ModelTypeRerank}, field.ModelTypes,
			"the input belongs to the rerank form only")
	}
}

// TestRerankScoreScalesMatchTheVendorDocs pins which vendors return something
// other than a 0..1 relevance score. Getting this wrong is silent: the number
// still looks like a score, and the retrieval threshold still compares it, so
// a logit-scaled vendor simply loses every negatively scored document.
func TestRerankScoreScalesMatchTheVendorDocs(t *testing.T) {
	// Both spell the field like a probability and return neither.
	//   NIM:      rankings[].logit, e.g. 0.226 / -1.171 / -6.31
	//   GPUStack: relevance_score,  e.g. 1.951 / -3.734 / -6.157
	logitScaled := map[string]bool{"nvidia": true, "gpustack": true}

	for _, v := range catalog.ListByType(types.ModelTypeRerank) {
		resolved, err := catalog.Resolve(catalog.Ref{
			Provider: v.ID, Model: "m", ModelType: types.ModelTypeRerank,
		})
		require.NoError(t, err)
		want := api.ScoreProbability
		if logitScaled[v.ID] {
			want = api.ScoreLogit
		}
		assert.Equal(t, want, resolved.Rerank.ScoreScale, "%s score scale", v.ID)
	}
}

// TestUnimplementedRerankDialectsAreRefused covers a model that names a
// protocol no package implements. Hiding it from the picker stops new rows;
// a row that already names it has to fail somewhere, and failing at
// construction with the reason beats sending a request shaped for the wrong
// protocol and reporting whatever the decoder makes of the reply.
func TestUnimplementedRerankDialectsAreRefused(t *testing.T) {
	v, ok := catalog.Get("aliyun")
	require.True(t, ok)

	offered := make([]string, 0)
	for _, m := range v.ModelsByType(types.ModelTypeRerank) {
		offered = append(offered, m.ID)
	}
	assert.NotContains(t, offered, "qwen3-rerank", "the picker must not offer an unimplemented dialect")
	assert.Contains(t, offered, "gte-rerank-v2")

	_, err := catalog.Resolve(catalog.Ref{
		Provider: "aliyun", Model: "qwen3-rerank", ModelType: types.ModelTypeRerank,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/compatible-api/v1/reranks",
		"the error should name the protocol the model actually speaks")

	// The vendor's other rerank model is unaffected.
	_, err = catalog.Resolve(catalog.Ref{
		Provider: "aliyun", Model: "gte-rerank-v2", ModelType: types.ModelTypeRerank,
	})
	require.NoError(t, err)
}

// TestGatewayRerankEndpoints pins the URL each gateway's rerank rows reach.
// All three serve the Cohere dialect the protocol package already speaks, so
// opening the type was a declaration — but LiteLLM documents the route on the
// proxy root rather than under /v1, which is why it needs its own default.
func TestGatewayRerankEndpoints(t *testing.T) {
	for _, tc := range []struct {
		provider string
		wantURL  string
	}{
		// https://docs.novita.ai/api-reference/model-apis-llm-create-rerank
		{provider: "novita", wantURL: "https://api.novita.ai/openai/v1/rerank"},
		// https://openrouter.ai/docs/api/api-reference/rerank/submit-a-rerank-request
		{provider: "openrouter", wantURL: "https://openrouter.ai/api/v1/rerank"},
		// https://docs.litellm.ai/docs/rerank — the curl example posts to
		// http://0.0.0.0:4000/rerank, not /v1/rerank.
		{provider: "litellm", wantURL: "http://your_litellm_proxy/rerank"},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			resolved, err := catalog.Resolve(catalog.Ref{
				Provider: tc.provider, Model: "m", ModelType: types.ModelTypeRerank,
			})
			require.NoError(t, err)
			require.Equal(t, api.RerankCohere, resolved.RerankAPI)

			// Rebuild the URL the way cohererank does.
			got := strings.TrimRight(resolved.BaseURL, "/")
			if !strings.HasSuffix(got, "/rerank") {
				got += "/rerank"
			}
			assert.Equal(t, tc.wantURL, got)
		})
	}
}

// TestScoreScaleIsOverridablePerRow covers the gateways, where the vendor
// cannot know the answer: GPUStack and a generic endpoint serve whatever
// reranker was deployed behind them, and the two families disagree — BGE
// answers a 0..1 probability, Qwen3-Reranker an unbounded score. Converting a
// probability as if it were a logit squeezes every score into [0.5, 0.73],
// which makes a relevance threshold meaningless, so the operator needs a way
// to say which one is actually there.
func TestScoreScaleIsOverridablePerRow(t *testing.T) {
	for _, id := range []string{"gpustack", "generic"} {
		t.Run(id, func(t *testing.T) {
			base := "http://127.0.0.1:9/v1"
			overridden, err := catalog.Resolve(catalog.Ref{
				Provider: id, Model: "bge-reranker-v2-m3", ModelType: types.ModelTypeRerank,
				BaseURL: base, Extra: map[string]string{catalog.ExtraScoreScale: "probability"},
			})
			require.NoError(t, err)
			assert.Equal(t, api.ScoreProbability, overridden.Rerank.ScoreScale)

			// And the input to set it is rendered by the editor.
			v, ok := catalog.Get(id)
			require.True(t, ok)
			var field *catalog.ExtraField
			for i := range v.ExtraFields {
				if v.ExtraFields[i].Key == catalog.ExtraScoreScale {
					field = &v.ExtraFields[i]
				}
			}
			require.NotNil(t, field, "%s must offer the override it needs", id)
			assert.Equal(t, "select", field.Type)
			assert.Len(t, field.Options, 2)
		})
	}

	_, err := catalog.Resolve(catalog.Ref{
		Provider: "gpustack", Model: "m", ModelType: types.ModelTypeRerank,
		BaseURL: "http://127.0.0.1:9/v1",
		Extra:   map[string]string{catalog.ExtraScoreScale: "sigmoid"},
	})
	require.Error(t, err, "an unknown scale must not be treated as a probability")
}

// TestGatewayVendorsOfferTheScoreScaleOverride keeps the override where the
// question is open. A gateway cannot know which reranker is deployed behind
// it, so all three offer the input; the other vendors' scales are settled by
// their own documentation and must not offer a switch that only lets an
// operator get it wrong.
func TestGatewayVendorsOfferTheScoreScaleOverride(t *testing.T) {
	gateways := map[string]bool{"generic": true, "gpustack": true, "litellm": true}

	for _, v := range catalog.ListByType(types.ModelTypeRerank) {
		var field *catalog.ExtraField
		for i := range v.ExtraFields {
			if v.ExtraFields[i].Key == catalog.ExtraScoreScale {
				field = &v.ExtraFields[i]
			}
		}
		if !gateways[v.ID] {
			assert.Nil(t, field, "%s: its documentation settles the scale", v.ID)
			continue
		}
		require.NotNil(t, field, "%s is a gateway and needs the override", v.ID)
		require.Len(t, field.Options, 2)
		for _, opt := range field.Options {
			assert.NotEmpty(t, opt.LocalizedLabel("zh-CN"), "%s: option %q has no label", v.ID, opt.Value)
			assert.NotEqual(t, opt.Label, opt.LocalizedLabel("zh-CN"),
				"%s: option %q is prose and needs a zh-CN label", v.ID, opt.Value)
		}
		assert.NotEqual(t, field.Placeholder, field.LocalizedPlaceholder("zh-CN"),
			"%s: the placeholder is prose and needs a zh-CN variant", v.ID)
	}
}

// Every operator-facing string a vendor declares has to exist in both
// languages, or half the product reads English prose in a Chinese form.
func TestEveryExtraFieldProseIsLocalized(t *testing.T) {
	for _, v := range catalog.List() {
		for _, field := range v.ExtraFields {
			if field.Placeholder != "" && strings.Contains(field.Placeholder, " ") {
				assert.NotEqual(t, field.Placeholder, field.LocalizedPlaceholder("zh-CN"),
					"%s/%s: placeholder is prose without a zh-CN variant", v.ID, field.Key)
			}
			for _, opt := range field.Options {
				// An identifier (a region code, a version) is the same in
				// every language; prose is not.
				if strings.Contains(opt.Label, " ") {
					assert.NotEqual(t, opt.Label, opt.LocalizedLabel("zh-CN"),
						"%s/%s: option %q is prose without a zh-CN label", v.ID, field.Key, opt.Value)
				}
			}
		}
	}
}
