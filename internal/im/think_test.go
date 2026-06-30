package im

import (
	"strings"
	"testing"
)

func TestStripThinkBlocks(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "no think blocks", input: "Hello, world!", want: "Hello, world!"},
		{name: "empty", input: "", want: ""},
		{
			name:  "single block before answer",
			input: "<think>reasoning</think>The answer is 42.",
			want:  "The answer is 42.",
		},
		{
			name: "multiline think with tools",
			input: "<think>\n让我先搜索知识库\n正在调用 搜索关键词...\n搜索关键词：「文明」\n</think>\n\n文明6是一款策略游戏。",
			want:  "文明6是一款策略游戏。",
		},
		{
			name:  "multiple blocks",
			input: "<think>first</think>Part 1. <think>second</think>Part 2.",
			want:  "Part 1. Part 2.",
		},
		{name: "only think block", input: "<think>just thinking</think>", want: ""},
		{
			name:  "unclosed think block",
			input: "<think>still streaming",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripThinkBlocks(tt.input)
			if got != tt.want {
				t.Fatalf("StripThinkBlocks() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatIMDisplayContent_intermediate_showsThinkingStyled(t *testing.T) {
	raw := "<think>\n分析用户问题\n正在调用 知识库检索...\n</think>\n\n"
	got := FormatIMDisplayContent(raw, StreamDisplayIntermediate)

	if !strings.Contains(got, "思考过程") {
		t.Fatalf("intermediate display should include thinking header, got: %q", got)
	}
	if !strings.Contains(got, "分析用户问题") {
		t.Fatalf("intermediate display should include thinking body, got: %q", got)
	}
	if !strings.Contains(got, "知识库检索") {
		t.Fatalf("intermediate display should include tool progress, got: %q", got)
	}
	if strings.Contains(got, "<think>") {
		t.Fatalf("intermediate display must not leak raw think tags, got: %q", got)
	}
}

func TestFormatIMDisplayContent_intermediate_inProgressThink(t *testing.T) {
	raw := "<think>\n正在推理\n正在调用 搜索关键词...\n"
	got := FormatIMDisplayContent(raw, StreamDisplayIntermediate)

	if !strings.Contains(got, "思考") {
		t.Fatalf("open think block should show thinking header, got: %q", got)
	}
	if strings.Contains(got, "<think>") {
		t.Fatalf("must not leak raw tags, got: %q", got)
	}
}

func TestFormatIMDisplayContent_intermediate_showsAnswerPreview(t *testing.T) {
	raw := "<think>\n检索中\n</think>\n\n文明6是回合制策略游戏。"
	got := FormatIMDisplayContent(raw, StreamDisplayIntermediate)

	if !strings.Contains(got, "文明6是回合制策略游戏") {
		t.Fatalf("intermediate display should preview answer after think block, got: %q", got)
	}
}

func TestFormatIMDisplayContent_final_stripsThinkingAndTools(t *testing.T) {
	raw := "<think>\n让我先搜索知识库\n正在调用 搜索关键词...\n搜索关键词：「文明」\n</think>\n\n文明6是一款策略游戏。"
	got := FormatIMDisplayContent(raw, StreamDisplayFinal)

	want := "文明6是一款策略游戏。"
	if got != want {
		t.Fatalf("final display = %q, want %q", got, want)
	}
}

func TestFormatIMDisplayContent_final_plainAnswerUnchanged(t *testing.T) {
	raw := "这是最终答案。"
	got := FormatIMDisplayContent(raw, StreamDisplayFinal)
	if got != raw {
		t.Fatalf("final display = %q, want %q", got, raw)
	}
}

func TestFormatIMDisplayContent_final_ragPipelineHidden(t *testing.T) {
	raw := "<think>\n正在理解问题...\n已完成问题理解\n正在检索知识库...\n检索知识库：「query」 · 找到 3 个结果\n</think>\n\n根据知识库，答案是 A。"
	got := FormatIMDisplayContent(raw, StreamDisplayFinal)

	if strings.Contains(got, "问题理解") || strings.Contains(got, "知识库检索") {
		t.Fatalf("final display must not contain RAG pipeline steps, got: %q", got)
	}
	if got != "根据知识库，答案是 A。" {
		t.Fatalf("final display = %q", got)
	}
}

func TestFormatIMAgentIntermediate_answerFirstBeforeTools(t *testing.T) {
	parts := IMStreamParts{
		Mode:       IMStreamModeAgent,
		LiveAnswer: "好的，让我先搜索知识库。",
	}
	got := FormatIMIntermediateFromParts(parts, true)
	if got != "好的，让我先搜索知识库。" {
		t.Fatalf("should stream as plain answer, got: %q", got)
	}
	if strings.Contains(got, "思考过程") {
		t.Fatal("think header must not appear while answer is live")
	}
}

func TestFormatIMAgentIntermediate_retractIntoThinkOnTools(t *testing.T) {
	parts := IMStreamParts{
		Mode: IMStreamModeAgent,
		AgentInner: "好的，让我先搜索知识库。\n",
		AgentToolSteps: []IMToolStep{
			{ToolName: "grep_chunks", Pending: true},
			{ToolName: "knowledge_search", Success: true, Arguments: map[string]any{"query": "文明6"}},
		},
	}
	got := FormatIMIntermediateFromParts(parts, true)
	if !strings.Contains(got, "思考过程") {
		t.Fatalf("after tool retract should show think block, got: %q", got)
	}
	if !strings.Contains(got, "好的，让我先搜索知识库") {
		t.Fatalf("retracted preamble should be inside think, got: %q", got)
	}
	if !strings.Contains(got, "搜索关键词") {
		t.Fatalf("tool lines should be inside think, got: %q", got)
	}
	if !strings.Contains(got, "文明6") {
		t.Fatalf("tool query should be inside think, got: %q", got)
	}
}

func TestFormatIMAgentIntermediate_newAnswerAfterTools(t *testing.T) {
	parts := IMStreamParts{
		Mode: IMStreamModeAgent,
		AgentInner: "好的，让我搜索\n",
		AgentToolSteps: []IMToolStep{
			{ToolName: "knowledge_search", Success: true, Arguments: map[string]any{"query": "文明6"}},
		},
		LiveAnswer: "根据检索结果，文明6是…",
	}
	got := FormatIMIntermediateFromParts(parts, true)
	if !strings.Contains(got, "根据检索结果，文明6是…") {
		t.Fatalf("should still stream live answer, got: %q", got)
	}
	if !strings.Contains(got, "思考过程") {
		t.Fatalf("think block should stay visible above answer, got: %q", got)
	}
	if !strings.Contains(got, "文明6") {
		t.Fatalf("tool query should remain in think block, got: %q", got)
	}
}

func TestBuildIMStreamRaw_agentInProgress_mergesToolsAndNarrativeIntoThink(t *testing.T) {
	parts := IMStreamParts{
		Mode:       IMStreamModeAgent,
		AgentInner: "用户又问文明6\n",
		AgentToolSteps: []IMToolStep{
			{ToolName: "grep_chunks", Pending: true},
			{ToolName: "knowledge_search", Success: true},
		},
	}
	got := FormatIMIntermediateFromParts(parts, true)

	if !strings.Contains(got, "搜索关键词") {
		t.Fatalf("tool progress should be inside think block, got: %q", got)
	}
	if !strings.Contains(got, "思考过程") {
		t.Fatalf("agent tooling phase should show 思考过程, got: %q", got)
	}
}

func TestFormatIMQuickQA_separatesPipelineAndThinking(t *testing.T) {
	parts := IMStreamParts{
		Mode: IMStreamModeQuickQA,
		PipelineToolSteps: []IMToolStep{
			{ToolName: "query_understand", Pending: true},
			{ToolName: "knowledge_search", Success: true, Arguments: map[string]any{"query": "文明6"}},
		},
		ReasoningInner: "分析问题意图…",
	}
	got := FormatIMIntermediateFromParts(parts, false)

	if strings.Contains(got, "思考过程") {
		t.Fatalf("quick QA should not use agent 思考过程 header, got: %q", got)
	}
	if !strings.Contains(got, "> 💭 **思考**") {
		t.Fatalf("quick QA reasoning should use separate 思考 section, got: %q", got)
	}
	if !strings.Contains(got, "分析问题意图") {
		t.Fatalf("reasoning body missing, got: %q", got)
	}
	if !strings.Contains(got, "正在理解问题") {
		t.Fatalf("pipeline steps missing, got: %q", got)
	}
	if !strings.Contains(got, "文明6") {
		t.Fatalf("pipeline query missing, got: %q", got)
	}
}

func TestFormatIMQuickQA_collapsesToAnswerWhenStreaming(t *testing.T) {
	parts := IMStreamParts{
		Mode: IMStreamModeQuickQA,
		PipelineToolSteps: []IMToolStep{
			{ToolName: "query_understand", Success: true},
			{ToolName: "knowledge_search", Success: true},
		},
		Answer: "文明6是回合制策略游戏。",
	}
	got := FormatIMIntermediateFromParts(parts, false)
	if got != "文明6是回合制策略游戏。" {
		t.Fatalf("quick QA should collapse to answer preview, got: %q", got)
	}
}

func TestFormatIMFinalFromParts_agentAnswerOnly(t *testing.T) {
	parts := IMStreamParts{
		Mode: IMStreamModeAgent,
		AgentInner: "好的，让我搜索\n",
		AgentToolSteps: []IMToolStep{
			{ToolName: "grep_chunks", Pending: true},
			{ToolName: "knowledge_search", Success: true},
		},
		LiveAnswer: "不应出现在最终消息",
		Answer:     "文明6是一款策略游戏。",
	}
	got := FormatIMFinalFromParts(parts)
	if got != "文明6是一款策略游戏。" {
		t.Fatalf("final should be answer-only, got: %q", got)
	}
	if strings.Contains(got, "思考过程") {
		t.Fatalf("final must not include collapsed think header, got: %q", got)
	}
}

func TestFormatIMFinalFromParts_usesAnswerOnly(t *testing.T) {
	parts := IMStreamParts{
		Mode:           IMStreamModeQuickQA,
		PipelineToolSteps: []IMToolStep{{ToolName: "query_understand", Success: true}},
		ReasoningInner: "推理中",
		AgentToolSteps: []IMToolStep{{ToolName: "grep_chunks", Pending: true}},
		Answer:         "文明6是一款策略游戏。",
	}
	got := FormatIMFinalFromParts(parts)
	if got != "文明6是一款策略游戏。" {
		t.Fatalf("final display = %q", got)
	}
}

func TestIsRAGPipelineToolName_matchesWeb(t *testing.T) {
	for _, name := range []string{"query_understand", "knowledge_search"} {
		if !IsRAGPipelineToolName(name) {
			t.Fatalf("%q should be a RAG pipeline tool", name)
		}
	}
	if IsRAGPipelineToolName("grep_chunks") {
		t.Fatal("grep_chunks is agent tool, not RAG pipeline progress tool")
	}
}
