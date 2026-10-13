package embedding

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjf2000/ants/v2"
)

type poolTestEmbedder struct {
	Embedder
	send func(context.Context, []string) ([][]float32, error)
}

func (e *poolTestEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
	return e.send(ctx, texts)
}

func TestBatchPoolRejectsInvalidBatchSize(t *testing.T) {
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	for _, size := range []string{"0", "-1", "bad"} {
		t.Run(size, func(t *testing.T) {
			t.Setenv("BATCH_EMBED_SIZE", size)
			_, err := NewBatchEmbedder(pool).BatchEmbedWithPool(context.Background(), nil, []string{"a"})
			if err == nil {
				t.Fatal("accepted invalid batch size")
			}
		})
	}
}

func TestBatchPoolCancellationWaitsForCallbacks(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	t.Setenv("EMBED_BATCH_INTERVAL_MS", "60000")
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var completed atomic.Bool
	e := &poolTestEmbedder{send: func(ctx context.Context, _ []string) ([][]float32, error) {
		cancel()
		completed.Store(true)
		return nil, ctx.Err()
	}}
	_, err = NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, e, []string{"a", "b"})
	if !errors.Is(err, context.Canceled) || !completed.Load() {
		t.Fatalf("callback left running: completed=%v err=%v", completed.Load(), err)
	}
}

func TestBatchPoolConcurrentErrors(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	t.Setenv("EMBED_BATCH_INTERVAL_MS", "0")
	pool, err := ants.NewPool(8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	wantErr := errors.New("provider error")
	e := &poolTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
		return nil, wantErr
	}}
	_, err = NewBatchEmbedder(pool).BatchEmbedWithPool(context.Background(), e, make([]string, 100))
	if !errors.Is(err, wantErr) {
		t.Fatalf("provider error lost: %v", err)
	}
}

func TestBatchPoolOverflowingIntervalFallsBackToUnpaced(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	// This value would wrap to an enormous positive wait without a bound check.
	ms := 3 * int64((1<<63-1)/time.Millisecond)
	t.Setenv("EMBED_BATCH_INTERVAL_MS", strconv.FormatInt(ms, 10))
	pool, err := ants.NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	e := &poolTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
		return [][]float32{{1}}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	vectors, err := NewBatchEmbedder(pool).BatchEmbedWithPool(ctx, e, []string{"a", "b"})
	if err != nil || len(vectors) != 2 {
		t.Fatalf("overflowed interval did not fall back: vectors=%v err=%v", vectors, err)
	}
}

func TestBatchPoolSubmissionFailureWaitsForCallbacks(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	t.Setenv("EMBED_BATCH_INTERVAL_MS", "0")
	pool, err := ants.NewPool(1, ants.WithNonblocking(true))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Release()
	entered := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	e := &poolTestEmbedder{send: func(context.Context, []string) ([][]float32, error) {
		close(entered)
		<-release
		close(completed)
		return [][]float32{{1}}, nil
	}}
	result := make(chan error, 1)
	go func() {
		_, callErr := NewBatchEmbedder(pool).BatchEmbedWithPool(context.Background(), e, []string{"a", "b"})
		result <- callErr
	}()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first callback did not start")
	}
	select {
	case err := <-result:
		t.Fatalf("returned while callback was still running: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-result:
		if !errors.Is(err, ants.ErrPoolOverload) {
			t.Fatalf("submission error lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("call did not return after callback completion")
	}
	select {
	case <-completed:
	default:
		t.Fatal("accepted callback outlived the call")
	}
}
