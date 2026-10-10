package embedding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTokenBudgetSharedAcrossModelRecords(t *testing.T) {
	t.Setenv("WEKNORA_EMBEDDING_TPM", "60000")
	c := Config{
		Source: "remote", BaseURL: "https://example.test", APIKey: t.Name(), ModelName: "embedding", ModelID: "first",
	}
	a := embeddingTokenBudget(c)
	c.ModelID = "second"
	b := embeddingTokenBudget(c)
	if a != b {
		t.Fatal("same account/model has independent budgets")
	}
	c.APIKey += "different"
	if a != embeddingTokenBudget(c) {
		t.Fatal("keys at the same provider/model bypass the global budget")
	}
	c.BaseURL = "https://different.test"
	if a == embeddingTokenBudget(c) {
		t.Fatal("unrelated providers share a budget")
	}
}

func TestTokenBudgetPacesConcurrentDocuments(t *testing.T) {
	b := &tokenBudget{slot: make(chan struct{}, 1), tpm: 36, window: 20 * time.Millisecond}
	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := b.call(context.Background(), []string{"庄子庄子庄子"}, func() ([][]float32, error) {
				mu.Lock()
				starts = append(starts, time.Now())
				mu.Unlock()
				return nil, nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 18*time.Millisecond {
			t.Fatal("token budget burst across documents")
		}
	}
}

func TestTokenBudgetRateLimitWaitsAndRetriesOnlyFailedCall(t *testing.T) {
	b := &tokenBudget{slot: make(chan struct{}, 1), tpm: 60000, window: 20 * time.Millisecond}
	var calls int
	var first time.Time
	_, err := b.call(context.Background(), []string{"测试"}, func() ([][]float32, error) {
		calls++
		if calls == 1 {
			first = time.Now()
			return nil, errors.New("status 429 insufficient_quota")
		}
		if time.Since(first) < 18*time.Millisecond {
			t.Fatal("quota recovery window was skipped")
		}
		return [][]float32{{1}}, nil
	})
	if err != nil || calls != 2 || b.tpm != 30000 {
		t.Fatalf("calls=%d tpm=%d err=%v", calls, b.tpm, err)
	}
}

func TestTokenBudgetCancellationAndPermanentFailure(t *testing.T) {
	b := &tokenBudget{slot: make(chan struct{}, 1), tpm: 60000, window: time.Minute, next: time.Now().Add(time.Minute)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	_, err := b.call(ctx, nil, func() ([][]float32, error) { called.Store(true); return nil, nil })
	if !errors.Is(err, context.Canceled) || called.Load() {
		t.Fatal("cancelled request reached provider")
	}
	b.next = time.Time{}
	calls := 0
	_, err = b.call(context.Background(), nil, func() ([][]float32, error) {
		calls++
		return nil, errors.New("status 401 unauthorized")
	})
	if calls != 1 || err == nil {
		t.Fatal("permanent error retried")
	}
}

func TestTokenBudgetDisabledWithoutConfiguredQuota(t *testing.T) {
	t.Setenv("WEKNORA_EMBEDDING_TPM", "")
	if embeddingTokenBudget(Config{Source: "remote", BaseURL: "https://unconfigured.test", ModelName: "test"}) != nil {
		t.Fatal("budget changed unconfigured throughput")
	}
}

func TestTokenBudgetExhaustionPreservesProviderError(t *testing.T) {
	budget := &tokenBudget{slot: make(chan struct{}, 1), tpm: 60000, window: time.Millisecond}
	providerErr := errors.New("HTTP 429 quota")
	calls := 0
	_, err := budget.call(context.Background(), nil, func() ([][]float32, error) { calls++; return nil, providerErr })
	if calls != 5 || !errors.Is(err, ErrBudgetRetriesExhausted) || !errors.Is(err, providerErr) {
		t.Fatalf("quota exhaustion lost its retry state: calls=%d err=%v", calls, err)
	}
}

func TestTokenBudgetReductionDoesNotRecoverOnSuccess(t *testing.T) {
	budget := &tokenBudget{slot: make(chan struct{}, 1), tpm: 60000, window: time.Millisecond}
	calls := 0
	provider := func() ([][]float32, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("HTTP 429 quota")
		}
		return [][]float32{{1}}, nil
	}
	if _, err := budget.call(context.Background(), []string{"chunk"}, provider); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 5; n++ {
		if _, err := budget.call(context.Background(), []string{"chunk"}, provider); err != nil {
			t.Fatal(err)
		}
	}
	if budget.tpm != 30000 {
		t.Fatalf("unexpected automatic budget recovery: %d", budget.tpm)
	}
}
