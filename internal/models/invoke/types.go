// Package invoke is the unified model-invocation layer (design v2 §6): a
// strategy registry of per-vendor adapters, one HTTP executor, and a single
// entry surface (Chat/ChatStream/Embed/Rerank/Transcribe/List) that upstream
// callers depend on instead of any vendor SDK or wire format.
package invoke

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Credentials carries the three fixed credential slots (design §6.8). Storage
// treats them as opaque values; semantics belong to the adapter (APIKey for
// Bearer/api-key style auth, AppID+AppSecret for body-signing vendors such as
// WeKnoraCloud).
type Credentials struct {
	APIKey    string
	AppID     string
	AppSecret string
}

// Usage is the token-consumption view returned by every facet.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// Prompt-cache detail (v1 types.TokenUsage subset). Zero values mean
	// "not reported" — adapters only fill these when the vendor actually
	// carries native counters (DeepSeek hit/miss, Anthropic read/creation,
	// OpenAI prompt_tokens_details).
	CacheReadTokens  int  `json:"cache_read_tokens,omitempty"`  // deepseek prompt_cache_hit_tokens
	CacheWriteTokens int  `json:"cache_write_tokens,omitempty"` // anthropic cache_creation_input_tokens
	CacheMissTokens  int  `json:"cache_miss_tokens,omitempty"`  // deepseek prompt_cache_miss_tokens
	CacheReported    bool `json:"cache_reported,omitempty"`
}

// usageToTypes maps onto the wire-facing types.TokenUsage, recomputing the
// cache status from the detail counters exactly as v1 SetPromptCacheUsage did.
func (u *Usage) usageToTypes() *types.TokenUsage {
	if u == nil {
		return nil
	}
	out := &types.TokenUsage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
		TotalTokens:      u.TotalTokens,
	}
	if u.CacheReported {
		out.SetPromptCacheUsage(u.CacheReadTokens, u.CacheWriteTokens, u.CacheMissTokens, true)
	}
	return out
}

// Prompt-cache retention preferences (values match v1 semantics).
const (
	CacheRetentionNone  = "none"
	CacheRetentionShort = "short"
	CacheRetentionLong  = "long"
)

// --- Neutral message model (design §6.1): no OpenAI assumptions ---

// Part is one content fragment of a message.
type Part struct {
	Text  string
	Image *ImageRef
	Audio *AudioRef
}

// ImageRef mirrors v1 ImageURL semantics: URL or base64 data URI, Detail is
// "auto"/"low"/"high".
type ImageRef struct {
	URL    string
	Detail string
}

// AudioRef carries inline audio bytes for ASR-style inputs.
type AudioRef struct {
	Data   []byte
	Format string
}

// FunctionCall is a named JSON-argument call.
type FunctionCall struct {
	Name      string
	Arguments string // JSON string
}

// ToolCall is a tool invocation emitted by (or replayed to) the model.
type ToolCall struct {
	ID               string
	Type             string // "function"
	Function         FunctionCall
	ProviderMetadata types.ToolCallMetadata
}

// ToolDef declares a callable tool.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// MessageKind marks messages the engine synthesized rather than received from
// the user or the model. Compaction needs to tell its own summary apart from a
// real user turn: a summary that looks like ordinary history gets fed back into
// the next summarization pass and degrades into a summary of a summary.
// (Ported from v1 chat.MessageKind — engine-internal bookkeeping, never wired.)
type MessageKind string

// MessageKindCompactionSummary marks the message that replaces compacted
// history. It carries the `user` role because that is where providers expect
// conversation history, so the role alone cannot identify it.
const MessageKindCompactionSummary MessageKind = "compaction_summary"

