package rerank

import (
	"context"
	"errors"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

const langfuseRerankPreviewDocs = 8
const langfuseRerankMaxScores = 50

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

	totalChars := len([]rune(query))
	for _, doc := range documents {
		totalChars += len([]rune(doc))
	}

	genCtx, gen := mgr.StartGeneration(
		ctx,
		buildLangfuseRerankOptions(
			ctx,
			l.inner.GetModelName(),
			l.inner.GetModelID(),
			query,
			documents,
			totalChars,
		),
	)

	results, err := l.inner.Rerank(genCtx, query, documents)

	gen.Finish(
		buildLangfuseRerankOutput(ctx, results, documents),
		approxRerankUsage(query, documents),
		buildLangfuseRerankError(ctx, err),
	)
	return results, err
}

func buildLangfuseRerankOptions(
	ctx context.Context,
	modelName string,
	modelID string,
	query string,
	documents []string,
	totalChars int,
) langfuse.GenerationOptions {
	options := langfuse.GenerationOptions{
		Name:  "rerank",
		Model: modelName,
		Input: map[string]interface{}{
			"query":          query,
			"document_count": len(documents),
			"documents_preview": previewDocs(
				documents,
				langfuseRerankPreviewDocs,
			),
		},
		Metadata: map[string]interface{}{
			"model_id":      modelID,
			"num_queries":   1,
			"total_chars":   totalChars,
			"avg_doc_chars": avgDocChars(documents),
		},
	}
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		options.Model = "prepared-knowledge-model"
		options.Input = map[string]interface{}{
			"query_length":      len([]rune(query)),
			"document_count":    len(documents),
			"scope_hash_prefix": hashPrefix,
		}
		options.Metadata = map[string]interface{}{
			"num_queries":       1,
			"total_chars":       totalChars,
			"avg_doc_chars":     avgDocChars(documents),
			"scope_hash_prefix": hashPrefix,
		}
	}
	return options
}

func buildLangfuseRerankOutput(
	ctx context.Context,
	results []RankResult,
	documents []string,
) map[string]interface{} {
	output := map[string]interface{}{
		"results": summarizeResults(
			results,
			documents,
			langfuseRerankMaxScores,
		),
		"total_count": len(results),
		"score_stats": scoreStats(results),
	}
	if len(results) > langfuseRerankMaxScores {
		output["truncated"] = len(results) - langfuseRerankMaxScores
	}
	if hashPrefix, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		delete(output, "results")
		output["scope_hash_prefix"] = hashPrefix
	}
	return output
}

func buildLangfuseRerankError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if _, prepared := langfuse.PreparedKnowledgeScopeHashPrefix(ctx); prepared {
		return errors.New("prepared rerank failed")
	}
	return err
}

func avgDocChars(documents []string) int {
	if len(documents) == 0 {
		return 0
	}
	total := 0
	for _, doc := range documents {
		total += len([]rune(doc))
	}
	return total / len(documents)
}

func scoreStats(results []RankResult) map[string]interface{} {
	if len(results) == 0 {
		return nil
	}
	minScore := results[0].RelevanceScore
	maxScore := results[0].RelevanceScore
	sum := 0.0
	for _, r := range results {
		if r.RelevanceScore < minScore {
			minScore = r.RelevanceScore
		}
		if r.RelevanceScore > maxScore {
			maxScore = r.RelevanceScore
		}
		sum += r.RelevanceScore
	}
	return map[string]interface{}{
		"min": minScore,
		"max": maxScore,
		"avg": sum / float64(len(results)),
	}
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

func summarizeResults(results []RankResult, documents []string, n int) []map[string]interface{} {
	if len(results) < n {
		n = len(results)
	}
	out := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		row := map[string]interface{}{
			"rank":        i + 1,
			"index":       results[i].Index,
			"model_score": results[i].RelevanceScore,
		}
		idx := results[i].Index
		if idx >= 0 && idx < len(documents) {
			row["preview"] = truncateRunes(documents[idx], 160)
		}
		out = append(out, row)
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
