// Package token provides token estimation for LLM context window management.
//
// The primary source of truth for token counts is the model API's Usage field
// (types.TokenUsage), returned with every LLM response. This package serves as
// a supplementary estimator used in two scenarios:
//
//  1. Delta estimation — after an LLM call, new messages (assistant reply +
//     tool results) are appended before the next call. The Estimator computes
//     the token cost of these new messages so the engine can decide whether
//     context compression is needed without making an extra API round-trip.
//
//  2. First-round fallback — on the very first round of a session, no prior
//     API Usage is available, so the Estimator provides a full estimate.
//
// The encoding used (cl100k_base) is an approximation. Different model families
// use different tokenizers, so the numbers will not be exact for non-OpenAI
// models. This is acceptable because the estimate only needs to be close enough
// to trigger compression at roughly the right time; over- or under-estimating
// by a small margin is corrected on the next API call.
package token

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/tiktoken-go/tokenizer"
)

const (
	perMessageOverhead  = 3
	perConversationTail = 3
)

// Estimator counts tokens for messages and strings using BPE tokenization.
// It is intended for incremental (delta) estimation between LLM calls; the
// authoritative token count comes from the API's Usage response.
type Estimator struct {
	codec tokenizer.Codec
}

// NewEstimator creates a token estimator using the cl100k_base encoding.
func NewEstimator() (*Estimator, error) {
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return nil, fmt.Errorf("token: failed to initialize tokenizer: %w", err)
	}
	return &Estimator{codec: codec}, nil
}

// EstimateMessages returns the estimated token count for a slice of messages.
// Prefer using API Usage for the full context and this method only for deltas.
func (e *Estimator) EstimateMessages(messages []chat.Message) int {
	total := 0
	for i := range messages {
		total += e.EstimateMessage(&messages[i])
	}
	total += perConversationTail
	return total
}

// EstimateString returns the token count for a string using BPE tokenization.
func (e *Estimator) EstimateString(s string) int {
	if len(s) == 0 {
		return 0
	}
	ids, _, err := e.codec.Encode(s)
	if err != nil {
		return (len(s) + 3) / 4
	}
	return len(ids)
}

// EstimateMessage returns the token count for a single message.
func (e *Estimator) EstimateMessage(msg *chat.Message) int {
	tokens := perMessageOverhead
	tokens += e.EstimateString(msg.Role)
	tokens += e.EstimateString(msg.Content)
	tokens += e.EstimateString(msg.Name)

	for _, tc := range msg.ToolCalls {
		tokens += e.EstimateString(tc.Function.Name)
		tokens += e.EstimateString(tc.Function.Arguments)
		tokens += 4
	}

	return tokens
}