// Role is the neutral message-role vocabulary (the internal standard). Vendor
// vocabularies diverge (anthropic has no system role; openai adds developer);
// mapping Role onto them is an adapter concern — every adapter maps it via a
// `//exhaustive:enforce` switch so a new constant forces an explicit decision
// in every adapter. It is a typed string (not int): Message is JSON-serialized
// into session storage and compaction records, and this keeps the on-disk
// representation identical to v1's plain string.
type Role string

const (
	// RoleSystem is the system prompt role.
	RoleSystem Role = "system"
	// RoleUser is the human turn role.
	RoleUser Role = "user"
	// RoleAssistant is the model turn role.
	RoleAssistant Role = "assistant"
	// RoleTool is the tool-result role (carries ToolCallID).
	RoleTool Role = "tool"
)

// Message is the neutral message carrier. Content is part-sliced (no pure-text
// assumption); ReasoningContent must round-trip verbatim for strict vendors
// (DeepSeek V3.2/V4, MiMo) that 400 on missing reasoning replay.
type Message struct {
	Role             Role
	Name             string // tool role name (weknoracloud TransformMessages relies on it)
	Content          []Part
	ToolCalls        []ToolCall
	ToolCallID       string
	ReasoningContent string
	// Kind is engine-internal bookkeeping (compaction summary marker). It must
	// never reach the wire: `json:"-"` keeps it out of any serialization and
	// adapters do not read it.
	Kind MessageKind `json:"-"`
}

// TextMessage builds a message from plain text — the v1 chat.Message string
// Content shape (implicit single text part), now explicit on the part model.
func TextMessage(role Role, text string) Message {
	return Message{Role: role, Content: []Part{{Text: text}}}
}

// Text returns the message's plain-text view: all text parts concatenated.
// Image/audio-only or empty messages safely return "" (v1 string Content
// semantics: "" when no text).
func (m *Message) Text() string {
	var b strings.Builder
	for _, p := range m.Content {
		b.WriteString(p.Text)
	}
	return b.String()
}

// HasImages reports whether any message part carries an image.
func HasImages(msgs []Message) bool {
	for i := range msgs {
		for _, p := range msgs[i].Content {
			if p.Image != nil {
				return true
			}
		}
	}
	return false
}

// StripImages returns a copy of msgs with all image parts removed (the
// multimodal degradation retry rebuilds the request from this).
func StripImages(msgs []Message) []Message {
	cleaned := make([]Message, len(msgs))
	for i := range msgs {
		parts := make([]Part, 0, len(msgs[i].Content))
		for _, p := range msgs[i].Content {
			if p.Image == nil {
				parts = append(parts, p)
			}
		}
		cleaned[i] = msgs[i]
		cleaned[i].Content = parts
	}
	return cleaned
}

// --- Chat facet types (design §6.1) ---

// ChatOptions is the internal chat call surface. MaxCompletionTokens is the
// ONLY completion-budget field (user ruling: MaxTokens is the same thing); the
// wire field name (max_tokens vs max_completion_tokens, folding, clamping) is
// adapter knowledge. Sampling params are value types — zero means "do not
// send" (v1 ChatOptions semantics, deliberately not pointerized).
type ChatOptions struct {
	Messages            []Message
	Tools               []ToolDef
	ToolChoice          string // "auto", "required", "none", or a tool name
	MaxCompletionTokens int    // internal single budget field

	Temperature       float64
	TopP              float64
	FrequencyPenalty  float64
	PresencePenalty   float64
	Seed              int
	ParallelToolCalls *bool
	Format            json.RawMessage // structured output (json_schema form)

	// PromptCacheKey is the provider routing key (openai-family
	// prompt_cache_key + session affinity); empty means no routing key is
	// sent — callers that want session affinity pass the session ID
	// explicitly (there is no implicit context fallback; openai_wire.go
	// receives ChatOptions.PromptCacheKey only). CacheRetention controls
	// prompt-cache TTL ("none"/"short"/"long"; empty = short).
	PromptCacheKey string
	CacheRetention string

	// Thinking: nil = send nothing; &false = off (forced-thinking models
	// ignore); &true = on with ThinkingLevel. ThinkingLevel is the SINGLE
	// level for this call (winner of the five-tier chain); level sets live on
	// the model record (SelectedLevels) and provider caps (SupportedLevels).
	Thinking      *bool
	ThinkingLevel string

	Stream bool
}

