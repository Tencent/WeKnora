package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeImageVLM implements vlm.VLM for chain tests.
type fakeImageVLM struct {
	id       string
	name     string
	response string
	err      error
	calls    int
}

func (f *fakeImageVLM) Predict(context.Context, [][]byte, string) (string, error) {
	f.calls++
	return f.response, f.err
}

func (f *fakeImageVLM) GetModelName() string { return f.name }
func (f *fakeImageVLM) GetModelID() string   { return f.id }

func resolvedChain(models ...*fakeImageVLM) *resolvedImageVLM {
	r := &resolvedImageVLM{}
	for _, m := range models {
		r.requestedModelIDs = append(r.requestedModelIDs, m.id)
		r.candidates = append(r.candidates, imageVLMCandidate{
			model:   m,
			modelID: m.id,
		})
	}
	return r
}

func TestPredictWithVLMChain_PrimarySuccessDoesNotCallFallback(t *testing.T) {
	primary := &fakeImageVLM{id: "primary-id", name: "primary", response: "primary text"}
	fallback := &fakeImageVLM{id: "fallback-id", name: "fallback", response: "fallback text"}

	text, trace, err := predictWithVLMChain(context.Background(), resolvedChain(primary, fallback),
		[][]byte{{1, 2, 3}}, "prompt")
	if err != nil {
		t.Fatalf("predict returned error: %v", err)
	}
	if text != "primary text" {
		t.Fatalf("expected primary response, got %q", text)
	}
	if primary.calls != 1 {
		t.Fatalf("expected primary called once, got %d", primary.calls)
	}
	if fallback.calls != 0 {
		t.Fatalf("expected fallback not called, got %d", fallback.calls)
	}
	if trace.UsedModelID != "primary-id" {
		t.Fatalf("expected used model primary-id, got %q", trace.UsedModelID)
	}
	if len(trace.Attempts) != 1 {
		t.Fatalf("expected one attempt, got %d", len(trace.Attempts))
	}
}

func TestPredictWithVLMChain_PrimaryErrorCallsFallback(t *testing.T) {
	primaryErr := errors.New("upstream timeout")
	primary := &fakeImageVLM{id: "primary-id", name: "primary", err: primaryErr}
	fallback := &fakeImageVLM{id: "fallback-id", name: "fallback", response: "fallback text"}

	text, trace, err := predictWithVLMChain(context.Background(), resolvedChain(primary, fallback),
		[][]byte{{1, 2, 3}}, "prompt")
	if err != nil {
		t.Fatalf("predict returned error: %v", err)
	}
	if text != "fallback text" {
		t.Fatalf("expected fallback response, got %q", text)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("expected primary and fallback called once, got primary=%d fallback=%d", primary.calls, fallback.calls)
	}
	if trace.UsedModelID != "fallback-id" {
		t.Fatalf("expected fallback-id used, got %q", trace.UsedModelID)
	}
	if trace.Attempts[0].Error != primaryErr.Error() {
		t.Fatalf("expected first attempt error %q, got %q", primaryErr.Error(), trace.Attempts[0].Error)
	}
}

func TestPredictWithVLMChain_EmptyTextCallsFallback(t *testing.T) {
	primary := &fakeImageVLM{id: "primary-id", name: "primary", response: "   "}
	fallback := &fakeImageVLM{id: "fallback-id", name: "fallback", response: "fallback text"}

	text, trace, err := predictWithVLMChain(context.Background(), resolvedChain(primary, fallback),
		[][]byte{{1, 2, 3}}, "prompt")
	if err != nil {
		t.Fatalf("predict returned error: %v", err)
	}
	if text != "fallback text" {
		t.Fatalf("expected fallback response, got %q", text)
	}
	if primary.calls != 1 || fallback.calls != 1 {
		t.Fatalf("expected primary and fallback called once, got primary=%d fallback=%d", primary.calls, fallback.calls)
	}
	if trace.Attempts[0].Error != "empty VLM response" {
		t.Fatalf("expected empty response as first attempt error, got %q", trace.Attempts[0].Error)
	}
	if trace.UsedModelID != "fallback-id" {
		t.Fatalf("expected fallback-id used, got %q", trace.UsedModelID)
	}
}

func TestPredictWithVLMChain_ThirdModelSucceeds(t *testing.T) {
	first := &fakeImageVLM{id: "first", name: "first", err: errors.New("model not found")}
	second := &fakeImageVLM{id: "second", name: "second", err: errors.New("rate limited")}
	third := &fakeImageVLM{id: "third", name: "third", response: "third text"}

	text, trace, err := predictWithVLMChain(context.Background(), resolvedChain(first, second, third),
		[][]byte{{1, 2, 3}}, "prompt")
	if err != nil {
		t.Fatalf("predict returned error: %v", err)
	}
	if text != "third text" {
		t.Fatalf("expected third response, got %q", text)
	}
	if first.calls != 1 || second.calls != 1 || third.calls != 1 {
		t.Fatalf("expected every model called once, got first=%d second=%d third=%d", first.calls, second.calls, third.calls)
	}
	if trace.UsedModelID != "third" {
		t.Fatalf("expected third used, got %q", trace.UsedModelID)
	}
	if len(trace.Attempts) != 3 {
		t.Fatalf("expected three attempts, got %d", len(trace.Attempts))
	}
}

func TestPredictWithVLMChain_AllFailedReturnsAggregatedError(t *testing.T) {
	first := &fakeImageVLM{id: "first", name: "first", err: errors.New("model not found")}
	second := &fakeImageVLM{id: "second", name: "second", response: ""}

	_, trace, err := predictWithVLMChain(context.Background(), resolvedChain(first, second), [][]byte{{1, 2, 3}}, "prompt")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "all VLM models failed") {
		t.Fatalf("expected aggregate error, got %v", err)
	}
	if !strings.Contains(trace.AllFailedError, "first: model not found") ||
		!strings.Contains(trace.AllFailedError, "second: empty VLM response") {
		t.Fatalf("unexpected all-failed trace: %q", trace.AllFailedError)
	}
	if trace.UsedModelID != "" {
		t.Fatalf("expected no used model, got %q", trace.UsedModelID)
	}
}

func TestPredictWithVLMChain_UnresolvedChain(t *testing.T) {
	if _, _, err := predictWithVLMChain(context.Background(), nil, [][]byte{{1}}, "prompt"); err == nil {
		t.Fatal("expected error for nil chain")
	}
	if _, _, err := predictWithVLMChain(context.Background(), &resolvedImageVLM{}, [][]byte{{1}}, "prompt"); err == nil {
		t.Fatal("expected error for empty chain")
	}
}
