package invoke

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// The llm_debug formatters are the v1 llm_debug.go port; these tests pin the
// record-facing shapes (role prefix, image markers, option fields) so the
// on-disk log format stays stable for existing tooling.

func TestBuildLLMMessagesNeutralView(t *testing.T) {
	msgs := []Message{
		TextMessage(RoleSystem, "be brief"),
		{
			Role: RoleUser,
			Content: []Part{{Text: "look"}, {Image: &ImageRef{
				URL: "data:image/png;base64," + strings.Repeat("A", 200),
			}}},
			ToolCallID: "call_9",
			Name:       "fetcher",
		},
		{
			Role:    RoleAssistant,
			Content: []Part{{Text: "calling tool"}},
			ToolCalls: []ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
			}},
		},
	}

	out := buildLLMMessages(msgs)
	if len(out) != 3 {
		t.Fatalf("got %d LLMMessage entries, want 3", len(out))
	}
	if out[0].Role != "system" || out[0].Content != "be brief" {
		t.Fatalf("unexpected first message: %+v", out[0])
	}
	// Image parts become truncated [image_url: …] content markers; the data
	// URI must not flood the record.
	if !strings.Contains(out[1].Content, "[image_url: ") {
		t.Fatalf("image marker missing: %q", out[1].Content)
	}
	if len(out[1].Content) > 300 {
		t.Fatalf("image marker not truncated: %d chars", len(out[1].Content))
	}
	if out[1].ToolCallID != "call_9" || out[1].Name != "fetcher" {
		t.Fatalf("name/tool_call_id not carried: %+v", out[1])
	}
	if len(out[2].ToolCalls) != 1 || out[2].ToolCalls[0].FuncName != "search" {
		t.Fatalf("assistant tool call not carried: %+v", out[2].ToolCalls)
	}
}

func TestBuildOptionsSectionFields(t *testing.T) {
	if s := buildOptionsSection(nil); s != "" {
		t.Fatalf("nil opts must render empty, got %q", s)
	}
	thinking := true
	opts := &ChatOptions{
		Temperature:         0.5,
		TopP:                0.9,
		MaxCompletionTokens: 2048,
		ToolChoice:          "auto",
		Format:              json.RawMessage(`{"type":"json_object"}`),
		ThinkingLevel:       "high",
		Thinking:            &thinking,
	}
	s := buildOptionsSection(opts)
	for _, want := range []string{
		"Temperature=0.50", "TopP=0.90", "CompletionBudget=2048",
		"ToolChoice=auto", "ResponseFormat=json_object", "ThinkingLevel=high",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("options section missing %q: %q", want, s)
		}
	}
}

func TestBuildToolsSectionAndToolCallInfos(t *testing.T) {
	opts := &ChatOptions{Tools: []ToolDef{
		{Name: "search", Description: "web search"},
		{Name: "calc", Description: "calculator"},
	}}
	if s := buildToolsSection(opts); s != "- search: web search\n- calc: calculator" {
		t.Fatalf("tools section mismatch: %q", s)
	}

	infos := responseToolCallInfos([]types.LLMToolCall{{
		ID:       "call_1",
		Function: types.FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
	}})
	if len(infos) != 1 || infos[0].ID != "call_1" ||
		infos[0].FuncName != "search" || infos[0].Arguments != `{"q":"x"}` {
		t.Fatalf("tool call info mismatch: %+v", infos)
	}
	if infos := responseToolCallInfos(nil); infos != nil {
		t.Fatalf("nil input must give nil, got %+v", infos)
	}
}

func TestUsageStringAndTruncate(t *testing.T) {
	u := types.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	if got := usageString(u); !strings.HasPrefix(got, "Prompt: 10, Completion: 5, Total: 15") {
		t.Fatalf("usage mismatch: %q", got)
	}
	long := strings.Repeat("x", 50)
	got := truncateForDebug(long, 10)
	if !strings.HasPrefix(got, "xxxxxxxxxx...(50 chars)") {
		t.Fatalf("truncate mismatch: %q", got)
	}
	if short := truncateForDebug("short", 10); short != "short" {
		t.Fatalf("short string must pass through: %q", short)
	}
}
