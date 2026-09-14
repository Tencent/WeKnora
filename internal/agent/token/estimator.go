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
	perToolCallOverhead = 4
	perToolDefOverhead  = 8

	// estimatedImageTokens is the assumed cost of one image. Providers bill
	// images by tile count, so neither the URL nor the base64 blob is any
	// guide: a short https:// link and a megabyte data URI can cost the same.
	// 1200 tokens is ~4800 characters at the usual 4-chars-per-token heuristic.
	estimatedImageTokens = 1200
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
//
// Every field that goes out on the wire has to be counted here, including the
// ones that are easy to forget. Reasoning content is the expensive one:
// thinking models (DeepSeek V3.2/V4, MiMo) require the previous turns'
// reasoning_content to be sent back verbatim, and it is typically several times
// longer than the visible reply. Omitting it made a 130k context measure as
// 26k — compaction then cut in the wrong place, freed almost nothing, and ran
// again the next round. Reasoning content is counted alongside text and tool
// calls for that reason.
func (e *Estimator) EstimateMessage(msg *chat.Message) int {
	tokens := perMessageOverhead
	tokens += e.EstimateString(msg.Role)
	tokens += e.EstimateString(msg.Content)
	tokens += e.EstimateString(msg.Name)
	tokens += e.EstimateString(msg.ToolCallID)
	tokens += e.EstimateString(msg.ReasoningContent)
	tokens += e.estimateImageParts(msg)

	for _, tc := range msg.ToolCalls {
		tokens += e.EstimateString(tc.Function.Name)
		tokens += e.EstimateString(tc.Function.Arguments)
		tokens += perToolCallOverhead
	}

	return tokens
}

// estimateImageParts counts multimodal content. MultiContent is the assembled
// representation actually sent to the provider, so when it is present the raw
// Images list is the same pictures counted a second time.
func (e *Estimator) estimateImageParts(msg *chat.Message) int {
	if len(msg.MultiContent) == 0 {
		return len(msg.Images) * estimatedImageTokens
	}
	tokens := 0
	for _, part := range msg.MultiContent {
		if part.ImageURL != nil || part.Type == "image_url" {
			tokens += estimatedImageTokens
			continue
		}
		tokens += e.EstimateString(part.Text)
	}
	return tokens
}

// EstimateTools returns the token cost of the tool schemas sent with every
// request. They are part of the prompt the provider bills. They are not part
// of the compaction trigger, which estimates messages only; use this for
// diagnostics and request-budget clamping, not for ShouldCompact.
func (e *Estimator) EstimateTools(tools []chat.Tool) int {
	total := 0
	for _, tool := range tools {
		total += e.EstimateString(tool.Function.Name)
		total += e.EstimateString(tool.Function.Description)
		total += e.EstimateString(string(tool.Function.Parameters))
		total += perToolDefOverhead
	}
	return total
}