// ChatResponse is the non-stream chat result.
type ChatResponse struct {
	Content          string
	ToolCalls        []ToolCall
	Usage            Usage
	FinishReason     string
	Thinking         *string
	ReasoningContent string // multi-turn replay channel for strict vendors
}

// StreamKind classifies a StreamEvent; the entry maps each kind to exactly one
// types.ResponseType (mechanical, no vendor knowledge).
type StreamKind string

const (
	// StreamKindAnswer marks answer text deltas produced by adapters
	// (see StreamKind doc above); the remaining kinds follow the same
	// mapping rule per their names.
	StreamKindAnswer StreamKind = "answer"
	// StreamKindThinking marks reasoning/thinking deltas.
	StreamKindThinking StreamKind = "thinking"
	// StreamKindToolCall marks incremental tool-call fragments.
	StreamKindToolCall StreamKind = "tool_call"
	// StreamKindUsage marks a terminal usage report.
	StreamKindUsage StreamKind = "usage"
	// StreamKindError marks a mid-stream provider error.
	StreamKindError StreamKind = "error"
)

// ContentDelta is an incremental text fragment.
type ContentDelta struct {
	Text string
}

// ToTypes maps the neutral result onto the session-level response type
// (ports v1 parseCompletionResponse's caller-side shape: ToolCalls →
// types.LLMToolCall, Usage → types.TokenUsage with the prompt-cache detail
// recomputed from the four Usage cache counters — v1
// applyRawPromptCacheUsage/SetPromptCacheUsage semantics, DeepSeek/Anthropic
// counters included).
func (r *ChatResponse) ToTypes() *types.ChatResponse {
	out := &types.ChatResponse{
		Content:          r.Content,
		ReasoningContent: r.ReasoningContent,
		FinishReason:     r.FinishReason,
		Usage:            *r.Usage.usageToTypes(),
	}
	out.ToolCalls = toLLMToolCalls(r.ToolCalls)
	return out
}

// ToolCallDelta is an incremental tool-call fragment (index-keyed assembly).
type ToolCallDelta struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}

// FinishInfo closes a stream. Incomplete mirrors FinishReasonIncomplete
// semantics (broken stream); ToolCalls carries the partial tool calls that
// error chunks may still deliver.
type FinishInfo struct {
	FinishReason string
	Incomplete   bool
	ToolCalls    []ToolCall
}

// StreamEvent is the internal standard stream event produced by adapter
// TranslateStreamEvent and consumed by the entry's mechanical mapping.
type StreamEvent struct {
	Kind          StreamKind
	Delta         *ContentDelta
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	Done          *FinishInfo
}

// StreamChunk is one demuxed wire event produced by the executor-side
// demultiplexer (SSE or NDJSON). Event is the SSE event name ("" for plain
// data frames / NDJSON); Data is the payload bytes.
type StreamChunk struct {
	Event string
	Data  []byte
}

// StreamBridgeState is the per-stream opaque carrier for adapter bridging
// (tool-call delta assembly, finish tracking). One instance per stream,
// created by the entry when the stream starts.
type StreamBridgeState struct {
	values map[string]any
}

// NewStreamBridgeState creates the per-stream state.
func NewStreamBridgeState() *StreamBridgeState {
	return &StreamBridgeState{values: make(map[string]any)}
}

// Get returns a scratch value stored by the bridge.
func (s *StreamBridgeState) Get(key string) (any, bool) {
	v, ok := s.values[key]
	return v, ok
}

