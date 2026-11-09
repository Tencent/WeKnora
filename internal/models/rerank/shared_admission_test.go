package rerank

import (
	"context"
	"github.com/Tencent/WeKnora/internal/models/limiter"
	"sync/atomic"
	"testing"
	"time"
)

type admissionFake struct {
	calls   atomic.Int32
	entered chan struct{}
	exit    chan struct{}
}

func (f *admissionFake) GetModelName() string       { return "fake" }
func (f *admissionFake) GetModelID() string         { return "rerank" }
func (f *admissionFake) MaxPassageRunes(string) int { return 128 }
func (f *admissionFake) Rerank(ctx context.Context, _ string, _ []string) ([]RankResult, error) {
	f.calls.Add(1)
	close(f.entered)
	<-f.exit
	return nil, nil
}
func TestRerankCrossUserAdmissionBeforeProvider(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 2, MaxWaiting: 8, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	defer limiter.ConfigureSharedGate(limiter.SharedGateConfig{})
	if err := limiter.RegisterModelEndpoint("rerank", "http://127.0.0.1:8085"); err != nil {
		t.Fatal(err)
	}
	f := &admissionFake{entered: make(chan struct{}), exit: make(chan struct{})}
	w := &concurrencyReranker{inner: f, key: "rerank", limit: 1}
	if MaxPassageRunes(w, "q") != 128 {
		t.Fatal("decorator lost passage limit")
	}
	done := make(chan struct{})
	go func() { w.Rerank(context.Background(), "q", []string{"doc"}); close(done) }()
	<-f.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := w.Rerank(ctx, "other user", []string{"doc"}); err == nil {
		t.Fatal("cancelled waiter admitted")
	}
	if f.calls.Load() != 1 {
		t.Fatal("second user reached provider before admission")
	}
	close(f.exit)
	<-done
}
