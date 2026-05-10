package sse_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

// terminator is the canonical "stream is done" event the accumulator
// recognises. The server emits response_type=complete after the answer
// fragments and any references; per-event done=true on agent_query /
// answer frames is NOT terminal.
var terminator = &sdk.StreamResponse{ResponseType: sdk.ResponseTypeComplete, Done: true}

func TestAccumulator_AppendsContent(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "Hello "})
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "world"})
	if got := a.Result(); got != "Hello world" {
		t.Errorf("got %q, want %q", got, "Hello world")
	}
	if a.Done() {
		t.Error("expected Done=false before terminal event")
	}
}

func TestAccumulator_FinalizesOnComplete(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "answer"})
	// References may arrive on a dedicated event before the terminator.
	a.Append(&sdk.StreamResponse{
		ResponseType:        sdk.ResponseTypeReferences,
		KnowledgeReferences: []*sdk.SearchResult{{KnowledgeID: "k1"}},
	})
	a.Append(terminator)
	if !a.Done() {
		t.Error("expected Done=true after response_type=complete")
	}
	if len(a.References) != 1 {
		t.Errorf("expected 1 reference, got %d", len(a.References))
	}
	if a.References[0].KnowledgeID != "k1" {
		t.Errorf("references payload not preserved: %+v", a.References[0])
	}
}

func TestAccumulator_IgnoresAgentQueryDone(t *testing.T) {
	// Server emits a leading agent_query frame with done=true to deliver
	// session metadata; the accumulator must NOT treat that as terminal —
	// otherwise the answer fragments that follow would be discarded.
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{
		ResponseType:       sdk.ResponseTypeAgentQuery,
		Done:               true,
		SessionID:          "sess_123",
		AssistantMessageID: "msg_456",
	})
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "real answer"})
	a.Append(terminator)
	if !a.Done() {
		t.Error("expected Done=true after response_type=complete")
	}
	if got := a.Result(); got != "real answer" {
		t.Errorf("agent_query done=true should not terminate; got %q", got)
	}
	if a.SessionID != "sess_123" {
		t.Errorf("session metadata from agent_query frame lost: got %q", a.SessionID)
	}
}

func TestAccumulator_IgnoresPostComplete(t *testing.T) {
	a := &sse.Accumulator{}
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "first"})
	a.Append(terminator)
	a.Append(&sdk.StreamResponse{ResponseType: sdk.ResponseTypeAnswer, Content: "after"})
	if got := a.Result(); got != "first" {
		t.Errorf("post-complete append should be no-op, got %q", got)
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
		ResponseType:       sdk.ResponseTypeAnswer,
		SessionID:          "sess_123",
		AssistantMessageID: "msg_456",
		Content:            "hi",
	})
	a.Append(terminator)
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