// Set stores a scratch value for the bridge.
func (s *StreamBridgeState) Set(key string, value any) {
	if s.values == nil {
		s.values = make(map[string]any)
	}
	s.values[key] = value
}

// --- Embedding / Rerank / ASR / Listing facet types (design §6.1) ---

// EmbeddingOptions requests vectors for the given inputs. TruncatePromptTokens
// and SupportsDimensionOverride ride from the model record (the caller-side
// embedder fills them once at construction); each adapter gates them exactly
// as its v1 client did, so zero values mean "vendor default" here.
type EmbeddingOptions struct {
	Inputs     []string
	Dimensions int
	// TruncatePromptTokens: v1 per-vendor defaults apply when zero (511 for
	// openai-shape vendors carrying truncate_prompt_tokens; ignored by
	// vendors whose API has no such param).
	TruncatePromptTokens int
	// SupportsDimensionOverride gates the dimensions wire param
	// (v1: supportsDimensionOverride && dimensions > 0).
	SupportsDimensionOverride bool
}

// EmbeddingResponse returns one vector per input.
type EmbeddingResponse struct {
	Vectors [][]float32
	Usage   Usage
}

// RerankOptions asks to rank documents against a query.
type RerankOptions struct {
	Query     string
	Documents []string
	TopN      int
}

// RerankResult is one ranked document (index into the input slice).
type RerankResult struct {
	Index int
	Score float64
}

// RerankResponse returns the ranked subset.
type RerankResponse struct {
	Results []RerankResult
}

// ASROptions describes one transcription request. FileName carries the
// upload's extension hint (v1 defaulted the multipart filename to
// audio.mp3 when empty).
type ASROptions struct {
	Audio    []byte
	FileName string
	Format   string
	Language string
}

// ASRResponse returns the transcribed text with its timestamped segments
// (v1 TranscriptionResult semantics; verbose_json response_format).
type ASRResponse struct {
	Text     string
	Segments []ASRSegment
}

// ASRSegment is one timestamped transcript segment.
type ASRSegment struct {
	Start float64
	End   float64
	Text  string
}

// ListOptions parameterizes remote model listing (invoke.List, design
// §5.10). BaseURL is SSRF-gated by the entry; Credentials carries the API
// key (app-level credentials stay unused today — native adapters that need
// them read Credentials.AppID/AppSecret).
//
// 2026-09-12 ruling: EVERY remote list load filters by the model type being
// edited — ModelType carries that facet (empty = unfiltered legacy callers),
// and PageNo (1-based; 0 → adapter default) drives the entry's pagination
// loop for adapters that opt in via PaginatedLister.
type ListOptions struct {
	BaseURL     string
	Credentials Credentials
	ModelType   types.ModelType
	PageNo      int
}

// RemoteModel is one model discovered on the provider. The json contract is
// the v1 remote-catalog wire (id/display_name/owned_by, design §5.10.1); the
// richer fields are filled by adapters with vendor metadata (ollama /api/tags).
// v1 also emitted an always-empty "meta" envelope — dropped, it was never
// populated and the frontend reads ids only.
type RemoteModel struct {
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name,omitempty"`
	OwnedBy         string   `json:"owned_by,omitempty"`
	ContextWindow   int      `json:"context_window,omitempty"`
	MaxOutputTokens int      `json:"max_output_tokens,omitempty"`
	Modalities      []string `json:"modalities,omitempty"`
	ThinkingLevels  []string `json:"thinking_levels,omitempty"`
}

// --- Call configuration (design §6.1 ModelConfig) ---

