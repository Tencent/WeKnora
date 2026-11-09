package embedding

import (
	"context"
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
	"time"
)

type budgetEmbedder struct {
	Embedder
	deadline chan time.Duration
}

func (f *budgetEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	d, ok := ctx.Deadline()
	if !ok {
		f.deadline <- 0
	} else {
		f.deadline <- time.Until(d)
	}
	return nil, ctx.Err()
}
func TestExecutionBudgetStartsAfterAdmission(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 2, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	defer limiter.ConfigureSharedGate(limiter.SharedGateConfig{})
	if err := limiter.RegisterModelEndpoint("budget", "http://127.0.0.1:8084"); err != nil {
		t.Fatal(err)
	}
	held, err := limiter.AcquireCall(context.Background(), "budget", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	f := &budgetEmbedder{Embedder: newFakeEmbedder("budget"), deadline: make(chan time.Duration, 1)}
	w := wrapEmbeddingConcurrency(f, 1)
	done := make(chan error, 1)
	go func() {
		_, err := w.Embed(types.WithModelExecutionBudget(context.Background(), 20*time.Millisecond), "q")
		done <- err
	}()
	time.Sleep(40 * time.Millisecond)
	held()
	if err := <-done; err != nil {
		t.Fatal("queue consumed execution budget", err)
	}
	if remaining := <-f.deadline; remaining <= 0 || remaining > 20*time.Millisecond {
		t.Fatalf("execution deadline not started at grant: %v", remaining)
	}
}
