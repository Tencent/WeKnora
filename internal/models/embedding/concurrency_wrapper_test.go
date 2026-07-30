package embedding

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
)

// fakeEmbedder blocks every Embed/BatchEmbed call until release is closed and
// records the max number of calls it ever saw in flight simultaneously, so
// tests can assert the governor's per-model concurrency bound.
type fakeEmbedder struct {
	id       string
	pooler   EmbedderPooler // optional: exercises the BatchEmbedWithPool fan-out
	inFlight int32
	maxSeen  int32
	enter    chan struct{} // one signal per call that reaches the provider
	release  chan struct{} // closed to unblock all (current + future) calls
}

func newFakeEmbedder(id string) *fakeEmbedder {
	return &fakeEmbedder{
		id:      id,
		enter:   make(chan struct{}, 256),
		release: make(chan struct{}),
	}
}

func (f *fakeEmbedder) track() {
	n := atomic.AddInt32(&f.inFlight, 1)
	for {
		old := atomic.LoadInt32(&f.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&f.maxSeen, old, n) {
			break
		}
	}
	f.enter <- struct{}{}
	<-f.release
	atomic.AddInt32(&f.inFlight, -1)
}

func (f *fakeEmbedder) Embed(ctx context.Context, _ string) ([]float32, error) {
	f.track()
	return []float32{1}, nil
}

func (f *fakeEmbedder) BatchEmbed(ctx context.Context, _ []string) ([][]float32, error) {
	f.track()
	return [][]float32{{1}}, nil
}

func (f *fakeEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	if f.pooler != nil {
		return f.pooler.BatchEmbedWithPool(ctx, model, texts)
	}
	return model.BatchEmbed(ctx, texts)
}

func (f *fakeEmbedder) GetModelName() string { return f.id }
func (f *fakeEmbedder) GetDimensions() int   { return 1 }
func (f *fakeEmbedder) GetModelID() string   { return f.id }

// TestConcurrencyEmbedderBackgroundGated verifies background BatchEmbed calls
// are capped at the per-model limit.
func TestConcurrencyEmbedderBackgroundGated(t *testing.T) {
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	limiter.SetGovernor(limiter.NewLocalLimiter(), 2)

	f := newFakeEmbedder("emb-bg")
	w := wrapEmbeddingConcurrency(f, 0)

	ctx := types.WithBackgroundTask(context.Background())
	const n = 5
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.BatchEmbed(ctx, []string{"x"})
		}()
	}

	// With limit=2 exactly two calls may reach the provider concurrently.
	for i := range 2 {
		select {
		case <-f.enter:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected call %d to enter, inFlight=%d", i, atomic.LoadInt32(&f.inFlight))
		}
	}
	// A third must not sneak in while both slots are held.
	select {
	case <-f.enter:
		t.Fatal("a third call entered while limit=2 slots were held")
	case <-time.After(150 * time.Millisecond):
	}

	close(f.release)
	wg.Wait()
	if got := atomic.LoadInt32(&f.maxSeen); got > 2 {
		t.Fatalf("max in-flight %d exceeded limit 2", got)
	}
}

// TestConcurrencyEmbedderPerModelLimitOverridesDefault verifies a model's own
// configured limit takes precedence over the process-wide default.
func TestConcurrencyEmbedderPerModelLimitOverridesDefault(t *testing.T) {
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	// Global default is generous (10), but this model is pinned to 1.
	limiter.SetGovernor(limiter.NewLocalLimiter(), 10)

	f := newFakeEmbedder("emb-permodel")
	w := wrapEmbeddingConcurrency(f, 1)

	ctx := types.WithBackgroundTask(context.Background())
	const n = 3
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.BatchEmbed(ctx, []string{"x"})
		}()
	}

	// Only one may be in flight because the per-model limit is 1.
	select {
	case <-f.enter:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected one call to enter, inFlight=%d", atomic.LoadInt32(&f.inFlight))
	}
	select {
	case <-f.enter:
		t.Fatal("a second call entered while per-model limit=1 slot was held")
	case <-time.After(150 * time.Millisecond):
	}

	close(f.release)
	wg.Wait()
	if got := atomic.LoadInt32(&f.maxSeen); got > 1 {
		t.Fatalf("max in-flight %d exceeded per-model limit 1", got)
	}
}

// TestConcurrencyEmbedderInteractiveNotGated verifies interactive calls bypass
// the governor entirely, even at limit 1.
func TestConcurrencyEmbedderInteractiveNotGated(t *testing.T) {
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	limiter.SetGovernor(limiter.NewLocalLimiter(), 1)

	f := newFakeEmbedder("emb-interactive")
	w := wrapEmbeddingConcurrency(f, 0)

	ctx := context.Background() // no background marker
	const n = 3
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.Embed(ctx, "x")
		}()
	}
	// All three must be able to run at once despite limit=1.
	for i := range n {
		select {
		case <-f.enter:
		case <-time.After(2 * time.Second):
			t.Fatalf("interactive call %d did not enter (should be ungated), inFlight=%d",
				i, atomic.LoadInt32(&f.inFlight))
		}
	}
	close(f.release)
	wg.Wait()
}

// TestConcurrencyEmbedderPoolFanOutGated verifies that BatchEmbedWithPool's
// per-sub-batch provider round-trips are individually gated — the reason the
// wrapper sits innermost.
func TestConcurrencyEmbedderPoolFanOutGated(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1") // one provider round-trip per text
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	limiter.SetGovernor(limiter.NewLocalLimiter(), 2)

	pool, err := ants.NewPool(16)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	defer pool.Release()

	f := newFakeEmbedder("emb-pool")
	f.pooler = NewBatchEmbedder(pool)
	w := wrapEmbeddingConcurrency(f, 0)

	ctx := types.WithBackgroundTask(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = w.BatchEmbedWithPool(ctx, w, []string{"a", "b", "c", "d", "e"})
	}()

	for i := range 2 {
		select {
		case <-f.enter:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected sub-batch %d to enter, inFlight=%d", i, atomic.LoadInt32(&f.inFlight))
		}
	}
	select {
	case <-f.enter:
		t.Fatal("a third sub-batch entered while limit=2 slots were held")
	case <-time.After(150 * time.Millisecond):
	}

	close(f.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("BatchEmbedWithPool did not complete after release")
	}
	if got := atomic.LoadInt32(&f.maxSeen); got > 2 {
		t.Fatalf("max in-flight sub-batches %d exceeded limit 2", got)
	}
}
