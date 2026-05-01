package rerank

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/provider"
)

// Declarative rerank provider registry. Each spec replaces a ~130-line file
// that existed before the refactor.

// openaiSpec targets OpenAI-compatible /rerank endpoints. Used as the
// fallback for any provider without its own spec.
var openaiSpec = rerankerSpec{
	providerName:   "OpenAI",
	defaultBaseURL: "https://api.openai.com/v1",
	buildBody: func(r *httpReranker, query string, documents []string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"model":                  r.modelName,
			"query":                  query,
			"documents":              documents,
			"additional_data":        nil,
			"truncate_prompt_tokens": 511,
		})
	},
	parseBody: func(body []byte, _ []string) ([]RankResult, error) {
		var response struct {
			Results []RankResult `json:"results"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		return response.Results, nil
	},
}

// jinaSpec targets api.jina.ai. Jina omits truncate_prompt_tokens and uses
// return_documents=true to get text back in the response.
var jinaSpec = rerankerSpec{
	providerName:   "Jina",
	defaultBaseURL: "https://api.jina.ai/v1",
	buildBody: func(r *httpReranker, query string, documents []string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"model":            r.modelName,
			"query":            query,
			"documents":        documents,
			"return_documents": true,
		})
	},
	parseBody: func(body []byte, _ []string) ([]RankResult, error) {
		var response struct {
			Results []RankResult `json:"results"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		return response.Results, nil
	},
}

// aliyunSpec targets DashScope text-rerank. The endpoint URL is itself the
// full path, and the request shape nests query+documents under input{}.
var aliyunSpec = rerankerSpec{
	providerName:    "Aliyun",
	defaultBaseURL:  "https://dashscope.aliyuncs.com/api/v1/services/rerank/text-rerank/text-rerank",
	fullEndpointURL: true,
	buildBody: func(r *httpReranker, query string, documents []string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"model": r.modelName,
			"input": map[string]any{
				"query":     query,
				"documents": documents,
			},
			"parameters": map[string]any{
				"return_documents": true,
				"top_n":            len(documents),
			},
		})
	},
	parseBody: func(body []byte, _ []string) ([]RankResult, error) {
		var response struct {
			Output struct {
				Results []struct {
					Document struct {
						Text string `json:"text"`
					} `json:"document"`
					Index          int     `json:"index"`
					RelevanceScore float64 `json:"relevance_score"`
				} `json:"results"`
			} `json:"output"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		out := make([]RankResult, len(response.Output.Results))
		for i, r := range response.Output.Results {
			out[i] = RankResult{
				Index:          r.Index,
				Document:       DocumentInfo{Text: r.Document.Text},
				RelevanceScore: r.RelevanceScore,
			}
		}
		return out, nil
	},
}

// zhipuSpec targets Zhipu AI's paas/v4/rerank endpoint. Zhipu's response uses
// a plain `document` string field rather than an object.
var zhipuSpec = rerankerSpec{
	providerName:    "Zhipu",
	defaultBaseURL:  "https://open.bigmodel.cn/api/paas/v4/rerank",
	fullEndpointURL: true,
	buildBody: func(r *httpReranker, query string, documents []string) ([]byte, error) {
		return json.Marshal(map[string]any{
			"model":             r.modelName,
			"query":             query,
			"documents":         documents,
			"top_n":             0, // 0 means return all
			"return_documents":  true,
			"return_raw_scores": false,
		})
	},
	parseBody: func(body []byte, _ []string) ([]RankResult, error) {
		var response struct {
			Results []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"relevance_score"`
				Document       string  `json:"document"`
			} `json:"results"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		out := make([]RankResult, len(response.Results))
		for i, r := range response.Results {
			out[i] = RankResult{
				Index:          r.Index,
				Document:       DocumentInfo{Text: r.Document},
				RelevanceScore: r.RelevanceScore,
			}
		}
		return out, nil
	},
}

// nvidiaSpec targets ai.api.nvidia.com. Nvidia uses "passages" instead of
// "documents" and returns "rankings" with a "logit" score field.
var nvidiaSpec = rerankerSpec{
	providerName:    "Nvidia",
	defaultBaseURL:  "https://ai.api.nvidia.com/v1/retrieval/nvidia/reranking",
	fullEndpointURL: true,
	buildBody: func(r *httpReranker, query string, documents []string) ([]byte, error) {
		passages := make([]map[string]any, len(documents))
		for i, d := range documents {
			passages[i] = map[string]any{"text": d}
		}
		return json.Marshal(map[string]any{
			"model":    r.modelName,
			"query":    map[string]any{"text": query},
			"passages": passages,
		})
	},
	parseBody: func(body []byte, documents []string) ([]RankResult, error) {
		var response struct {
			Rankings []struct {
				Index          int     `json:"index"`
				RelevanceScore float64 `json:"logit"`
			} `json:"rankings"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}
		out := make([]RankResult, len(response.Rankings))
		for i, r := range response.Rankings {
			// Nvidia's response doesn't echo the document text, so we look it
			// up in the original slice by the returned index.
			text := ""
			if r.Index >= 0 && r.Index < len(documents) {
				text = documents[r.Index]
			}
			out[i] = RankResult{
				Index:          r.Index,
				Document:       DocumentInfo{Text: text},
				RelevanceScore: r.RelevanceScore,
			}
		}
		return out, nil
	},
}

// specForProvider picks the reranker spec for the given provider.
// Returns false for providers handled out-of-band (WeKnoraCloud).
func specForProvider(name provider.ProviderName) (rerankerSpec, bool) {
	switch name {
	case provider.ProviderAliyun:
		return aliyunSpec, true
	case provider.ProviderZhipu:
		return zhipuSpec, true
	case provider.ProviderJina:
		return jinaSpec, true
	case provider.ProviderNvidia:
		return nvidiaSpec, true
	}
	// WeKnoraCloud is handled separately; everything else falls back to the
	// OpenAI-compatible /rerank spec.
	return openaiSpec, true
}
