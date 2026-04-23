package rerank

import (
	"context"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// langfuseReranker wraps a Reranker and reports each rerank call as a
// Langfuse generation observation. Rerankers don't return token usage, but
// the call still incurs cost (billed per 1K documents by most vendors); we
// estimate input tokens from the query + documents so the Langfuse cost
// dashboard gets a proportional signal.
type langfuseReranker struct {
	inner Reranker
}

func (l *langfuseReranker) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseReranker) GetModelID() string   { return l.inner.GetModelID() }

func (l *langfuseReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	mgr := langfuse.GetManager()
	if !mgr.Enabled() {
		return l.inner.Rerank(ctx, query, documents)
	}

	genCtx, gen := mgr.StartGeneration(ctx, langfuse.GenerationOptions{
		Name:  "rerank",
		Model: l.inner.GetModelName(),
		Input: map[string]interface{}{
			"query":          query,
			"document_count": len(documents),
			// Only send short previews — reranker inputs can be hundreds of
			// passages totalling hundreds of KB, which would bloat traces.
			"documents_preview": previewDocs(documents, 5),
		},
		Metadata: map[string]interface{}{
			"model_id":    l.inner.GetModelID(),
			"num_queries": 1,
		},
	})

	results, err := l.inner.Rerank(genCtx, query, documents)

	output := map[string]interface{}{
		"results":     summarizeResults(results, 10),
		"total_count": len(results),
	}
	gen.Finish(output, approxRerankUsage(query, documents), err)
	return results, err
}

func approxRerankUsage(query string, documents []string) *langfuse.TokenUsage {
	total := len([]rune(query))/4 + 1
	for _, d := range documents {
		total += len([]rune(d))/4 + 1
	}
	if total == 0 {
		return nil
	}
	return &langfuse.TokenUsage{
		Input: total,
		Total: total,
		Unit:  "TOKENS",
	}
}

func previewDocs(docs []string, n int) []map[string]interface{} {
	if len(docs) < n {
		n = len(docs)
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]interface{}{
			"index":   i,
			"preview": truncateRunes(docs[i], 160),
			"length":  len([]rune(docs[i])),
		})
	}
	return out
}

func summarizeResults(results []RankResult, n int) []map[string]interface{} {
	if len(results) < n {
		n = len(results)
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]interface{}{
			"index": results[i].Index,
			"score": results[i].RelevanceScore,
		})
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "..."
}

// wrapRerankerLangfuse applies the Langfuse decorator when the manager is
// enabled. Called from NewReranker after the debug wrapper so both sinks see
// the same calls.
func wrapRerankerLangfuse(r Reranker, err error) (Reranker, error) {
	if err != nil || r == nil {
		return r, err
	}
	if !langfuse.GetManager().Enabled() {
		return r, nil
	}
	return &langfuseReranker{inner: r}, nil
}
