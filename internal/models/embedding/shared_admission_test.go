package embedding

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
)

func TestSharedAdmissionEmbeddingRejectsBeforeProvider(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 0, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{}); err != nil {
			t.Error(err)
		}
	})
	f := newFakeEmbedder("admission-embed")
	close(f.release)
	w := wrapEmbeddingConcurrency(f, 0)
	if _, err := w.Embed(context.Background(), "text"); !errors.Is(err, limiter.ErrSharedModelUnregistered) {
		t.Fatalf("unregistered: %v", err)
	}
	if err := limiter.RegisterModelEndpoint(f.GetModelID(), "http://127.0.0.1:8084/v1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.BatchEmbed(ctx, []string{"text"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if atomic.LoadInt32(&f.maxSeen) != 0 {
		t.Fatal("rejected request reached embedding")
	}
	if _, err := w.Embed(context.Background(), "text"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&f.maxSeen) != 1 || limiter.SharedRuntimeStats().Active != 0 {
		t.Fatal("embedding call lease not released")
	}
}
