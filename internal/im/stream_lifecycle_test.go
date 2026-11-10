package im

import (
	"context"
	"strings"
	"testing"
)

// imStreamDisplayState mirrors the display buffers in handleMessageStream for lifecycle tests.
type imStreamDisplayState struct {
	useAgent          bool
	agentDone         bool
	agentInner        streamSection
	agentLiveAnswer   strings.Builder
	answerOuter       strings.Builder
	agentToolSteps    []IMToolStep
	agentToolIdx      map[string]int
	pipelineToolSteps []IMToolStep
	pipelineIdx       map[string]int
}

func newIMStreamDisplayState(useAgent bool) *imStreamDisplayState {
	return &imStreamDisplayState{
		useAgent:     useAgent,
		agentToolIdx: make(map[string]int),
		pipelineIdx:  make(map[string]int),
	}
}

func (s *imStreamDisplayState) retractAgentLiveAnswer() {
	if s.agentLiveAnswer.Len() == 0 {
		return
	}
	if s.agentInner.text.Len() > 0 {
		s.agentInner.ensureNewlineBefore()
	}
	s.agentInner.write(s.agentLiveAnswer.String())
	s.agentLiveAnswer.Reset()
}

func (s *imStreamDisplayState) parts() IMStreamParts {
	mode := IMStreamModeQuickQA
	if s.useAgent {
		mode = IMStreamModeAgent
	}
	return IMStreamParts{
		Mode:              mode,
		PipelineToolSteps: s.pipelineToolSteps,
		AgentInner:        s.agentInner.text.String(),
		AgentToolSteps:    s.agentToolSteps,
		LiveAnswer:        s.agentLiveAnswer.String(),
		Answer:            s.answerOuter.String(),
	}
}

func (s *imStreamDisplayState) intermediate() string {
	return FormatIMIntermediateFromParts(s.parts(), s.useAgent && !s.agentDone)
}

func (s *imStreamDisplayState) final() string {
	return FormatIMFinalFromParts(s.parts())
}

// TestIMStreamLifecycle_agentToolRetract simulates handleMessageStream display assembly:
// live answer → tool retract → tool result → final answer → complete.
func TestIMStreamLifecycle_agentToolRetract(t *testing.T) {
	state := newIMStreamDisplayState(true)

	state.agentLiveAnswer.WriteString("好的，让我先搜索知识库。")
	liveOnly := state.intermediate()
	if liveOnly != "好的，让我先搜索知识库。" {
		t.Fatalf("live answer phase = %q", liveOnly)
	}
	if strings.Contains(liveOnly, "思考过程") {
		t.Fatal("think header must not appear before tool retract")
	}

	state.retractAgentLiveAnswer()
	upsertIMToolStep(&state.agentToolSteps, state.agentToolIdx, "tool-1", func(step *IMToolStep) {
		step.ToolName = "grep_chunks"
		step.Pending = true
	})
	duringTools := state.intermediate()
	if !strings.Contains(duringTools, "思考过程") {
		t.Fatalf("after retract should show think block, got: %q", duringTools)
	}
	if !strings.Contains(duringTools, "好的，让我先搜索知识库") {
		t.Fatalf("retracted preamble missing, got: %q", duringTools)
	}

	upsertIMToolStep(&state.agentToolSteps, state.agentToolIdx, "tool-1", func(step *IMToolStep) {
		step.ToolName = "grep_chunks"
		step.Pending = false
		step.Success = true
	})
	state.agentLiveAnswer.WriteString("根据检索结果，《文明6》是回合制策略游戏。")
	withLiveAnswer := state.intermediate()
	if !strings.Contains(withLiveAnswer, "根据检索结果") {
		t.Fatalf("live answer after tools missing, got: %q", withLiveAnswer)
	}

	state.agentDone = true
	state.answerOuter.WriteString("《文明6》是回合制策略游戏。")
	final := state.final()
	if !strings.Contains(final, "《文明6》是回合制策略游戏。") {
		t.Fatalf("final answer missing, got: %q", final)
	}

	rec := &recordingStreamSender{}
	ctx := context.Background()
	incoming := &IncomingMessage{Platform: PlatformWeCom, UserID: "u1"}
	streamID, err := rec.StartStream(ctx, incoming)
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if err := rec.UpdateStreamContent(ctx, incoming, streamID, duringTools); err != nil {
		t.Fatalf("UpdateStreamContent: %v", err)
	}
	if err := rec.FinalizeStream(ctx, incoming, streamID, final); err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if err := rec.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	chunks, finalized, ended := rec.snapshot()
	if !ended {
		t.Fatal("stream should end")
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 intermediate update, got %d", len(chunks))
	}
	if chunks[0] == finalized {
		t.Fatal("intermediate and final should differ")
	}
}

// TestIMStreamLifecycle_quickQAPipeline verifies quick-QA pipeline steps collapse to answer-only final.
func TestIMStreamLifecycle_quickQAPipeline(t *testing.T) {
	state := newIMStreamDisplayState(false)

	upsertIMToolStep(&state.pipelineToolSteps, state.pipelineIdx, "qu-1", func(step *IMToolStep) {
		step.ToolName = "query_understand"
		step.Pending = false
		step.Success = true
	})
	upsertIMToolStep(&state.pipelineToolSteps, state.pipelineIdx, "ks-1", func(step *IMToolStep) {
		step.ToolName = "knowledge_search"
		step.Pending = true
		step.Arguments = map[string]any{"query": "文明6"}
	})

	intermediate := state.intermediate()
	if intermediate == "" {
		t.Fatal("pipeline progress should be visible")
	}
	if strings.Contains(intermediate, "思考过程") {
		t.Fatalf("quick QA must not use agent think header, got: %q", intermediate)
	}

	state.answerOuter.WriteString("答案是 B。")
	final := state.final()
	if final != "答案是 B。" {
		t.Fatalf("final = %q, want answer only", final)
	}
}
