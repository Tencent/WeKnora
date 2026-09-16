package adapters

// nvidia.go — the nvidia NIM vendor deltas. Embedding rides the shared openai
// shape (embedSpecFor: the input_type tag); the rerank facet is bespoke —
// query and passages are {text} objects and scores come back as raw logits
// under "rankings", normalized to probabilities here (downstream callers
// filter on 0-1 thresholds).

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"

	"github.com/Tencent/WeKnora/internal/models/invoke"
)

type nvidiaRerankDocument struct {
	Text string `json:"text"`
}

type nvidiaRerankRequest struct {
	Model    string                 `json:"model"`
	Query    nvidiaRerankDocument   `json:"query"`
	Passages []nvidiaRerankDocument `json:"passages"`
}

func buildNvidiaRerank(ep invoke.Endpoint, model string, opts *invoke.RerankOptions) (*invoke.Request, error) {
	// v1 posts to the base URL DIRECTLY (full endpoint …/reranking).
	base := ep.BaseURL
	if base == "" {
		base = invoke.NvidiaRerankBaseURL
	}
	passages := make([]nvidiaRerankDocument, 0, len(opts.Documents))
	for _, doc := range opts.Documents {
		passages = append(passages, nvidiaRerankDocument{Text: doc})
	}
	body := nvidiaRerankRequest{
		Model:    model,
		Query:    nvidiaRerankDocument{Text: opts.Query},
		Passages: passages,
	}
	return buildRerankRequestShared(base, ep, body, "")
}

// parseNvidiaRerank reads the rankings[] envelope and normalizes the raw
// logit into a probability exactly as the v1 client did
// (normalizeNvidiaLogit — P3 review finding 1).
func parseNvidiaRerank(_ int, _ http.Header, body []byte) (*invoke.RerankResponse, error) {
	var resp struct {
		Rankings []rankResultWire `json:"rankings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, invoke.ClassifyError(fmt.Errorf("unmarshal response: %w", err))
	}
	out := make([]invoke.RerankResult, 0, len(resp.Rankings))
	for _, r := range resp.Rankings {
		out = append(out, invoke.RerankResult{Index: r.Index, Score: sigmoid(r.Score)})
	}
	return &invoke.RerankResponse{Results: out}, nil
}

// sigmoid is the numerically-stable v1 normalizeNvidiaLogit (nvidia
// reranker): raw reranker logit → (0,1) probability.
func sigmoid(logit float64) float64 {
	if logit >= 0 {
		return 1 / (1 + math.Exp(-logit))
	}
	expLogit := math.Exp(logit)
	return expLogit / (1 + expLogit)
}
