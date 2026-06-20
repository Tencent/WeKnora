package sse_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/sse"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestProjector_DefaultKeepsOnlyAnswerEvents(t *testing.T) {
	p := sse.NewProjector(false, false, "kb_fallback")
	input := []*sdk.StreamResponse{
		{ID: "think", ResponseType: sdk.ResponseTypeThinking, Content: "hidden"},
		{ID: "a", ResponseType: sdk.ResponseTypeAnswer, Content: "one", KnowledgeReferences: []*sdk.SearchResult{{ID: "piggyback", Content: "bulk"}}},
		{ID: "refs", ResponseType: sdk.ResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{{ID: "c1", Content: "bulk"}}},
		{ID: "a", ResponseType: sdk.ResponseTypeAnswer, Content: "two", Done: true},
		{ResponseType: sdk.ResponseTypeComplete, Done: true},
	}
	var got []sse.ProjectedEvent
	for _, event := range input {
		if projected, ok := p.Chat(event); ok {
			got = append(got, projected)
		}
	}
	if !p.Done() || len(got) != 2 {
		t.Fatalf("done=%v events=%+v", p.Done(), got)
	}
	if got[0].Content != "one" || got[1].Content != "two" {
		t.Errorf("answer order changed: %+v", got)
	}
	if len(got[0].KnowledgeReferences) != 0 {
		t.Errorf("default answer leaked piggyback references: %+v", got[0])
	}
}

func TestProjector_VerboseAndReferenceIncludeBothDetailClasses(t *testing.T) {
	p := sse.NewProjector(true, true, "kb_fallback")
	input := []*sdk.AgentStreamResponse{
		{ID: "t", ResponseType: sdk.AgentResponseTypeThinking, Content: "think"},
		{ID: "call", ResponseType: sdk.AgentResponseTypeToolCall, Content: "search"},
		{ID: "refs", ResponseType: sdk.AgentResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{{ID: "c1", KnowledgeBaseID: "kb1", ParentChunkID: "p1", Content: "bulk"}}},
		{ID: "a", ResponseType: sdk.AgentResponseTypeAnswer, Content: "answer"},
		{ResponseType: sdk.AgentResponseTypeComplete, Done: true},
	}
	var got []sse.ProjectedEvent
	for _, event := range input {
		if projected, ok := p.Agent(event); ok {
			got = append(got, projected)
		}
	}
	want := []string{"thinking", "tool_call", "references", "answer", "complete"}
	if len(got) != len(want) {
		t.Fatalf("events=%+v", got)
	}
	for i := range want {
		if got[i].ResponseType != want[i] {
			t.Errorf("event[%d]=%q, want %q", i, got[i].ResponseType, want[i])
		}
	}
	refs := got[2].KnowledgeReferences
	if len(refs) != 1 || refs[0].KBID != "kb1" || refs[0].ChunkID != "c1" || refs[0].ParentChunkID != "p1" {
		t.Errorf("reference index=%+v", refs)
	}
	if input[2].KnowledgeReferences[0].Content != "bulk" {
		t.Error("projection mutated the raw SDK event")
	}
}

func TestProjector_ReferenceOnlyAddsIndexesWithoutExecutionTrace(t *testing.T) {
	p := sse.NewProjector(false, true, "kb_fallback")
	input := []*sdk.AgentStreamResponse{
		{ID: "think", ResponseType: sdk.AgentResponseTypeThinking, Content: "hidden"},
		{ID: "refs", ResponseType: sdk.AgentResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{{ID: "c1", KnowledgeBaseID: "kb1", Content: "bulk"}}},
		{ID: "answer", ResponseType: sdk.AgentResponseTypeAnswer, Content: "answer"},
		{ResponseType: sdk.AgentResponseTypeComplete, Done: true},
	}
	var got []sse.ProjectedEvent
	for _, event := range input {
		if projected, ok := p.Agent(event); ok {
			got = append(got, projected)
		}
	}
	if len(got) != 2 || got[0].ResponseType != "references" || got[1].ResponseType != "answer" {
		t.Fatalf("reference-only events=%+v", got)
	}
	if len(got[0].KnowledgeReferences) != 1 || got[0].KnowledgeReferences[0].ChunkID != "c1" {
		t.Errorf("reference indexes=%+v", got[0].KnowledgeReferences)
	}
}

func TestProjector_VerboseDoesNotImplicitlyAddReferences(t *testing.T) {
	p := sse.NewProjector(true, false, "kb")
	input := []*sdk.AgentStreamResponse{
		{ResponseType: sdk.AgentResponseTypeThinking, Content: "thinking"},
		{ResponseType: sdk.AgentResponseTypeReferences, KnowledgeReferences: []*sdk.SearchResult{{ID: "c1"}}},
		{ResponseType: sdk.AgentResponseTypeAnswer, Content: "answer"},
		{ResponseType: sdk.AgentResponseTypeComplete, Done: true},
	}
	var got []sse.ProjectedEvent
	for _, event := range input {
		if projected, ok := p.Agent(event); ok {
			got = append(got, projected)
		}
	}
	for _, event := range got {
		if event.ResponseType == "references" || len(event.KnowledgeReferences) != 0 {
			t.Fatalf("verbose leaked references without reference=true: %+v", got)
		}
	}
}

func TestTextRenderer_StreamsSelectedEvents(t *testing.T) {
	var out bytes.Buffer
	r := sse.NewTextRenderer(&out, true)
	events := []sse.ProjectedEvent{
		{ID: "t", ResponseType: "thinking", Content: "THINK", Done: true},
		{ID: "call", ResponseType: "tool_call", Content: "SEARCH"},
		{ID: "a", ResponseType: "answer", Content: "ANSWER", Done: true},
	}
	for _, event := range events {
		if err := r.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	thinking := strings.Index(got, "THINK")
	tool := strings.Index(got, "SEARCH")
	answer := strings.Index(got, "ANSWER")
	if !(thinking >= 0 && thinking < tool && tool < answer) {
		t.Errorf("event order changed: %q", got)
	}
}
