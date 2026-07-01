package sse_test

import (
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestAgentAccumulator_FinalizesOnlyOnComplete(t *testing.T) {
	a := &sse.AgentAccumulator{}
	preTerminal := []*sdk.AgentStreamResponse{
		{ResponseType: sdk.AgentResponseTypeThinking, Content: "think", Done: true},
		{ResponseType: sdk.AgentResponseTypeToolCall, ID: "call_1", Content: "search"},
		{ResponseType: sdk.AgentResponseTypeReflection, Content: "reflect", Done: true},
		{ResponseType: sdk.AgentResponseTypeAnswer, Content: "final answer", Done: true},
	}
	for _, event := range preTerminal {
		a.Append(event)
		if a.Done() {
			t.Fatalf("%s Done=true terminated the whole stream", event.ResponseType)
		}
	}

	a.Append(&sdk.AgentStreamResponse{ResponseType: sdk.AgentResponseTypeComplete, Done: true})
	if !a.Done() {
		t.Fatal("complete event did not terminate the stream")
	}
	if got := a.Answer(); got != "final answer" {
		t.Errorf("answer=%q, want final answer", got)
	}
	if got := a.Thinking(); got != "thinkreflect" {
		t.Errorf("thinking=%q, want thinkreflect", got)
	}
	if len(a.ToolEvents) != 1 {
		t.Errorf("tool_events=%d, want 1", len(a.ToolEvents))
	}

	a.Append(&sdk.AgentStreamResponse{ResponseType: sdk.AgentResponseTypeAnswer, Content: "late"})
	if got := a.Answer(); got != "final answer" {
		t.Errorf("post-complete event was appended: %q", got)
	}
}
