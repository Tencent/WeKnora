package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
)

type quotaRetryEmbedder struct {
	embedding.Embedder
	calls int
	err   error
}

func (e *quotaRetryEmbedder) BatchEmbedWithPool(context.Context, embedding.Embedder, []string) ([][]float32, error) {
	e.calls++
	if e.calls == 1 {
		return nil, e.err
	}
	return [][]float32{{1}}, nil
}

func TestBatchBackoffPreservesUnconfiguredQuotaRetry(t *testing.T) {
	embedder := &quotaRetryEmbedder{err: errors.New("HTTP 429 quota")}
	_, err := batchEmbedWithBackoff(context.Background(), embedder, []string{"text"})
	if err != nil || embedder.calls != 2 {
		t.Fatalf("existing retry behavior changed without a token budget: calls=%d err=%v", embedder.calls, err)
	}
}

func TestBatchBackoffDoesNotRestartExhaustedBudget(t *testing.T) {
	err := errors.Join(embedding.ErrBudgetRetriesExhausted, errors.New("HTTP 429 quota"))
	embedder := &quotaRetryEmbedder{err: err}
	_, callErr := batchEmbedWithBackoff(context.Background(), embedder, []string{"text"})
	if callErr == nil || embedder.calls != 1 {
		t.Fatal("outer retries repeated a document after sub-batch quota retries were exhausted")
	}
}
