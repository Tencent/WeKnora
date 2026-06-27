package im

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// recordingStreamSender records streaming calls for TDD verification.
type recordingStreamSender struct {
	mu sync.Mutex

	streamID      string
	chunkContents []string // full display content per UpdateStreamContent call
	finalContent  string
	ended         bool
}

func (m *recordingStreamSender) StartStream(_ context.Context, _ *IncomingMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamID = "rec-stream-1"
	return m.streamID, nil
}

func (m *recordingStreamSender) UpdateStreamContent(_ context.Context, _ *IncomingMessage, _ string, fullContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunkContents = append(m.chunkContents, fullContent)
	return nil
}

func (m *recordingStreamSender) FinalizeStream(_ context.Context, _ *IncomingMessage, _ string, finalContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalContent = finalContent
	return nil
}

func (m *recordingStreamSender) EndStream(_ context.Context, _ *IncomingMessage, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended = true
	return nil
}

func (m *recordingStreamSender) snapshot() (chunks []string, final string, ended bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.chunkContents))
	copy(out, m.chunkContents)
	return out, m.finalContent, m.ended
}

func TestStreamDisplayPipeline_agentScenario_redGreen(t *testing.T) {
	// Simulates the agent IM stream lifecycle aligned with Web:
	// 1) intermediate updates show styled thinking + tools
	// 2) final replace shows answer only (no collapsed think header)
	rawIntermediate := "<think>\n分析 Civilization VI 问题\n正在调用 搜索关键词...\n搜索关键词\n</think>\n\n"

	rec := &recordingStreamSender{}
	ctx := context.Background()
	incoming := &IncomingMessage{Platform: PlatformWeCom, UserID: "u1"}

	streamID, err := rec.StartStream(ctx, incoming)
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	intermediate := FormatIMDisplayContent(rawIntermediate, StreamDisplayIntermediate)
	if err := rec.UpdateStreamContent(ctx, incoming, streamID, intermediate); err != nil {
		t.Fatalf("UpdateStreamContent intermediate: %v", err)
	}

	final := FormatIMFinalFromParts(IMStreamParts{
		Mode: IMStreamModeAgent,
		AgentInner: "分析 Civilization VI 问题\n",
		AgentToolSteps: []IMToolStep{
			{ToolName: "grep_chunks", Success: true},
			{ToolName: "knowledge_search", Success: true},
		},
		Answer: "《文明6》是回合制策略游戏。",
	})
	if err := rec.FinalizeStream(ctx, incoming, streamID, final); err != nil {
		t.Fatalf("FinalizeStream: %v", err)
	}
	if err := rec.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatalf("EndStream: %v", err)
	}

	chunks, finalized, ended := rec.snapshot()
	if !ended {
		t.Fatal("stream should be ended")
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 intermediate update, got %d", len(chunks))
	}
	if chunks[0] == final {
		t.Fatal("intermediate update should differ from final answer-only content")
	}
	if finalized != "《文明6》是回合制策略游戏。" {
		t.Fatalf("FinalizeStream content = %q", finalized)
	}
	if !strings.Contains(finalized, "《文明6》是回合制策略游戏。") {
		t.Fatalf("FinalizeStream must include answer, got: %q", finalized)
	}
}

func TestStreamDisplayPipeline_quickQA_redGreen(t *testing.T) {
	during := IMStreamParts{
		Mode: IMStreamModeQuickQA,
		PipelineToolSteps: []IMToolStep{
			{ToolName: "query_understand", Success: true},
			{ToolName: "knowledge_search", Pending: true, Arguments: map[string]any{"query": "test"}},
		},
	}
	done := IMStreamParts{
		Mode: IMStreamModeQuickQA,
		PipelineToolSteps: []IMToolStep{
			{ToolName: "query_understand", Success: true},
			{ToolName: "knowledge_search", Success: true, Arguments: map[string]any{"query": "test"}},
		},
		Answer: "答案是 B。",
	}

	intermediate := FormatIMIntermediateFromParts(during, false)
	if intermediate == "" {
		t.Fatal("quick QA should show pipeline progress while streaming")
	}
	if !strings.Contains(intermediate, "问题理解") {
		t.Fatalf("quick QA pipeline should show query_understand step, got: %q", intermediate)
	}
	if strings.Contains(intermediate, "思考过程") {
		t.Fatalf("quick QA pipeline should not use agent think header, got: %q", intermediate)
	}

	final := FormatIMFinalFromParts(done)
	if final != "答案是 B。" {
		t.Fatalf("quick QA final = %q, want answer only", final)
	}
}