// ModelConfig is the input to every entry function — the superset of v1
// ChatConfig. There is NO Source/local concept: local Ollama is a regular
// provider (base_url + SSRF whitelist, no special channel).
type ModelConfig struct {
	Provider string
	ModelID  string // concurrency limiter key
	// ModelName is the wire model identifier (Build*Request model parameter).
	ModelName string
	BaseURL   string

	Credentials Credentials // assembled by the shared constructor (§6.8)

	MaxConcurrency int               // 0 = process-wide default (limiter)
	CustomHeaders  map[string]string // overlaid by the entry (§6.4 rules)
	ExtraConfig    map[string]string // thinking_control legacy read source

	ThinkingLevel  string   // model record default (chain tier 2)
	SelectedLevels []string // model record allowed set
	// ThinkingEnabled is the model record's master switch (editor toggle).
	// nil = undeclared (no gate, tier chain runs as before); &false = the user
	// deliberately disabled thinking for this model — foldChatOptions forces
	// the wire off regardless of the session/agent tier.
	ThinkingEnabled *bool

	// Capability ceilings consumed by compression decisions / adapter clamps.
	ContextWindow   int
	MaxOutputTokens int
	// MaxInputTokens is a RESERVED field (user ruling): always 0 this cycle;
	// activation ships with the deferred context-budget work (§13.2).
	MaxInputTokens int
}

// Endpoint is what an adapter needs to reach the provider. Credentials ride
// along because body-dependent signatures (WeKnoraCloud HMAC) cannot be
// pre-rendered into headers by the entry.
type Endpoint struct {
	BaseURL     string
	Credentials Credentials
	// APIVersion carries ExtraConfig["api_version"] (azure deployment URL
	// query). Empty = the adapter's documented default.
	APIVersion string
	// ThinkingControl carries the folded ExtraConfig["thinking_control"]
	// override (v1 parseThinkingOverride: "none"/"enable_thinking"/
	// "thinking_type"/"chat_template_kwargs"). Empty = the adapter's default
	// thinking strategy applies.
	ThinkingControl string
	// TruncatePromptTokens carries the folded ExtraConfig["truncate_prompt_tokens"]
	// rerank opt-in (vLLM semantics, issue #2143). Zero = not sent.
	TruncatePromptTokens int
}

// Request is the adapter's "native call description" — adapters never touch
// HTTP themselves; the executor is the only network egress.
type Request struct {
	Method string
	URL    string
	// Header carries vendor auth headers. Body-dependent signing happens
	// inside Build*Request. User CustomHeaders are overlaid by the entry
	// after Build, skipping ProtectedHeaders (§6.4).
	Header http.Header
	// Body is the replayable request body ([]byte retries are free).
	Body []byte
	// BodyStream is the ASR large-file alternative; NOT retryable. Exactly
	// one of Body/BodyStream is set.
	BodyStream io.Reader
	// ProtectedHeaders lists header names the entry must not let user custom
	// headers override (signature/auth-critical headers, multipart
	// Content-Type).
	ProtectedHeaders []string
	Stream           bool
	// Timeout zero = executor default per model kind (§6.3 mapping).
	Timeout time.Duration
}

// ModelKind selects the executor's default timeout (§6.3) and retry context.
type ModelKind string

const (
	// ModelKindChat is non-stream chat (v1: WEKNORA_LLM_CHAT_TIMEOUT_SECONDS);
	// the remaining kinds resolve their timeout per the same §6.3 table.
	ModelKindChat ModelKind = "chat"
	// ModelKindChatStream is stream chat (..._STREAM_...).
	ModelKindChatStream ModelKind = "chat_stream"
	// ModelKindEmbedding is the embedding kind.
	ModelKindEmbedding ModelKind = "embedding"
	// ModelKindRerank is the rerank kind.
	ModelKindRerank ModelKind = "rerank"
	// ModelKindASR is the ASR kind (fixed 300s).
	ModelKindASR ModelKind = "asr"
)

// ModelKey identifies a call to the executor: limiter key, wire name, kind.
type ModelKey struct {
	ModelID   string
	ModelName string
	Kind      ModelKind
	// ConcurrencyLimit is the model's configured background cap; 0 falls back
	// to the process-wide default (limiter.GateNamedN semantics).
	ConcurrencyLimit int
}
