package chat

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
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

func TestPreparedKnowledgeGenerationTraceContainsOnlySafeSummary(t *testing.T) {
	const (
		executionHash = "1234567890abcdef"
		privateInput  = "private-query chunk-private knowledge-private"
		privateOutput = "private-answer tenant-private"
	)
	ctx := langfuse.WithPreparedKnowledgeScope(
		context.Background(),
		executionHash,
	)
	options := buildLangfuseGenerationOptions(
		ctx,
		"chat.completion",
		"private-model",
		"private-model-id",
		[]Message{{Role: "user", Content: privateInput}},
		nil,
		true,
	)
	output := buildLangfuseTraceOutput(
		ctx,
		privateOutput,
		"private-reasoning",
		"stop",
		[]types.LLMToolCall{{ID: "private-tool-call"}},
		true,
	)

	encoded, err := json.Marshal([]interface{}{
		options.Model,
		options.Input,
		options.ModelParameters,
		options.Metadata,
		output,
	})
	if err != nil {
		t.Fatalf("marshal prepared trace: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		privateInput,
		privateOutput,
		"private-model",
		"private-model-id",
		"private-reasoning",
		"private-tool-call",
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf("prepared trace leaked %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, executionHash[:12]) {
		t.Fatalf("prepared trace omitted scope hash prefix: %s", payload)
	}
}

func TestPreparedKnowledgeDebugRecordContainsOnlySafeSummary(t *testing.T) {
	const (
		hashPrefix       = "1234567890ab"
		privateOutput    = "private-answer knowledge-private"
		privateReasoning = "private-reasoning tenant-private"
		privateToolID    = "private-tool-call"
	)
	record := preparedChatDebugRecord(
		"Chat",
		hashPrefix,
		2,
		&types.ChatResponse{
			Content:          privateOutput,
			ReasoningContent: privateReasoning,
			ToolCalls:        []types.LLMToolCall{{ID: privateToolID}},
		},
		nil,
		context.Canceled,
		0,
	)
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal prepared debug record: %v", err)
	}
	payload := string(encoded)
	for _, secret := range []string{
		privateOutput,
		privateReasoning,
		privateToolID,
		context.Canceled.Error(),
	} {
		if strings.Contains(payload, secret) {
			t.Fatalf("prepared debug record leaked %q: %s", secret, payload)
		}
	}
	if !strings.Contains(payload, hashPrefix) {
		t.Fatalf("prepared debug record omitted scope hash prefix: %s", payload)
	}
}
