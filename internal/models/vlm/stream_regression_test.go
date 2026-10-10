package vlm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeStreamChat replays a canned StreamResponse event sequence through
// ChatStream, mirroring what the api stream assembler emits on the wire. It
// lets the regressions below pin PredictStream's behaviour against the exact
// production event shapes without an HTTP round-trip.
type fakeStreamChat struct {
	events []types.StreamResponse
}

func (f *fakeStreamChat) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	return nil, errors.New("fakeStreamChat: buffered Chat not used by these tests")
}

func (f *fakeStreamChat) ChatStream(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (f *fakeStreamChat) GetModelName() string { return "fake-stream" }
func (f *fakeStreamChat) GetModelID() string   { return "fake-stream" }

// newFakeStreamVLM builds a RemoteAPIVLM whose chat client replays events, and
// the managerVLM wrapper around it, so the test exercises the same
// PredictStream -> consumeStream path production uses.
func newFakeStreamVLM(t *testing.T, events []types.StreamResponse) *managerVLM {
	t.Helper()
	v := &RemoteAPIVLM{
		modelName: "fake-stream",
		chat:      &fakeStreamChat{events: events},
	}
	return &managerVLM{
		inner:                  v,
		modelID:                "test-" + t.Name(),
		modelName:              "fake-stream",
		innerSupportsPWO:       true,
		innerSupportsStreaming: true,
	}
}

// TestStreamThinkingIsNotTheAnswer is the regression for the thinking phase:
// the assembler emits reasoning chunks and then closes that phase with a
// {ResponseTypeThinking, Done: true} marker BEFORE any answer content. The
// manager must return the answer, not the reasoning, and must not terminate on
// the thinking-done marker.
func TestStreamThinkingIsNotTheAnswer(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeThinking, Content: "let me reason"},
		{ResponseType: types.ResponseTypeThinking, Content: " about the image"},
		{ResponseType: types.ResponseTypeThinking, Done: true},
		{ResponseType: types.ResponseTypeAnswer, Content: "ACTUAL"},
		{ResponseType: types.ResponseTypeAnswer, Content: " OCR"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: &types.TokenUsage{TotalTokens: 9}},
	})

	text, _, _, err := m.runInner(context.Background(), nil, "p", nil, time.Now())
	if err != nil {
		t.Fatalf("runInner: %v", err)
	}
	if text != "ACTUAL OCR" {
		t.Errorf("answer = %q, want %q (thinking content leaked into the answer)", text, "ACTUAL OCR")
	}
}

// TestStreamIncompleteTerminalIsAnError pins the EndAtEOF combination: the
// assembler can mark a stream complete (Done=true) and truncated
// (FinishReason=incomplete) on the SAME terminal chunk. That chunk must
// surface as an error, never as a successful partial answer.
func TestStreamIncompleteTerminalIsAnError(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "partial answ"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: types.FinishReasonIncomplete},
	})

	_, _, _, err := m.runInner(context.Background(), nil, "p", nil, time.Now())
	if err == nil {
		t.Fatal("incomplete terminal accepted as success; truncated OCR would be recorded as done")
	}
	if !strings.Contains(err.Error(), types.StreamEndedEarlyError) {
		t.Errorf("err = %v, want it to carry %q", err, types.StreamEndedEarlyError)
	}
}

// TestStreamCleanTerminalCarriesUsage keeps the happy path honest: a normal
// answer terminal event still completes as success and its usage survives.
func TestStreamCleanTerminalCarriesUsage(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "done text"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: &types.TokenUsage{TotalTokens: 7}},
	})

	text, usage, _, err := m.runInner(context.Background(), nil, "p", nil, time.Now())
	if err != nil {
		t.Fatalf("runInner: %v", err)
	}
	if text != "done text" {
		t.Errorf("answer = %q, want %q", text, "done text")
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Errorf("usage = %+v, want TotalTokens=7", usage)
	}
}
