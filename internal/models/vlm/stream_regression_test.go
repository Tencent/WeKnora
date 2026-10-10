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
	_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	return nil, errors.New("fakeStreamChat: buffered Chat not used by these tests")
}

func (f *fakeStreamChat) ChatStream(
	_ context.Context, _ []chat.Message, _ *chat.ChatOptions,
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
// the thinking-done marker. TTFT anchoring: the reasoning FIRST token is real
// server progress, so PhaseFirstToken must fire during the reasoning phase —
// before any answer content — while OnChunk still never sees reasoning text.
func TestStreamThinkingIsNotTheAnswer(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeThinking, Content: "let me reason"},
		{ResponseType: types.ResponseTypeThinking, Content: " about the image"},
		{ResponseType: types.ResponseTypeThinking, Done: true},
		{ResponseType: types.ResponseTypeAnswer, Content: "ACTUAL"},
		{ResponseType: types.ResponseTypeAnswer, Content: " OCR"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: &types.TokenUsage{TotalTokens: 9}},
	})

	var chunks []string
	var doneText string
	firstTokenAtFirstAnswer := false
	sawFirstToken := false
	opts := &PredictOptions{
		OnChunk: func(text string, done bool, _ error) {
			if done {
				doneText = text
				return
			}
			// By the time the first ANSWER chunk is forwarded, the reasoning
			// phase must already have marked the first token.
			if len(chunks) == 0 {
				firstTokenAtFirstAnswer = sawFirstToken
			}
			chunks = append(chunks, text)
		},
		StatusSink: func(phase Phase, _ PhaseInfo) {
			if phase == PhaseFirstToken {
				sawFirstToken = true
			}
		},
	}

	text, _, _, err := m.runInner(context.Background(), nil, "p", opts, time.Now())
	if err != nil {
		t.Fatalf("runInner: %v", err)
	}
	if text != "ACTUAL OCR" {
		t.Errorf("answer = %q, want %q (thinking content leaked into the answer)",
			text, "ACTUAL OCR")
	}
	if strings.Join(chunks, "|") != "ACTUAL| OCR" {
		t.Errorf("OnChunk incremental calls received %q, want [ACTUAL OCR] "+
			"(reasoning text must not be forwarded)", chunks)
	}
	if doneText != "ACTUAL OCR" {
		t.Errorf("OnChunk done summary = %q, want %q (thinking must not enter the answer)",
			doneText, "ACTUAL OCR")
	}
	if !firstTokenAtFirstAnswer {
		t.Error("PhaseFirstToken did not fire during the reasoning phase; " +
			"TTFT was anchored to the answer instead of the first server token")
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

// TestStreamExhaustedBudgetIsAnError pins the length-guard: a reasoning model
// that spends the whole completion budget on thinking still produces a clean
// answer-done event (finish_reason=length) with EMPTY text. That must surface
// as the same explicit error the buffered path raises — never as a "" success,
// which the caller would classify as "image has no text" (skipped).
func TestStreamExhaustedBudgetIsAnError(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeThinking, Content: "reasoning"},
		{ResponseType: types.ResponseTypeThinking, Done: true},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "length"},
	})

	_, _, _, err := m.runInner(context.Background(), nil, "p", nil, time.Now())
	if err == nil {
		t.Fatal("empty answer with finish_reason=length accepted as success; budget exhaustion would masquerade as skipped")
	}
	if !strings.Contains(err.Error(), "finish_reason=length") {
		t.Errorf("err = %v, want it to mention finish_reason=length", err)
	}
}

// TestStreamGenuinelyEmptyAnswerStaysSuccess keeps the skipped semantics: a
// clean terminal with NO text and NO length finish reason is the model saying
// "this image has no text" — still a success with empty content, routed by
// the caller to skipped.
func TestStreamGenuinelyEmptyAnswerStaysSuccess(t *testing.T) {
	m := newFakeStreamVLM(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: &types.TokenUsage{TotalTokens: 3}},
	})

	text, _, _, err := m.runInner(context.Background(), nil, "p", nil, time.Now())
	if err != nil {
		t.Fatalf("runInner: %v", err)
	}
	if text != "" {
		t.Errorf("answer = %q, want empty (genuinely blank image)", text)
	}
}
