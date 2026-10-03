package api

import (
	"context"
	"fmt"
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

// Tool-call indices belong to the vendor: gateways number from 1 and parallel
// calls arrive with gaps. Walking 0..len(map) dropped every call outside that
// range, and a round whose calls vanish reaches the agent as a plain answer.
func TestStreamAssembler_OrderedToolCallsNonContiguousIndices(t *testing.T) {
	cases := []struct {
		name    string
		indices []int
		want    []string
	}{
		{name: "zero based", indices: []int{0, 1}, want: []string{"f0", "f1"}},
		{name: "one based", indices: []int{1, 2}, want: []string{"f1", "f2"}},
		{name: "gapped", indices: []int{0, 3, 7}, want: []string{"f0", "f3", "f7"}},
		{name: "out of order arrival", indices: []int{5, 2}, want: []string{"f2", "f5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewStreamAssembler(context.Background(), "m")
			ch := make(chan types.StreamResponse, 64)
			for _, idx := range tc.indices {
				a.Process(ch, Delta{ToolCalls: []ToolCallDelta{{
					Index:     idx,
					ID:        fmt.Sprintf("call_%d", idx),
					Name:      fmt.Sprintf("f%d", idx),
					Arguments: `{}`,
				}}})
			}

			calls := a.OrderedToolCalls()
			if len(calls) != len(tc.want) {
				t.Fatalf("got %d calls %#v, want %d", len(calls), calls, len(tc.want))
			}
			for i, name := range tc.want {
				if calls[i].Function.Name != name {
					t.Fatalf("call %d = %q, want %q (order must follow the vendor index)",
						i, calls[i].Function.Name, name)
				}
			}
		})
	}
}

// Metadata-only entries (a thought signature that arrives before its call)
// are kept too, whatever index the vendor gave them.
func TestStreamAssembler_OrderedToolCallsIncludesMetadataOnlyIndex(t *testing.T) {
	a := NewStreamAssembler(context.Background(), "m")
	a.SetToolCallMetadata(4, types.ToolCallMetadata{"thought_signature": []byte(`"sig"`)})

	calls := a.OrderedToolCalls()
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].ProviderMetadata["thought_signature"] == nil {
		t.Fatal("provider metadata lost")
	}
}
