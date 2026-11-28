package embedding

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

// flakyEmbedder fails the first sub-batch it is given and succeeds afterwards,
// so the pool has a failure to record while other sub-batches are still queued.
type flakyEmbedder struct {
	calls atomic.Int32
}

func (f *flakyEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, errProviderDown
}

func (f *flakyEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	if f.calls.Add(1) == 1 {
		return nil, errProviderDown
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1}
	}
	return out, nil
}

func (f *flakyEmbedder) GetModelName() string { return "flaky" }
func (f *flakyEmbedder) GetDimensions() int   { return 1 }
func (f *flakyEmbedder) GetModelID() string   { return "flaky" }

// BatchEmbedWithPool satisfies EmbedderPooler; the pool under test is the one
// passed explicitly to it, so this is never the path the tests exercise.
func (f *flakyEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return model.BatchEmbed(ctx, texts)
}

var errProviderDown = errors.New("provider down")

// BatchEmbedWithPool fans a batch out over the ants pool, so with
// BATCH_EMBED_SIZE=1 every text becomes its own sub-batch and several workers
// run at once. When one of them fails, the remaining queued sub-batches read
// firstErr to decide whether to skip — a decision every worker makes
// concurrently with other workers' writes to that same variable.
//
// That read is only observable under the race detector: whether a particular
// worker happens to observe the write depends on scheduling, so no assertion on
// call counts can pin it without being flaky. Run this with -race:
//
//	go test -race ./internal/models/embedding/ -run TestBatchEmbedWithPoolErrorPath
//
// Before the read was taken under mu, the detector reported a race between
// batch.go's guard and the write in the failure branch.
func TestBatchEmbedWithPoolErrorPath(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")

	pool, err := ants.NewPool(8)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Release)
	pooler := NewBatchEmbedder(pool)

	// Repeat so the workers overlap in many different interleavings rather than
	// relying on one lucky schedule.
	for round := range 20 {
		texts := make([]string, 64)
		for i := range texts {
			texts[i] = fmt.Sprintf("round-%d-chunk-%d", round, i)
		}

		model := &flakyEmbedder{}
		got, err := pooler.BatchEmbedWithPool(context.Background(), model, texts)
		if !errors.Is(err, errProviderDown) {
			t.Fatalf("round %d: err = %v, want %v", round, err, errProviderDown)
		}
		if got != nil {
			t.Fatalf("round %d: results = %v, want none alongside an error", round, got)
		}
	}
}

// A provider that returns fewer vectors than inputs is the second failure mode
// the pool records. It writes firstErr from the same branch, so it guards
// against the same regression.
func TestBatchEmbedWithPoolLengthMismatchPath(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")

	pool, err := ants.NewPool(8)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Release)
	pooler := NewBatchEmbedder(pool)

	for round := range 20 {
		texts := make([]string, 64)
		for i := range texts {
			texts[i] = fmt.Sprintf("round-%d-chunk-%d", round, i)
		}

		model := &shortEmbedder{}
		got, err := pooler.BatchEmbedWithPool(context.Background(), model, texts)
		if err == nil {
			t.Fatalf("round %d: err = nil, want a length-mismatch error", round)
		}
		if got != nil {
			t.Fatalf("round %d: results = %v, want none alongside an error", round, got)
		}
	}
}

// shortEmbedder always answers with one vector fewer than it was given.
type shortEmbedder struct{}

func (shortEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, fmt.Errorf("unexpected Embed call")
}

func (shortEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1}
	}
	return out[:len(out)-1], nil
}

func (shortEmbedder) GetModelName() string { return "short" }
func (shortEmbedder) GetDimensions() int   { return 1 }
func (shortEmbedder) GetModelID() string   { return "short" }

// BatchEmbedWithPool satisfies EmbedderPooler; see flakyEmbedder.
func (shortEmbedder) BatchEmbedWithPool(
	ctx context.Context, model Embedder, texts []string,
) ([][]float32, error) {
	return model.BatchEmbed(ctx, texts)
}

type overlappingBatchEmbedder struct {
	Embedder
	firstErr    error
	peerStarted chan struct{}
	peerResult  chan error
}

func (e *overlappingBatchEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	if texts[0] == "fail" {
		select {
		case <-e.peerStarted:
			return nil, e.firstErr
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	close(e.peerStarted)
	<-ctx.Done()
	e.peerResult <- ctx.Err()
	return nil, ctx.Err()
}

func TestBatchEmbedWithPoolCancelsInFlightSibling(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	for _, tc := range []struct {
		name        string
		firstErr    error
		wantMessage string
	}{
		{name: "provider error", firstErr: errProviderDown},
		{name: "result count mismatch", wantMessage: "embedding model returned 0 embeddings for 1 inputs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := ants.NewPool(2)
			if err != nil {
				t.Fatalf("new pool: %v", err)
			}
			t.Cleanup(pool.Release)

			// A deadline only keeps a broken implementation from hanging; the
			// peer must observe cancellation, not this deadline expiring.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			model := &overlappingBatchEmbedder{
				firstErr:    tc.firstErr,
				peerStarted: make(chan struct{}),
				peerResult:  make(chan error, 1),
			}
			got, err := NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, model, []string{"fail", "sibling"})
			if got != nil {
				t.Fatalf("results = %v, want none alongside an error", got)
			}
			if tc.firstErr != nil {
				if !errors.Is(err, tc.firstErr) {
					t.Fatalf("err = %v, want %v", err, tc.firstErr)
				}
			} else if err == nil || err.Error() != tc.wantMessage {
				t.Fatalf("err = %v, want %q", err, tc.wantMessage)
			}
			select {
			case err := <-model.peerResult:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("in-flight sibling error = %v, want context.Canceled", err)
				}
			default:
				t.Fatal("returned before the in-flight sibling observed cancellation")
			}
			if ctx.Err() != nil {
				t.Fatalf("caller context was canceled: %v", ctx.Err())
			}
		})
	}
}
