package chat

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

type countedAdmissionChat struct{ calls atomic.Int32 }

func (f *countedAdmissionChat) GetModelID() string   { return "admission-chat" }
func (f *countedAdmissionChat) GetModelName() string { return "probe" }
func (f *countedAdmissionChat) Chat(context.Context, []Message, *ChatOptions) (*types.ChatResponse, error) {
	f.calls.Add(1)
	return &types.ChatResponse{}, nil
}
func (f *countedAdmissionChat) ChatStream(context.Context, []Message, *ChatOptions) (<-chan types.StreamResponse, error) {
	f.calls.Add(1)
	ch := make(chan types.StreamResponse)
	close(ch)
	return ch, nil
}
func TestSharedAdmissionChatRejectsBeforeProvider(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 0, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{}); err != nil {
			t.Error(err)
		}
	})
	f := &countedAdmissionChat{}
	w := &concurrencyChat{inner: f}
	if _, err := w.Chat(context.Background(), nil, nil); !errors.Is(err, limiter.ErrSharedModelUnregistered) {
		t.Fatalf("unregistered provider reached: %v", err)
	}
	if err := limiter.RegisterModelEndpoint(f.GetModelID(), "http://127.0.0.1:8083/v1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Chat(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled nonstream: %v", err)
	}
	if _, err := w.ChatStream(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream: %v", err)
	}
	release, err := limiter.AcquireCall(context.Background(), f.GetModelID(), f.GetModelName(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Chat(context.Background(), nil, nil); !errors.Is(err, limiter.ErrSharedQueueFull) {
		release()
		t.Fatalf("full queue: %v", err)
	}
	release()
	if f.calls.Load() != 0 {
		t.Fatal("rejected request called provider")
	}
	if _, err := w.Chat(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 || limiter.SharedRuntimeStats().Active != 0 {
		t.Fatal("successful provider did not release")
	}
}

type delayedAdmissionStream struct{ finish chan struct{} }

func (f *delayedAdmissionStream) GetModelID() string   { return "delayed-stream" }
func (f *delayedAdmissionStream) GetModelName() string { return "delayed-stream" }
func (f *delayedAdmissionStream) Chat(context.Context, []Message, *ChatOptions) (*types.ChatResponse, error) {
	return &types.ChatResponse{}, nil
}
func (f *delayedAdmissionStream) ChatStream(context.Context, []Message, *ChatOptions) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, 2)
	ch <- types.StreamResponse{}
	ch <- types.StreamResponse{}
	go func() { <-f.finish; close(ch) }()
	return ch, nil
}
func TestSharedAdmissionStreamHoldsUntilProducerEnds(t *testing.T) {
	if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{Enabled: true, Concurrency: 1, MaxWaiting: 0, InteractiveBurst: 3, MaxWait: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := limiter.ConfigureSharedGate(limiter.SharedGateConfig{}); err != nil {
			t.Error(err)
		}
	})
	f := &delayedAdmissionStream{finish: make(chan struct{})}
	if err := limiter.RegisterModelEndpoint(f.GetModelID(), "http://127.0.0.1:8083/v1"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out, err := (&concurrencyChat{inner: f}).ChatStream(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	<-out
	cancel()
	select {
	case _, ok := <-out:
		if !ok {
			close(f.finish)
			t.Fatal("admission released before provider termination")
		}
		// A send already selected concurrently with cancellation may complete.
		// The invariant is that provider admission stays occupied until it ends.
		select {
		case _, ok := <-out:
			if !ok {
				close(f.finish)
				t.Fatal("admission released while producer lives")
			}
		case <-time.After(50 * time.Millisecond):
		}
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := limiter.AcquireCall(context.Background(), f.GetModelID(), f.GetModelName(), 0); !errors.Is(err, limiter.ErrSharedQueueFull) {
		close(f.finish)
		t.Fatalf("live producer budget escaped: %v", err)
	}
	close(f.finish)
	select {
	case <-out:
	case <-time.After(time.Second):
		t.Fatal("terminated producer did not release stream")
	}
	if limiter.SharedRuntimeStats().Active != 0 {
		t.Fatal("terminated producer leaked admission")
	}
}
