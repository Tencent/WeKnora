package regression

import (
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Mismatch is one contract field whose value differs between the frozen
// baseline and the current result. A mismatch is a configuration problem, never
// a quality regression.
type Mismatch struct {
	Field    string `json:"field"`
	Baseline string `json:"baseline"`
	Current  string `json:"current"`
}

// CheckCompatibility compares the frozen Benchmark v1.1 contract of baseline
// and current and returns every field that differs (never just the first).
//
// It deliberately reads only the existing reproducibility/config snapshot on
// types.BenchmarkResult and does not redefine or recompute any contract. Its
// purpose is fail-closed protection: two results produced under a different
// model, dataset, or retrieval contract must not be compared as if they were
// the same benchmark.
func CheckCompatibility(baseline, current *types.BenchmarkResult) []Mismatch {
	if baseline == nil || current == nil {
		// nil is a caller-level error, handled by Compare/CLI before this runs.
		return nil
	}
	b, c := baseline, current

	checks := []struct {
		label             string
		baseline, current string
	}{
		{"benchmark_version", b.BenchmarkVersion, c.BenchmarkVersion},
		{"dataset.dataset_id", b.Config.Dataset.DatasetID, c.Config.Dataset.DatasetID},
		{"dataset.dataset_semantic_sha256", b.Config.Dataset.DatasetSemanticSHA256, c.Config.Dataset.DatasetSemanticSHA256},
		{"dataset.corpus_count", intStr(b.Config.Dataset.CorpusCount), intStr(c.Config.Dataset.CorpusCount)},
		{"dataset.question_count", intStr(b.Config.Dataset.QuestionCount), intStr(c.Config.Dataset.QuestionCount)},
		{
			"retrieval.vector_threshold",
			floatStr(b.Config.Retrieval.VectorThreshold), floatStr(c.Config.Retrieval.VectorThreshold),
		},
		{
			"retrieval.keyword_threshold",
			floatStr(b.Config.Retrieval.KeywordThreshold), floatStr(c.Config.Retrieval.KeywordThreshold),
		},
		{"retrieval.embedding_top_k", intStr(b.Config.Retrieval.EmbeddingTopK), intStr(c.Config.Retrieval.EmbeddingTopK)},
		{"retrieval.rerank_top_k", intStr(b.Config.Retrieval.RerankTopK), intStr(c.Config.Retrieval.RerankTopK)},
		{
			"retrieval.rerank_threshold",
			floatStr(b.Config.Retrieval.RerankThreshold), floatStr(c.Config.Retrieval.RerankThreshold),
		},
		{"retrieval.retrieve_driver", b.Config.Retrieval.RetrieveDriver, c.Config.Retrieval.RetrieveDriver},
		{"models.embedding.name", modelName(b.Config.Models.Embedding), modelName(c.Config.Models.Embedding)},
		{"models.embedding.provider", modelProvider(b.Config.Models.Embedding), modelProvider(c.Config.Models.Embedding)},
		{"models.embedding.dimension", modelDimension(b.Config.Models.Embedding), modelDimension(c.Config.Models.Embedding)},
		{"models.chat.name", modelName(b.Config.Models.Chat), modelName(c.Config.Models.Chat)},
		{"models.chat.provider", modelProvider(b.Config.Models.Chat), modelProvider(c.Config.Models.Chat)},
		{"models.rerank.name", modelName(b.Config.Models.Rerank), modelName(c.Config.Models.Rerank)},
		{"models.rerank.provider", modelProvider(b.Config.Models.Rerank), modelProvider(c.Config.Models.Rerank)},
		{"execution.worker_limit", intStr(b.Config.Execution.WorkerLimit), intStr(c.Config.Execution.WorkerLimit)},
	}

	var out []Mismatch
	for _, ch := range checks {
		if ch.baseline != ch.current {
			out = append(out, Mismatch{Field: ch.label, Baseline: ch.baseline, Current: ch.current})
		}
	}
	return out
}

// FormatMismatches renders a human-readable compatibility failure. It lists
// every mismatch with its baseline and current value.
func FormatMismatches(mismatches []Mismatch) string {
	var b strings.Builder
	b.WriteString("Compatibility: FAIL\n\n")
	for _, m := range mismatches {
		writeBuilder(&b, "%s\n", m.Field)
		writeBuilder(&b, "baseline = %s\n", m.Baseline)
		writeBuilder(&b, "current  = %s\n\n", m.Current)
	}
	return b.String()
}

func intStr(v int) string { return strconv.Itoa(v) }

func floatStr(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func modelName(m *types.EvaluationConfiguredModelSnapshot) string {
	if m == nil {
		return "<missing>"
	}
	return m.Name
}

func modelProvider(m *types.EvaluationConfiguredModelSnapshot) string {
	if m == nil {
		return "<missing>"
	}
	return m.Provider
}

func modelDimension(m *types.EvaluationConfiguredModelSnapshot) string {
	if m == nil || m.Embedding == nil {
		return "<missing>"
	}
	return intStr(m.Embedding.Dimension)
}
