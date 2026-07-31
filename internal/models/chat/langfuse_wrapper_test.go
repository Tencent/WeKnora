package chat

import (
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildLangfuseGenerationOutput(t *testing.T) {
	toolCalls := []types.LLMToolCall{{ID: "call_1", Type: "function"}}

	got := buildLangfuseGenerationOutput("", "", "tool_calls", toolCalls)
	want := map[string]interface{}{
		"content":       "",
		"tool_calls":    toolCalls,
		"finish_reason": "tool_calls",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output without reasoning = %#v; want %#v", got, want)
	}

	got = buildLangfuseGenerationOutput("answer", "thinking", "stop", nil)
	want = map[string]interface{}{
		"content":           "answer",
		"tool_calls":        []types.LLMToolCall(nil),
		"finish_reason":     "stop",
		"reasoning_content": "thinking",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("output with reasoning = %#v; want %#v", got, want)
	}
}

func TestSnapshotLangfuseToolCallsKeepsModelArguments(t *testing.T) {
	providerCalls := []types.LLMToolCall{{
		ID:       "call_1",
		Function: types.FunctionCall{Name: "wiki_read_page", Arguments: `{"slugs":["res://0001"]}`},
	}}
	snapshot := snapshotLangfuseToolCalls(providerCalls)
	providerCalls[0].Function.Arguments = `{"slugs":["summary/uuid"]}`

	if got := snapshot[0].Function.Arguments; got != `{"slugs":["res://0001"]}` {
		t.Fatalf("Langfuse snapshot was mutated to %s", got)
	}
}

func TestBuildLangfuseMessagesReasoningContent(t *testing.T) {
	msgs := buildLangfuseMessages([]Message{
		{Role: "assistant", ReasoningContent: "chain of thought", ToolCalls: []ToolCall{{ID: "tc1"}}},
	})
	if len(msgs) != 1 {
		t.Fatalf("len(messages) = %d; want 1", len(msgs))
	}
	if msgs[0]["reasoning_content"] != "chain of thought" {
		t.Fatalf("reasoning_content = %v; want chain of thought", msgs[0]["reasoning_content"])
	}
}

func TestConvertUsageIncludesPromptCacheCounters(t *testing.T) {
	got := convertUsage(&types.TokenUsage{
		PromptTokens: 1000, CompletionTokens: 50, TotalTokens: 1050,
		CacheReadTokens: 800, CacheWriteTokens: 100, CacheMissTokens: 200,
	})
	if got == nil {
		t.Fatal("convertUsage returned nil")
	}
	if got.CacheRead != 800 || got.CacheWrite != 100 || got.CacheMiss != 200 {
		t.Fatalf("cache usage = read:%d write:%d miss:%d", got.CacheRead, got.CacheWrite, got.CacheMiss)
	}
}
