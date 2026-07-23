package types

import (
	"database/sql/driver"
	"encoding/json"
)

// PromptCacheStatus distinguishes a real cache miss from providers that do not
// expose prompt-cache accounting. Treating both as zero makes fleet-level hit
// rate dashboards misleading.
type PromptCacheStatus string

const (
	PromptCacheStatusUnsupported PromptCacheStatus = "unsupported"
	PromptCacheStatusUnreported  PromptCacheStatus = "unreported"
	PromptCacheStatusMiss        PromptCacheStatus = "miss"
	PromptCacheStatusHit         PromptCacheStatus = "hit"
)

// TokenUsage holds token consumption statistics returned by the model API.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens is the legacy alias for CacheReadTokens. It remains on the
	// wire for compatibility with existing API consumers.
	CachedTokens     int               `json:"cached_tokens,omitempty"`
	CacheReadTokens  int               `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int               `json:"cache_write_tokens,omitempty"`
	CacheMissTokens  int               `json:"cache_miss_tokens,omitempty"`
	CacheReported    bool              `json:"cache_reported"`
	CacheStatus      PromptCacheStatus `json:"cache_status,omitempty"`
}

// SetPromptCacheUsage normalizes provider-specific cache counters into the
// shared usage model. promptTokens remains the provider's total input-token
// count; read/write/miss are descriptive subsets and must not be added to it.
func (u *TokenUsage) SetPromptCacheUsage(read, write, miss int, reported bool) {
	if u == nil {
		return
	}
	if read < 0 {
		read = 0
	}
	if write < 0 {
		write = 0
	}
	if miss < 0 {
		miss = 0
	}
	u.CachedTokens = read
	u.CacheReadTokens = read
	u.CacheWriteTokens = write
	u.CacheMissTokens = miss
	u.CacheReported = reported
	switch {
	case !reported:
		u.CacheStatus = PromptCacheStatusUnreported
	case read > 0:
		u.CacheStatus = PromptCacheStatusHit
	default:
		u.CacheStatus = PromptCacheStatusMiss
	}
}

// MarkPromptCacheUnsupported marks a provider/model path that cannot report
// provider-side prompt-cache usage.
func (u *TokenUsage) MarkPromptCacheUnsupported() {
	if u == nil {
		return
	}
	u.SetPromptCacheUsage(0, 0, 0, false)
	u.CacheStatus = PromptCacheStatusUnsupported
}

// LLMToolCall represents a function/tool call from the LLM
type LLMToolCall struct {
	ID               string           `json:"id"`
	Type             string           `json:"type"` // "function"
	Function         FunctionCall     `json:"function"`
	ProviderMetadata ToolCallMetadata `json:"provider_metadata,omitempty"`
}

// ToolCallMetadata carries provider-specific tool-call state that must round-trip
// with the assistant tool call, without teaching core agent code vendor fields.
type ToolCallMetadata map[string]json.RawMessage

// FunctionCall represents the function details
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ChatResponse chat response
type ChatResponse struct {
	Content string `json:"content"`
	// ReasoningContent 是支持思考链的模型（DeepSeek thinking、小米 MiMo、vLLM reasoning 等）
	// 在本轮输出的推理内容。需要在后续多轮请求中原样回传给那些严格校验的供应商。
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []LLMToolCall `json:"tool_calls,omitempty"`
	FinishReason     string        `json:"finish_reason,omitempty"`
	Usage            TokenUsage    `json:"usage"`

	// AnswerStreamed reports whether the user-facing answer text was already
	// streamed live to the final-answer UI area during this round (i.e. the
	// model answered with plain content). When true, the natural-stop branch
	// must only emit the closing
	// Done marker for AnswerEventID instead of re-emitting the whole answer —
	// otherwise the answer would render twice and "jump" at end of stream.
	// Transient, never persisted.
	AnswerStreamed bool `json:"-"`
	// AnswerEventID is the EventBus event ID under which the live answer
	// chunks were streamed, so the natural-stop branch can close the same
	// stream with a Done marker. Empty when AnswerStreamed is false.
	AnswerEventID string `json:"-"`
}

// Response type
type ResponseType string

const (
	// Answer response type
	ResponseTypeAnswer ResponseType = "answer"
	// References response type
	ResponseTypeReferences ResponseType = "references"
	// Thinking response type (for agent thought process)
	ResponseTypeThinking ResponseType = "thinking"
	// Tool call response type (for agent tool invocations)
	ResponseTypeToolCall ResponseType = "tool_call"
	// Tool result response type (for agent tool results)
	ResponseTypeToolResult ResponseType = "tool_result"
	// Error response type
	ResponseTypeError ResponseType = "error"
	// Reflection response type (for agent reflection)
	ResponseTypeReflection ResponseType = "reflection"
	// Session title response type
	ResponseTypeSessionTitle ResponseType = "session_title"
	// Agent query response type (query received and processing started)
	ResponseTypeAgentQuery ResponseType = "agent_query"
	// Complete response type (agent complete)
	ResponseTypeComplete ResponseType = "complete"
	// ToolApprovalRequired: MCP tool marked dangerous — UI must collect user approval before execution continues
	ResponseTypeToolApprovalRequired ResponseType = "tool_approval_required"
	// ToolApprovalResolved: user approved/rejected (or timeout); informational for UI replay
	ResponseTypeToolApprovalResolved ResponseType = "tool_approval_resolved"
	// MCPOAuthRequired: an OAuth-enabled MCP service was invoked but the user
	// has not authorized it — UI must surface an "Authorize" prompt and the
	// agent pauses until authorization completes (or the wait times out).
	ResponseTypeMCPOAuthRequired ResponseType = "mcp_oauth_required"
	// MCPOAuthResolved: authorization completed / timed out / canceled;
	// informational for UI replay.
	ResponseTypeMCPOAuthResolved ResponseType = "mcp_oauth_resolved"
)

// StreamResponse stream response
type StreamResponse struct {
	ID                  string                 `json:"id"`
	ResponseType        ResponseType           `json:"response_type"`
	Content             string                 `json:"content"`
	Done                bool                   `json:"done"`
	KnowledgeReferences References             `json:"knowledge_references,omitempty"`
	SessionID           string                 `json:"session_id,omitempty"`
	AssistantMessageID  string                 `json:"assistant_message_id,omitempty"`
	ToolCalls           []LLMToolCall          `json:"tool_calls,omitempty"`
	Data                map[string]interface{} `json:"data,omitempty"`
	Usage               *TokenUsage            `json:"usage,omitempty"`
	FinishReason        string                 `json:"finish_reason,omitempty"`
}

// References references
type References []*SearchResult

// Value implements the driver.Valuer interface, used to convert References to database values
func (c References) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database values to References
func (c *References) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
