package rerank

import (
	"context"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"time"
)

// This lease spans all batches in a user request. Batch concurrency alone
// cannot bound different users or endpoints sharing the same resource.
type concurrencyReranker struct {
	inner Reranker
	key   string
	limit int
}

func (w *concurrencyReranker) GetModelName() string         { return w.inner.GetModelName() }
func (w *concurrencyReranker) GetModelID() string           { return w.inner.GetModelID() }
func (w *concurrencyReranker) MaxPassageRunes(q string) int { return MaxPassageRunes(w.inner, q) }
func (w *concurrencyReranker) Rerank(ctx context.Context, q string, docs []string) ([]RankResult, error) {
	start := time.Now()
	release, err := limiter.AcquireCall(ctx, w.key, w.GetModelName(), w.limit)
	logger.Infof(ctx, "[MODEL ADMISSION] kind=rerank wait_ms=%d admitted=%t", time.Since(start).Milliseconds(), err == nil)
	if err != nil {
		return nil, err
	}
	start = time.Now()
	out, err := w.inner.Rerank(ctx, q, docs)
	logger.Infof(ctx, "[MODEL EXECUTION] kind=rerank execution_ms=%d failed=%t", time.Since(start).Milliseconds(), err != nil)
	release()
	return out, err
}
