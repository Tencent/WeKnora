package rerank

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// debugReranker wraps a Reranker with LLM debug logging.
type debugReranker struct {
	inner Reranker
}

func (d *debugReranker) Rerank(ctx context.Context, query string, documents []string) ([]RankResult, error) {
	start := time.Now()
	result, err := d.inner.Rerank(ctx, query, documents)
	logRerankDebug(ctx, d.inner.GetModelName(), query, documents, result, err, time.Since(start))
	return result, err
}

func (d *debugReranker) GetModelName() string { return d.inner.GetModelName() }
func (d *debugReranker) GetModelID() string   { return d.inner.GetModelID() }

func logRerankDebug(ctx context.Context, model string, query string, documents []string, results []RankResult, callErr error, dur time.Duration) {
	if !logger.LLMDebugEnabled() {
		return
	}

	record := &logger.LLMCallRecord{
		CallType: "Rerank",
		Model:    model,
		Duration: dur,
	}

	// Query section
	record.Sections = append(record.Sections, logger.RecordSection{
		Title:   "Query",
		Content: query,
	})

	// Documents section
	var docBuf strings.Builder
	docBuf.WriteString(fmt.Sprintf("count=%d\n", len(documents)))
	for i, doc := range documents {
		preview := strings.ReplaceAll(doc, "\n", "\\n")
		preview = logger.TruncateRunes(preview, 200)
		docBuf.WriteString(fmt.Sprintf("[%d] (len=%d) %s\n", i, len([]rune(doc)), preview))
	}
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Documents", Content: docBuf.String()})

	// Results section
	if results != nil {
		var resBuf strings.Builder
		resBuf.WriteString(fmt.Sprintf("count=%d\n", len(results)))
		for _, r := range results {
			docPreview := strings.ReplaceAll(r.Document.Text, "\n", "\\n")
			docPreview = logger.TruncateRunes(docPreview, 200)
			resBuf.WriteString(fmt.Sprintf("  [%d] score=%.6f  %s\n", r.Index, r.RelevanceScore, docPreview))
		}
		record.Sections = append(record.Sections, logger.RecordSection{Title: "Results", Content: resBuf.String()})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}
