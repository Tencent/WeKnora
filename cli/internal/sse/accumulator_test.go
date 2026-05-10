package sse_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestAccumulator_AppendsContent(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{Content: "Hello "})
	a.Append(&sdk.StreamResponse{Content: "world"})
	if got := a.Result(); got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
	if a.Done() {
		t.Error("expected Done=false before terminal event")
	}
}

func TestAccumulator_FinalizesOnDone(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{Content: "answer"})
	a.Append(&sdk.StreamResponse{
		Done:                true,
		KnowledgeReferences: []*sdk.SearchResult{{KnowledgeID: "k1"}},
	})
	if !a.Done() {
		t.Error("expected Done=true")
	}
	if len(a.References) != 1 {
		t.Errorf("expected 1 reference, got %d", len(a.References))
	}
	if a.References[0].KnowledgeID != "k1" {
		t.Errorf("references payload not preserved: %+v", a.References[0])
	}
}

func TestAccumulator_IgnoresPostDone(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{Content: "first"})
	a.Append(&sdk.StreamResponse{Done: true})
	a.Append(&sdk.StreamResponse{Content: "after"})
	if got := a.Result(); got != "first" {
		t.Errorf("post-Done append should be no-op, got %q", got)
	}
}

func TestAccumulator_NilSafe(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(nil)
	if got := a.Result(); got != "" {
		t.Errorf("expected empty result for nil append, got %q", got)
	}
	if a.Done() {
		t.Error("nil append must not finalize")
	}
}

func TestAccumulator_CapturesSessionMetadata(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{
		SessionID:          "sess_123",
		AssistantMessageID: "msg_456",
		Content:            "hi",
	})
	a.Append(&sdk.StreamResponse{Done: true})
	if a.SessionID != "sess_123" {
		t.Errorf("SessionID: got %q", a.SessionID)
	}
	if a.AssistantMessageID != "msg_456" {
		t.Errorf("AssistantMessageID: got %q", a.AssistantMessageID)
	}
}

func TestAccumulator_FirstSessionMetadataWins(t *testing.T) {
	// Subsequent events must not overwrite the first non-empty value — the
	// session id is set once at session start and any later override would be
	// a server bug we should not silently mask.
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{SessionID: "sess_first", AssistantMessageID: "msg_first"})
	a.Append(&sdk.StreamResponse{SessionID: "sess_second", AssistantMessageID: "msg_second"})
	if a.SessionID != "sess_first" {
		t.Errorf("SessionID overwritten: got %q want sess_first", a.SessionID)
	}
	if a.AssistantMessageID != "msg_first" {
		t.Errorf("AssistantMessageID overwritten: got %q want msg_first", a.AssistantMessageID)
	}
}
