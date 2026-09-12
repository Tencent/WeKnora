package adapters

// zhipu.go — the zhipu (智谱 BigModel) vendor deltas. Chat rides the openai
// family funnel and embedding the shared openai shape (embedSpecFor: no
// encoding_format, truncate_prompt_tokens 511); the rerank facet is bespoke —
// v1 posts to the base URL DIRECTLY, the default base being the FULL endpoint
// (…/v4/rerank), not a host prefix.

import (
	"github.com/Tencent/WeKnora/internal/models/invoke"
)

type zhipuRerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
	ReturnRawScores bool     `json:"return_raw_scores,omitempty"`
}

func buildZhipuRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	base := ep.BaseURL
	if base == "" {
		base = "https://open.bigmodel.cn/api/paas/v4/rerank"
	}
	body := zhipuRerankRequest{
		Model:           model,
		Query:           opts.Query,
		Documents:       opts.Documents,
		TopN:            0, // v1: return all documents
		ReturnDocuments: true,
		ReturnRawScores: false,
	}
	return buildRerankRequestShared(base, ep, body, "")
}
