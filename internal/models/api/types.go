// Package api holds the protocol-neutral request model shared by every wire
// protocol implementation (OpenAI Chat Completions, OpenAI Responses,
// Anthropic Messages, Google Generative AI, Ollama) plus the helpers those
// implementations share: SSE reading, the SSRF-safe transport, streaming
// tool-call assembly, prompt-cache bookkeeping and usage logging.
//
// Protocol packages live under api/<protocol>; vendor facts live in
// internal/models/catalog and internal/models/providers. This package must not
// import either of them.
package api

import (
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// API identifies a wire protocol. It is the value of the "api" field in a
// vendor's models.json and decides which protocol package talks to the
// endpoint.
type API string

const (
	// APIOpenAICompletions is the OpenAI Chat Completions protocol, the
	// lingua franca of almost every third-party vendor.
	APIOpenAICompletions API = "openai-completions"
	// APIOpenAIResponses is the OpenAI Responses protocol.
	APIOpenAIResponses API = "openai-responses"
	// APIAnthropicMessages is the Anthropic Messages protocol, also exposed by
	// MiniMax, Zhipu and Moonshot as a compatibility surface.
	APIAnthropicMessages API = "anthropic-messages"
	// APIGoogleGenerativeAI is the native Gemini generateContent protocol.
	APIGoogleGenerativeAI API = "google-generative-ai"
	// APIOllama is the local Ollama chat protocol.
	APIOllama API = "ollama"
)

// Known reports whether the API value names a protocol this build ships.
func (a API) Known() bool {
	switch a {
	case APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages, APIGoogleGenerativeAI, APIOllama:
		return true
	}
	return false
}

// Tool represents a function/tool definition
type Tool struct {
	Type     string      `json:"type"` // "function"
	Function FunctionDef `json:"function"`
}

// FunctionDef represents a function definition
type FunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// MessageContentPart represents a part of multi-content message
type MessageContentPart struct {
	Type     string    `json:"type"`                // "text" or "image_url"
	Text     string    `json:"text,omitempty"`      // For type="text"
	ImageURL *ImageURL `json:"image_url,omitempty"` // For type="image_url"
}

// ImageURL represents the image URL structure
type ImageURL struct {
	URL    string `json:"url"`              // URL or base64 data URI
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// MessageKind marks messages the engine synthesized rather than received from
// the user or the model. Compaction needs to tell its own summary apart from a
// real user turn: a summary that looks like ordinary history gets fed back into
// the next summarization pass and degrades into a summary of a summary.
type MessageKind string

// MessageKindCompactionSummary marks the message that replaces compacted
// history. It carries the `user` role because that is where providers expect
// conversation history, so the role alone cannot identify it.
const MessageKindCompactionSummary MessageKind = "compaction_summary"

// Message 表示聊天消息
type Message struct {
	Role         string               `json:"role"`                    // 角色：system, user, assistant, tool
	Content      string               `json:"content"`                 // 消息内容
	MultiContent []MessageContentPart `json:"multi_content,omitempty"` // 多内容消息（文本+图片）
	Name         string               `json:"name,omitempty"`          // Function/tool name (for tool role)
	ToolCallID   string               `json:"tool_call_id,omitempty"`  // Tool call ID (for tool role)
	ToolCalls    []ToolCall           `json:"tool_calls,omitempty"`    // Tool calls (for assistant role)
	// Images are image URLs for multimodal input (current user message only).
	Images []string `json:"images,omitempty"`
	// ReasoningContent 是 assistant 推理类模型上一轮输出的思考内容。部分供应商
	// （MiMo、DeepSeek V3.2+、Kimi K2.5+）要求多轮对话中把 assistant 的
	// reasoning_content 原样回传，否则会以 400 拒绝请求；其他不要求的供应商会
	// 忽略未知字段。Anthropic 则要求把 thinking block 连同签名一起回放。
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ReasoningSignature is the provider-issued signature that must accompany
	// ReasoningContent when it is replayed (Anthropic thinking blocks, Gemini
	// thought signatures on text parts). Empty when the provider issues none.
	ReasoningSignature string `json:"reasoning_signature,omitempty"`
	// ReasoningMetadata carries opaque provider state that must round-trip
	// with the assistant turn but has no protocol-neutral representation:
	// OpenAI Responses reasoning items (encrypted_content), OpenRouter
	// reasoning_details, and so on. Keyed by protocol / vendor namespace.
	ReasoningMetadata types.ProviderMetadata `json:"reasoning_metadata,omitempty"`
	// Kind is engine-internal bookkeeping. `json:"-"` keeps it off the wire:
	// providers reject unknown message fields on some endpoints, and this one
	// means nothing to them anyway.
	Kind MessageKind `json:"-"`
	// TurnID is the stored assistant message whose turn this history message
	// was replayed from. It is empty for the live turn and for messages the
	// engine synthesized. Compaction uses it to tell whether a summary ends
	// exactly on a stored turn, the only kind it can persist for later turns.
	// Engine-internal like Kind, and kept off the wire for the same reason.
	TurnID string `json:"-"`
}

// ToolCall represents a tool call in a message
type ToolCall struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type"` // "function"
	Function         FunctionCall           `json:"function"`
	ProviderMetadata types.ToolCallMetadata `json:"provider_metadata,omitempty"`
}

// FunctionCall represents a function call
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ReasoningEffort is the protocol-neutral thinking level. Vendors map it onto
// their own vocabulary through a ThinkingLevelMap; the protocol package then
// emits whichever wire field the vendor documents (reasoning_effort,
// thinking.type, enable_thinking, thinkingConfig, ...).
type ReasoningEffort string

const (
	// ReasoningOff disables extended thinking where the model allows it.
	ReasoningOff ReasoningEffort = "off"
	// ReasoningAuto enables thinking but leaves the intensity to the provider
	// default. This is what the legacy boolean `thinking: true` maps to.
	ReasoningAuto ReasoningEffort = "auto"
	// ReasoningMinimal is the weakest graded level.
	ReasoningMinimal ReasoningEffort = "minimal"
	// ReasoningLow is the low graded level.
	ReasoningLow ReasoningEffort = "low"
	// ReasoningMedium is the medium graded level.
	ReasoningMedium ReasoningEffort = "medium"
	// ReasoningHigh is the high graded level.
	ReasoningHigh ReasoningEffort = "high"
	// ReasoningXHigh is the extra-high graded level (OpenAI, Zhipu).
	ReasoningXHigh ReasoningEffort = "xhigh"
	// ReasoningMax is the strongest graded level.
	ReasoningMax ReasoningEffort = "max"
)

// ReasoningLadder lists the graded levels from weakest to strongest. Off and
// Auto are deliberately excluded: they are switches, not intensities.
var ReasoningLadder = []ReasoningEffort{
	ReasoningMinimal, ReasoningLow, ReasoningMedium, ReasoningHigh, ReasoningXHigh, ReasoningMax,
}

// AllReasoningEfforts is every accepted value, in UI order.
var AllReasoningEfforts = append([]ReasoningEffort{ReasoningOff, ReasoningAuto}, ReasoningLadder...)

// ParseReasoningEffort validates a user-provided level. Empty input yields
// ("", true) meaning "unset: let the model default apply".
func ParseReasoningEffort(s string) (ReasoningEffort, bool) {
	if s == "" {
		return "", true
	}
	for _, level := range AllReasoningEfforts {
		if string(level) == s {
			return level, true
		}
	}
	// Accept a few aliases the legacy boolean UI and provider docs use.
	switch s {
	case "none", "false", "disabled":
		return ReasoningOff, true
	case "true", "enabled", "default", "on":
		return ReasoningAuto, true
	}
	return "", false
}

// Enabled reports whether the level asks for thinking to be on.
func (r ReasoningEffort) Enabled() bool {
	return r != "" && r != ReasoningOff
}

// Graded reports whether the level is one of the intensity rungs.
func (r ReasoningEffort) Graded() bool {
	for _, level := range ReasoningLadder {
		if level == r {
			return true
		}
	}
	return false
}

// ThinkingLevelMap maps protocol-neutral levels to a vendor's own vocabulary.
//
// Semantics mirror Pi's thinkingLevelMap:
//   - a missing key means "supported, pass the level name through" for
//     minimal/low/medium/high and "unsupported" for xhigh/max;
//   - a null value marks the level as unsupported (the caller clamps to the
//     nearest supported rung);
//   - a string value is what the vendor expects on the wire.
//
// The special key "off" may be mapped to null to state that thinking cannot
// be switched off (always-on reasoning models).
type ThinkingLevelMap map[ReasoningEffort]*string

// Supports reports whether the level is usable for this model.
func (m ThinkingLevelMap) Supports(level ReasoningEffort) bool {
	if v, ok := m[level]; ok {
		return v != nil
	}
	switch level {
	case ReasoningXHigh, ReasoningMax:
		return false
	case ReasoningOff, ReasoningAuto:
		return true
	}
	return true
}

// Value returns the vendor value for a supported level. Unmapped levels
// pass through under their own name.
func (m ThinkingLevelMap) Value(level ReasoningEffort) string {
	if v, ok := m[level]; ok && v != nil {
		return *v
	}
	return string(level)
}

// Clamp returns the nearest supported graded level: first upward, then
// downward, mirroring Pi's clampThinkingLevel. Off/Auto are returned as-is
// (Off may still be unsupported; callers check Supports(ReasoningOff)).
func (m ThinkingLevelMap) Clamp(level ReasoningEffort) ReasoningEffort {
	if !level.Graded() {
		return level
	}
	if m.Supports(level) {
		return level
	}
	idx := -1
	for i, l := range ReasoningLadder {
		if l == level {
			idx = i
			break
		}
	}
	for i := idx + 1; i < len(ReasoningLadder); i++ {
		if m.Supports(ReasoningLadder[i]) {
			return ReasoningLadder[i]
		}
	}
	for i := idx - 1; i >= 0; i-- {
		if m.Supports(ReasoningLadder[i]) {
			return ReasoningLadder[i]
		}
	}
	return ReasoningAuto
}

// SupportedLevels lists the levels this map accepts, in UI order.
func (m ThinkingLevelMap) SupportedLevels() []ReasoningEffort {
	out := make([]ReasoningEffort, 0, len(AllReasoningEfforts))
	for _, level := range AllReasoningEfforts {
		if m.Supports(level) {
			out = append(out, level)
		}
	}
	return out
}

// StringPtr is a small helper for building ThinkingLevelMap literals.
func StringPtr(s string) *string { return &s }

// TagSignature records which protocol issued a reasoning signature. Stored
// signatures are opaque to every other provider, so they must only be
// replayed to the protocol that produced them.
func TagSignature(protocol API, signature string) string {
	if signature == "" {
		return ""
	}
	return string(protocol) + ":" + signature
}

// SignatureFor returns the raw signature when it was issued by protocol, and
// "" for signatures from another protocol or without a protocol tag.
func SignatureFor(protocol API, tagged string) string {
	prefix := string(protocol) + ":"
	if !strings.HasPrefix(tagged, prefix) {
		return ""
	}
	return strings.TrimPrefix(tagged, prefix)
}
