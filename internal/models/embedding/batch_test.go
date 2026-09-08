package embedding

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

// stubModel is a minimal Embedder that records BatchEmbed calls and can
// fail a configurable prefix of each wave so we exercise partial retry.
type stubModel struct {
	// failFirstN fails the first N BatchEmbed calls (global counter).
	failFirstN int32
	calls      atomic.Int32
	// textsSeen accumulates every text successfully embedded.
	successTexts []string
}

func (m *stubModel) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (m *stubModel) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	n := m.calls.Add(1)
	if int(n) <= int(m.failFirstN) {
		return nil, &RateLimitError{
			StatusCode: 429,
			RetryAfter: 10 * time.Millisecond,
			Body:       "rate limited",
		}
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = []float32{float32(len(text))}
		m.successTexts = append(m.successTexts, text)
	}
	return out, nil
}

func (m *stubModel) BatchEmbedWithPool(ctx context.Context, model Embedder, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("not used")
}
func (m *stubModel) GetModelName() string { return "stub" }
func (m *stubModel) GetDimensions() int   { return 1 }
func (m *stubModel) GetModelID() string   { return "stub-id" }

func TestBatchEmbedWithPoolRetriesOnlyFailedSubBatches(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "2")
	t.Setenv("EMBED_BATCH_MAX_RETRIES", "3")
	t.Setenv("EMBED_REQUEST_INTERVAL_MS", "0")

	pool, err := ants.NewPool(4)
	if err != nil {
		t.Fatalf("ants.NewPool: %v", err)
	}
	defer pool.Release()

	// 6 texts → 3 sub-batches of size 2. Fail the first 2 BatchEmbed
	// calls (first wave of 3 jobs: 2 fail, 1 succeeds). Second wave
	// retries only the 2 failures and should succeed without re-calling
	// the already-successful sub-batch.
	model := &stubModel{failFirstN: 2}
	pooler := NewBatchEmbedder(pool)

	texts := []string{"a", "bb", "ccc", "dddd", "eeeee", "ffffff"}
	got, err := pooler.BatchEmbedWithPool(context.Background(), model, texts)
	if err != nil {
		t.Fatalf("BatchEmbedWithPool: %v", err)
	}
	if len(got) != len(texts) {
		t.Fatalf("len(results)=%d, want %d", len(got), len(texts))
	}
	for i, text := range texts {
		if got[i] == nil || got[i][0] != float32(len(text)) {
			t.Fatalf("result[%d]=%v, want vector with len(text)=%d", i, got[i], len(text))
		}
	}

	// 3 calls in wave 1 + 2 retries = 5. Must NOT be 3+3=6 (whole re-run).
	if calls := model.calls.Load(); calls != 5 {
		t.Fatalf("BatchEmbed calls = %d, want 5 (3 first wave + 2 retries only)", calls)
	}
}

func TestBatchEmbedWithPoolHonoursRequestInterval(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	t.Setenv("EMBED_BATCH_MAX_RETRIES", "0")
	t.Setenv("EMBED_REQUEST_INTERVAL_MS", "30")

	pool, err := ants.NewPool(4)
	if err != nil {
		t.Fatalf("ants.NewPool: %v", err)
	}
	defer pool.Release()

	model := &stubModel{}
	pooler := NewBatchEmbedder(pool)

	start := time.Now()
	// 4 single-text batches with 30ms spacing → at least ~90ms total.
	if _, err := pooler.BatchEmbedWithPool(context.Background(), model, []string{"a", "b", "c", "d"}); err != nil {
		t.Fatalf("BatchEmbedWithPool: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 80*time.Millisecond {
		t.Fatalf("elapsed %v, want >= 80ms with EMBED_REQUEST_INTERVAL_MS=30", elapsed)
	}
}

func TestBatchEmbedWithPoolEmptyInput(t *testing.T) {
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatalf("ants.NewPool: %v", err)
	}
	defer pool.Release()
	pooler := NewBatchEmbedder(pool)
	got, err := pooler.BatchEmbedWithPool(context.Background(), &stubModel{}, nil)
	if err != nil {
		t.Fatalf("empty input err = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want empty slice", got)
	}
}
