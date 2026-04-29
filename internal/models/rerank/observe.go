package rerank

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/internal/observe"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

// debugReranker + langfuseReranker previously lived in two separate files
// (llm_debug.go + langfuse_wrapper.go) with ~80% shared shape. They now share
// the observe.Wrap helper. The Reranker interface has one method (Rerank),
// which takes two scalar inputs, so we wrap them in a single struct passed
// through observe.Call.

type rerankCallInput struct {
	query     string
	documents []string
}

type debugReranker struct {
	inner Reranker
}

func (d *debugReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	return runWrapped(ctx, d.inner, query, documents)
}

func (d *debugReranker) GetModelName() string { return d.inner.GetModelName() }
func (d *debugReranker) GetModelID() string   { return d.inner.GetModelID() }

type langfuseReranker struct {
	inner Reranker
}

func (l *langfuseReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	return runWrapped(ctx, l.inner, query, documents)
}

func (l *langfuseReranker) GetModelName() string { return l.inner.GetModelName() }
func (l *langfuseReranker) GetModelID() string   { return l.inner.GetModelID() }

// runWrapped is the shared entry point used by both decorators. observe.Wrap
// is a no-op when the relevant subsystem is disabled, so each decorator only
// activates its own (debug logs for debugReranker, Langfuse generation for
// langfuseReranker via the global manager check). The duplication at wrapper
// level is intentional — matches the pre-refactor wiring where each decorator
// is a separate opt-in layer applied by NewReranker.
func runWrapped(ctx context.Context, inner Reranker, query string, documents []string) ([]RankResult, error) {
	in := rerankCallInput{query: query, documents: documents}
	call := observe.Call[rerankCallInput, []RankResult]{
		Name:    "rerank",
		Model:   inner.GetModelName(),
		ModelID: inner.GetModelID(),
		LangfuseInput: func(v rerankCallInput) any {
			return map[string]any{
				"query":             v.query,
				"document_count":    len(v.documents),
				"documents_preview": previewDocs(v.documents, 5),
			}
		},
		LangfuseMetadata: func(_ rerankCallInput) map[string]any {
			return map[string]any{
				"model_id":    inner.GetModelID(),
				"num_queries": 1,
			}
		},
		LangfuseOutput: func(_ rerankCallInput, results []RankResult, _ error) any {
			return map[string]any{
				"results":     summarizeResults(results, 10),
				"total_count": len(results),
			}
		},
		Usage: func(v rerankCallInput, _ []RankResult) *langfuse.TokenUsage {
			return approxRerankUsage(v.query, v.documents)
		},
		DebugRecord: func(v rerankCallInput, results []RankResult, callErr error, dur time.Duration) *logger.LLMCallRecord {
			return buildRerankDebugRecord(inner.GetModelName(), v.query, v.documents, results, callErr, dur)
		},
	}
	return observe.Wrap(ctx, call, in, func(ctx context.Context, _ rerankCallInput) ([]RankResult, error) {
		return inner.Rerank(ctx, query, documents)
	})
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

// --- helpers (moved from langfuse_wrapper.go / llm_debug.go verbatim) ---

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

func previewDocs(docs []string, n int) []map[string]any {
	n = min(n, len(docs))
	out := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
			"index":   i,
			"preview": truncateRunes(docs[i], 160),
			"length":  len([]rune(docs[i])),
		})
	}
	return out
}

func summarizeResults(results []RankResult, n int) []map[string]any {
	n = min(n, len(results))
	out := make([]map[string]any, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, map[string]any{
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

func buildRerankDebugRecord(model, query string, documents []string, results []RankResult, callErr error, dur time.Duration) *logger.LLMCallRecord {
	record := &logger.LLMCallRecord{
		CallType: "Rerank",
		Model:    model,
		Duration: dur,
	}

	record.Sections = append(record.Sections, logger.RecordSection{Title: "Query", Content: query})

	var docBuf strings.Builder
	fmt.Fprintf(&docBuf, "count=%d\n", len(documents))
	for i, doc := range documents {
		preview := strings.ReplaceAll(doc, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		fmt.Fprintf(&docBuf, "[%d] (len=%d) %s\n", i, len([]rune(doc)), preview)
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Documents", Content: docBuf.String()})

	if results != nil {
		var resBuf strings.Builder
		fmt.Fprintf(&resBuf, "count=%d\n", len(results))
		for _, r := range results {
			docPreview := strings.ReplaceAll(r.Document.Text, "\n", "\\n")
			docPreview = logger.TruncateRunes(docPreview, 200)
			fmt.Fprintf(&resBuf, "  [%d] score=%.6f  %s\n", r.Index, r.RelevanceScore, docPreview)
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Results", Content: resBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	return record
}

// buildRerankRequestDebug produces a single-line log line describing an
// outgoing rerank HTTP request. Used by runner.go at Debug level so operators
// can trace which documents were sent to which endpoint without full body
// dumps. Previously lived in logging.go.
const (
	maxLogDocuments = 3
	maxLogTextRunes = 120
)

func buildRerankRequestDebug(model, endpoint, query string, documents []string) string {
	previews := make([]string, 0, maxLogDocuments)
	for i, doc := range documents {
		if i >= maxLogDocuments {
			break
		}
		previews = append(previews, compactForLog(doc, maxLogTextRunes))
	}

	previewJSON, _ := json.Marshal(previews)
	return fmt.Sprintf(
		"rerank request endpoint=%s model=%s query_preview=%q query_runes=%d documents=%d preview_docs=%s",
		endpoint,
		model,
		compactForLog(query, maxLogTextRunes),
		utf8.RuneCountInString(query),
		len(documents),
		string(previewJSON),
	)
}

func compactForLog(text string, maxRunes int) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if utf8.RuneCountInString(normalized) <= maxRunes {
		return normalized
	}
	return string([]rune(normalized)[:maxRunes]) + "...(truncated)"
}
