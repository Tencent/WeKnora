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
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync/atomic"

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

// Scale bounds. cl100k counts Chinese at about one token a character, where
// Qwen and DeepSeek spend about 0.6, so a scale near the floor is expected for
// Chinese sessions on those models. Anything outside the bounds says more
// about a provider's usage report than about its tokenizer.
const (
	MinScale = 0.5
	MaxScale = 1.5
)

// Estimator counts tokens for messages and strings using BPE tokenization.
// It is intended for incremental (delta) estimation between LLM calls; the
// authoritative token count comes from the API's Usage response.
//
// cl100k is not the provider's tokenizer, so text counts are multiplied by a
// scale learned from the provider's own counts (see SetScale). Every budget an
// estimate is compared with is in provider tokens, so one scaled estimator
// keeps them all consistent: history loading, the compaction trigger, and
// compaction's own cut point and summarizer budget.
type Estimator struct {
	codec tokenizer.Codec
	// scale holds float64 bits. The engine recalibrates between rounds, which
	// must not race an estimate a tool is still taking.
	scale atomic.Uint64
}

// NewEstimator creates a token estimator using the cl100k_base encoding, at
// scale 1.
func NewEstimator() (*Estimator, error) {
	codec, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return nil, fmt.Errorf("token: failed to initialize tokenizer: %w", err)
	}
	e := &Estimator{codec: codec}
	e.SetScale(1)
	return e, nil
}

// Scale is the factor text counts are multiplied by.
func (e *Estimator) Scale() float64 {
	return math.Float64frombits(e.scale.Load())
}

// SetScale sets the factor text counts are multiplied by, clamped to
// [MinScale, MaxScale]. A non-positive scale means uncalibrated, which is 1.
func (e *Estimator) SetScale(scale float64) {
	if scale <= 0 || math.IsNaN(scale) {
		scale = 1
	}
	scale = min(max(scale, MinScale), MaxScale)
	e.scale.Store(math.Float64bits(scale))
}

// Unscaled returns an estimator with the same tokenizer at scale 1, for
// measuring the raw count a scale is derived from.
func (e *Estimator) Unscaled() *Estimator {
	u := &Estimator{codec: e.codec}
	u.SetScale(1)
	return u
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
	count := (len(s) + 3) / 4
	if ids, _, err := e.codec.Encode(s); err == nil {
		count = len(ids)
	}
	if scale := e.Scale(); scale != 1 {
		return int(math.Round(float64(count) * scale))
	}
	return count
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
//
// The reasoning artifacts that travel with it are counted too. They are opaque
// to us but not to the provider's billing: an Anthropic thinking signature is a
// few hundred base64 characters, and an OpenAI Responses encrypted reasoning
// item (or an OpenRouter reasoning_details blob) is several kilobytes replayed
// on every assistant turn. Leaving them out reproduces exactly the
// under-measurement described above, one replayed turn at a time.
func (e *Estimator) EstimateMessage(msg *chat.Message) int {
	tokens := perMessageOverhead
	tokens += e.EstimateString(msg.Role)
	tokens += e.EstimateString(msg.Content)
	tokens += e.EstimateString(msg.Name)
	tokens += e.EstimateString(msg.ToolCallID)
	tokens += e.EstimateString(msg.ReasoningContent)
	tokens += e.EstimateReasoningArtifacts(msg)
	tokens += e.estimateImageParts(msg)

	for _, tc := range msg.ToolCalls {
		tokens += e.EstimateString(tc.Function.Name)
		tokens += e.EstimateString(tc.Function.Arguments)
		tokens += e.estimateProviderMetadata(tc.ProviderMetadata)
		tokens += perToolCallOverhead
	}

	return tokens
}

// EstimateReasoningArtifacts counts the opaque provider state replayed with an
// assistant turn. The signature carries a protocol tag that is stripped before
// it goes out, so only the payload after the tag is billed; the metadata is
// sent as-is, one JSON value per namespace key.
func (e *Estimator) EstimateReasoningArtifacts(msg *chat.Message) int {
	tokens := 0
	if sig := msg.ReasoningSignature; sig != "" {
		if _, payload, found := strings.Cut(sig, ":"); found {
			sig = payload
		}
		tokens += e.EstimateString(sig)
	}
	tokens += e.estimateProviderMetadata(msg.ReasoningMetadata)
	return tokens
}

// estimateProviderMetadata counts one opaque provider-state map, one JSON
// value per namespace key, the way it goes out on the wire.
//
// The same accounting applies per tool call as it does per turn: a Gemini
// thought signature and an OpenAI Responses item id ride on the tool call, not
// on the message, and are replayed on every subsequent turn. A signature runs
// to kilobytes, so a round of parallel calls left uncounted under-measures the
// context exactly the way the missing reasoning content did.
func (e *Estimator) estimateProviderMetadata(md map[string]json.RawMessage) int {
	tokens := 0
	for key, raw := range md {
		tokens += e.EstimateString(key)
		tokens += e.EstimateString(string(raw))
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
