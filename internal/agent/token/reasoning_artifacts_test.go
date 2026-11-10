package token

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEstimateMessageCountsAnthropicThinkingOnce(t *testing.T) {
	est, err := NewEstimator()
	require.NoError(t, err)
	thinking := strings.Repeat("synthetic reasoning ", 1000)
	signature := strings.Repeat("c2ln", 200)
	blocks, err := json.Marshal([]map[string]string{
		{"type": "thinking", "thinking": thinking, "signature": signature},
		{"type": "redacted_thinking", "data": "opaque-block"},
	})
	require.NoError(t, err)
	canonical := chat.Message{Role: "assistant", ReasoningMetadata: types.ProviderMetadata{
		"anthropic_thinking_blocks": blocks,
	}}
	duplicated := canonical
	duplicated.ReasoningContent = thinking
	duplicated.ReasoningSignature = "anthropic-messages:" + signature
	duplicated.ReasoningMetadata = types.ProviderMetadata{
		"anthropic_thinking_blocks":   blocks,
		"anthropic_redacted_thinking": json.RawMessage(`["opaque-block"]`),
	}
	require.Equal(t, est.EstimateMessage(&canonical), est.EstimateMessage(&duplicated),
		"the provider replays the canonical blocks instead of the legacy display fields")
	require.Equal(t, est.EstimateReasoningArtifacts(&canonical), est.EstimateReasoningArtifacts(&duplicated))
	require.Greater(t, est.EstimateMessage(&duplicated), est.EstimateString(thinking),
		"the signed thinking and opaque artifacts must still be counted")
}

func TestEstimateMessageInvalidAnthropicMetadataKeepsLegacyReasoning(t *testing.T) {
	est, err := NewEstimator()
	require.NoError(t, err)
	for _, raw := range []string{`{}`, `[{"thinking":42}]`, `invalid`} {
		t.Run(raw, func(t *testing.T) {
			plain := chat.Message{Role: "assistant", ReasoningMetadata: types.ProviderMetadata{
				"anthropic_thinking_blocks": json.RawMessage(raw),
			}}
			legacy := plain
			legacy.ReasoningContent = strings.Repeat("legacy reasoning ", 100)
			legacy.ReasoningSignature = "anthropic-messages:signature"
			require.Equal(t, est.EstimateString(legacy.ReasoningContent)+est.EstimateString("signature"),
				est.EstimateMessage(&legacy)-est.EstimateMessage(&plain),
				"malformed canonical metadata falls back to the legacy fields on the wire")
		})
	}
}

// TestEstimateMessageCountsReasoningArtifacts pins the fix for the compaction
// trigger under-measuring replayed assistant turns. An OpenAI Responses
// encrypted reasoning item or an Anthropic signature rides on every replayed
// assistant message; leaving it out of the estimate is the same class of bug
// the package comment describes for reasoning_content.
func TestEstimateMessageCountsReasoningArtifacts(t *testing.T) {
	est, err := NewEstimator()
	if err != nil {
		t.Fatalf("NewEstimator: %v", err)
	}

	plain := chat.Message{Role: "assistant", Content: "ok"}
	blob, err := json.Marshal([]string{strings.Repeat("QUJDRA", 800)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	withArtifacts := plain
	withArtifacts.ReasoningSignature = "anthropic-messages:" + strings.Repeat("c2ln", 200)
	withArtifacts.ReasoningMetadata = types.ProviderMetadata{"openai_responses_reasoning": blob}

	base := est.EstimateMessage(&plain)
	full := est.EstimateMessage(&withArtifacts)
	if full <= base {
		t.Fatalf("reasoning artifacts not counted: base=%d full=%d", base, full)
	}
	// The blobs are kilobytes, so the delta must be substantial rather than
	// the handful of tokens a key name alone would add.
	if delta := full - base; delta < 500 {
		t.Errorf("artifact cost looks under-counted: delta=%d", delta)
	}
}

// TestEstimateReasoningArtifactsSkipsProtocolTag checks the stored signature's
// "<protocol>:" prefix is not billed: api.SignatureFor strips it before the
// signature goes out on the wire.
func TestEstimateReasoningArtifactsSkipsProtocolTag(t *testing.T) {
	est, err := NewEstimator()
	if err != nil {
		t.Fatalf("NewEstimator: %v", err)
	}
	payload := strings.Repeat("c2ln", 50)
	tagged := &chat.Message{Role: "assistant", ReasoningSignature: "anthropic-messages:" + payload}
	untagged := &chat.Message{Role: "assistant", ReasoningSignature: payload}
	if got, want := est.EstimateReasoningArtifacts(tagged), est.EstimateReasoningArtifacts(untagged); got != want {
		t.Errorf("protocol tag billed: tagged=%d untagged=%d", got, want)
	}
}

// TestEstimateReasoningArtifactsEmpty keeps the common case free of cost.
func TestEstimateReasoningArtifactsEmpty(t *testing.T) {
	est, err := NewEstimator()
	if err != nil {
		t.Fatalf("NewEstimator: %v", err)
	}
	if got := est.EstimateReasoningArtifacts(&chat.Message{Role: "user", Content: "hi"}); got != 0 {
		t.Errorf("expected 0 for a message without artifacts, got %d", got)
	}
}

// Gemini thought signatures and OpenAI Responses item ids ride on the tool
// call, not on the message, and are replayed on every later turn. Counting
// only name+arguments under-measured a parallel tool round by kilobytes —
// the same failure mode as the message-level artifacts above.
func TestEstimateMessageCountsToolCallProviderMetadata(t *testing.T) {
	est, err := NewEstimator()
	if err != nil {
		t.Fatalf("NewEstimator: %v", err)
	}

	signature, err := json.Marshal(strings.Repeat("CtoBCr8B", 400))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	call := chat.ToolCall{
		ID: "call_1", Type: "function",
		Function: chat.FunctionCall{Name: "search", Arguments: `{"q":"x"}`},
	}
	plain := chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{call}}

	withMetadata := call
	withMetadata.ProviderMetadata = types.ToolCallMetadata{"thought_signature": signature}
	annotated := chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{withMetadata}}

	base := est.EstimateMessage(&plain)
	full := est.EstimateMessage(&annotated)
	if full <= base {
		t.Fatalf("tool-call provider metadata not counted: base=%d full=%d", base, full)
	}
	// It must be accounted like the message-level artifacts: the payload, not
	// just the namespace key.
	if delta := full - base; delta < 500 {
		t.Errorf("tool-call metadata looks under-counted: delta=%d", delta)
	}
}

// Every call in a parallel round carries its own signature, so the cost has
// to scale with the number of calls.
func TestEstimateMessageCountsMetadataPerToolCall(t *testing.T) {
	est, err := NewEstimator()
	if err != nil {
		t.Fatalf("NewEstimator: %v", err)
	}
	md := types.ToolCallMetadata{"thought_signature": json.RawMessage(`"` + strings.Repeat("CtoB", 200) + `"`)}
	call := chat.ToolCall{
		ID: "c", Type: "function",
		Function: chat.FunctionCall{Name: "f", Arguments: "{}"}, ProviderMetadata: md,
	}
	one := chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{call}}
	two := chat.Message{Role: "assistant", ToolCalls: []chat.ToolCall{call, call}}

	single := est.EstimateMessage(&one) - est.EstimateMessage(&chat.Message{Role: "assistant"})
	double := est.EstimateMessage(&two) - est.EstimateMessage(&chat.Message{Role: "assistant"})
	if double < single*2-perToolCallOverhead {
		t.Errorf("metadata counted once for a parallel round: single=%d double=%d", single, double)
	}
}
