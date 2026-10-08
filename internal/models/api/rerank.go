package api

import "context"

// RerankAPI names a rerank wire protocol. It is deliberately a separate type
// from API, the chat protocol selector: the two vocabularies must not mix, or
// a chat row whose extra_config.api names a rerank dialect would pass
// validation and fail at call time.
type RerankAPI string

// The rerank protocols WeKnora speaks.
const (
	// RerankCohere is the de-facto standard shape — POST {base}/rerank with
	// {model, query, documents} and a results array of {index,
	// relevance_score, document}. Cohere defined it; Jina, Zhipu,
	// SiliconFlow, Qianfan, GPUStack and every OpenAI-compatible gateway
	// that serves rerank at all copy it.
	RerankCohere RerankAPI = "cohere-rerank"
	// RerankDashScope is Alibaba Model Studio's native shape, which wraps
	// the same fields in input/parameters and answers under output.
	RerankDashScope RerankAPI = "dashscope-rerank"
	// RerankNIM is NVIDIA NIM's retrieval shape: a query object, a passages
	// array, and rankings carrying an unbounded logit instead of a
	// probability.
	RerankNIM RerankAPI = "nim-rerank"
	// RerankTencentLKEAP is Tencent Cloud's RunRerank action, reached through
	// the official SDK because it is TC3-signed rather than key-authenticated.
	RerankTencentLKEAP RerankAPI = "tencent-lkeap"
	// RerankVolcengineKnowledge is Volcengine's managed Knowledge Service
	// rerank, reached through the vikingdb SDK for the same reason.
	RerankVolcengineKnowledge RerankAPI = "volcengine-knowledge"
)

// Known reports whether the value names a protocol this build implements.
func (a RerankAPI) Known() bool {
	switch a {
	case RerankCohere, RerankDashScope, RerankNIM,
		RerankTencentLKEAP, RerankVolcengineKnowledge:
		return true
	}
	return false
}

// HTTPServed reports whether a protocol package implements the protocol. The
// SDK-backed ones are built by the rerank package itself, which is where the
// vendor SDKs are imported.
func (a RerankAPI) HTTPServed() bool {
	switch a {
	case RerankCohere, RerankDashScope, RerankNIM:
		return true
	}
	return false
}

// ScoreScale describes what a rerank score means, which differs per protocol
// and cannot be inferred from the number itself.
type ScoreScale string

const (
	// ScoreProbability is a 0..1 relevance score. Every protocol but NIM
	// returns one.
	ScoreProbability ScoreScale = "probability"
	// ScoreLogit is an unbounded log-odds value that may be negative. NIM
	// returns one, so a threshold tuned for probabilities rejects almost
	// everything on that vendor.
	ScoreLogit ScoreScale = "logit"
)

// RerankResult is one scored document, in the vendor's own score scale.
// Index refers to the position in the documents slice that was sent.
type RerankResult struct {
	Index int
	Score float64
	// Text is the document echoed back, empty when the vendor was not asked
	// to return documents. Callers index into their own slice instead of
	// relying on it.
	Text string
}

// Reranker is what every rerank protocol package implements.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, error)
}
