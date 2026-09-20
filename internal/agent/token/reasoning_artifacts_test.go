package token

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

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
