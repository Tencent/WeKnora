// Package token provides conservative, model-aware token estimation for LLM
// context-window management and provider quota admission.
package token

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/tiktoken-go/tokenizer"
)

const (
	perMessageOverhead   = 3
	perConversationTail  = 3
	perToolCallOverhead  = 6
	lowDetailImageCost   = 85
	autoDetailImageCost  = 256
	highDetailImageCost  = 1024
	defaultOutputReserve = 4096
)

// RequestEstimate describes the local preflight count used before a provider
// request. InputTokens never includes the reserved completion budget.
type RequestEstimate struct {
	InputTokens          int
	ReservedOutputTokens int
	Source               string
}

// Estimator counts the complete request shape, not only message text. The
// tokenizer is selected from the configured model name when possible and
// falls back to cl100k_base for OpenAI-compatible/custom providers.
type Estimator struct {
	codec  tokenizer.Codec
	source string
}

// NewEstimator preserves the old constructor for callers without model
// metadata. New agent paths should use NewEstimatorForModel.
func NewEstimator() (*Estimator, error) {
	return newEstimator(tokenizer.Cl100kBase, "tokenizer:cl100k_base")
}

// NewEstimatorForModel chooses the closest tokenizer supported by the local
// tokenizer library. Unknown/provider-specific models conservatively use
// cl100k_base; Source makes clear that this is not an authoritative count.
func NewEstimatorForModel(modelName string) (*Estimator, error) {
	name := strings.ToLower(strings.TrimSpace(modelName))
	if name != "" {
		if codec, err := tokenizer.ForModel(tokenizer.Model(name)); err == nil {
			return &Estimator{codec: codec, source: "tokenizer:model:" + name}, nil
		}
	}

	encoding := tokenizer.Cl100kBase
	if usesO200k(name) {
		encoding = tokenizer.O200kBase
	}
	return newEstimator(encoding, "tokenizer:fallback:"+string(encoding))
}

func newEstimator(encoding tokenizer.Encoding, source string) (*Estimator, error) {
	codec, err := tokenizer.Get(encoding)
	if err != nil {
		return nil, fmt.Errorf("token: failed to initialize %s tokenizer: %w", encoding, err)
	}
	return &Estimator{codec: codec, source: source}, nil
}

func usesO200k(name string) bool {
	for _, prefix := range []string{"gpt-5", "gpt-4.1", "gpt-4o", "chatgpt-4o", "o1", "o3", "o4"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// EstimateRequest counts all input fields that are serialized into a chat
// request: messages, multimodal parts, tools, tool choice, and response format.
func (e *Estimator) EstimateRequest(messages []chat.Message, opts *chat.ChatOptions) RequestEstimate {
	total := e.EstimateMessages(messages)
	reserved := defaultOutputReserve
	if opts != nil {
		if len(opts.Tools) > 0 {
			total += e.estimateJSON(opts.Tools) + 8
		}
		total += e.EstimateString(opts.ToolChoice)
		if len(opts.Format) > 0 {
			total += e.estimateJSON(opts.Format) + 4
		}
		if opts.MaxCompletionTokens > 0 {
			reserved = opts.MaxCompletionTokens
		} else if opts.MaxTokens > 0 {
			reserved = opts.MaxTokens
		}
	}
	return RequestEstimate{InputTokens: total, ReservedOutputTokens: reserved, Source: e.source}
}

// EstimateMessages returns the estimated token count for a complete message
// list. It includes the conversation framing tail exactly once.
func (e *Estimator) EstimateMessages(messages []chat.Message) int {
	total := 0
	for i := range messages {
		total += e.EstimateMessage(&messages[i])
	}
	if len(messages) > 0 {
		total += perConversationTail
	}
	return total
}

// EstimateString returns the local tokenizer count for s.
func (e *Estimator) EstimateString(s string) int {
	if s == "" {
		return 0
	}
	ids, _, err := e.codec.Encode(s)
	if err != nil {
		return (len([]rune(s)) + 2) / 3
	}
	return len(ids)
}

// EstimateMessage counts every request-bearing field on a message. Image
// payload bytes are deliberately not tokenized; providers charge images by
// tiles/detail rather than by base64 length.
func (e *Estimator) EstimateMessage(msg *chat.Message) int {
	if msg == nil {
		return 0
	}
	tokens := perMessageOverhead
	tokens += e.EstimateString(msg.Role)
	tokens += e.EstimateString(msg.Content)
	tokens += e.EstimateString(msg.Name)
	tokens += e.EstimateString(msg.ToolCallID)
	tokens += e.EstimateString(msg.ReasoningContent)

	for _, part := range msg.MultiContent {
		tokens += e.EstimateString(part.Type)
		tokens += e.EstimateString(part.Text)
		if part.ImageURL != nil {
			tokens += imageTokenCost(part.ImageURL.Detail)
		}
	}
	for range msg.Images {
		tokens += autoDetailImageCost
	}

	for _, tc := range msg.ToolCalls {
		tokens += e.EstimateString(tc.ID)
		tokens += e.EstimateString(tc.Type)
		tokens += e.EstimateString(tc.Function.Name)
		tokens += e.EstimateString(tc.Function.Arguments)
		tokens += e.estimateJSON(tc.ProviderMetadata)
		tokens += perToolCallOverhead
	}
	return tokens
}

func imageTokenCost(detail string) int {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "low":
		return lowDetailImageCost
	case "high":
		return highDetailImageCost
	default:
		return autoDetailImageCost
	}
}

func (e *Estimator) estimateJSON(value any) int {
	b, err := json.Marshal(value)
	if err != nil || len(b) == 0 || string(b) == "null" || string(b) == "{}" || string(b) == "[]" {
		return 0
	}
	return e.EstimateString(string(b))
}

// InputBudget converts a model context window into the maximum admissible
// input after reserving output tokens and provider/tokenizer framing drift.
func InputBudget(contextWindow, reservedOutput int) int {
	if contextWindow <= 0 {
		return 0
	}
	if reservedOutput <= 0 {
		reservedOutput = defaultOutputReserve
	}
	safety := contextWindow / 50 // 2% drift allowance.
	if safety < 1024 {
		safety = 1024
	}
	budget := contextWindow - reservedOutput - safety
	if budget < 1 {
		return 1
	}
	return budget
}
