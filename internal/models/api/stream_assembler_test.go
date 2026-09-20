package api

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// A consumer that walks away must not pin the producing goroutine forever.
// Before the cancellation-aware send this blocked on `ch <-` for the life of
// the process, leaking the goroutine, the HTTP body and the timeout context.
func TestStreamAssembler_AbandonedConsumerDoesNotBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	a := NewStreamAssembler(ctx, "m")

	ch := make(chan types.StreamResponse) // unbuffered, never read
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Process(ch, Delta{Content: "hello"})
		a.Process(ch, Delta{FinishReason: "stop"})
		a.End(ch)
	}()

	// Let the producer park on the first send, then drop the consumer.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("producer still blocked after the consumer went away")
	}
	if !a.Aborted() {
		t.Fatal("Aborted() = false, want true after an abandoned send")
	}
}

// An already-cancelled context must abort deterministically rather than
// racing a ready channel.
func TestStreamAssembler_CancelledContextAbortsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := NewStreamAssembler(ctx, "m")

	ch := make(chan types.StreamResponse, 8)
	a.Process(ch, Delta{Content: "hello"})
	a.End(ch)

	if len(ch) != 0 {
		t.Fatalf("emitted %d chunks into an abandoned stream, want 0", len(ch))
	}
	if !a.Aborted() {
		t.Fatal("Aborted() = false, want true")
	}
}

// The live path must keep the exact chunk sequence the engine expects:
// thinking chunks, one thinking-done marker before the first answer token,
// the answer, then the closing chunk.
func TestStreamAssembler_ThinkingHandoffSequence(t *testing.T) {
	a := NewStreamAssembler(context.Background(), "m")
	ch := make(chan types.StreamResponse, 16)

	a.Process(ch, Delta{Reasoning: "think"})
	a.Process(ch, Delta{Content: "answer", FinishReason: "stop"})
	a.End(ch)
	close(ch)

	type step struct {
		kind types.ResponseType
		done bool
		text string
	}
	var got []step
	for chunk := range ch {
		got = append(got, step{chunk.ResponseType, chunk.Done, chunk.Content})
	}
	want := []step{
		{types.ResponseTypeThinking, false, "think"},
		{types.ResponseTypeThinking, true, ""},
		{types.ResponseTypeAnswer, true, "answer"},
		{types.ResponseTypeAnswer, true, ""},
	}
	if len(got) != len(want) {
		t.Fatalf("sequence = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
