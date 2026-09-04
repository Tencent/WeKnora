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

// Accumulate adds another call's usage into u, preserving the subset
// semantics: prompt/completion/total and every cache counter sum
// independently (cache counters stay subsets of the prompt count and are
// never folded into it). CacheReported ORs, and the cache status is
// recomputed from the combined counters so a single cache hit anywhere in
// the accumulated calls reads as a hit.
func (u *TokenUsage) Accumulate(other TokenUsage) {
	if u == nil {
		return
	}
	u.PromptTokens += other.PromptTokens
	u.CompletionTokens += other.CompletionTokens
	u.TotalTokens += other.TotalTokens
	u.CachedTokens += other.CachedTokens
	u.CacheReadTokens += other.CacheReadTokens
	u.CacheWriteTokens += other.CacheWriteTokens
	u.CacheMissTokens += other.CacheMissTokens
	u.CacheReported = u.CacheReported || other.CacheReported
	switch {
	case !u.CacheReported:
		u.CacheStatus = mergeUnreportedCacheStatus(u.CacheStatus, other.CacheStatus)
	case u.CacheReadTokens > 0:
		u.CacheStatus = PromptCacheStatusHit
	default:
		u.CacheStatus = PromptCacheStatusMiss
	}
}

// mergeUnreportedCacheStatus folds the statuses of never-reported usage.
// "unsupported" survives only while every accumulated call was itself
// classified unsupported — the first accumulation adopts the incoming
// classification, and any later call that is not known-unsupported (an
// unreported or unclassified one) degrades the aggregate to unreported.
func mergeUnreportedCacheStatus(accumulated, incoming PromptCacheStatus) PromptCacheStatus {
	if accumulated == "" {
		if incoming == "" {
			return PromptCacheStatusUnreported
		}
		return incoming
	}
	if accumulated == PromptCacheStatusUnsupported && incoming == PromptCacheStatusUnsupported {
		return PromptCacheStatusUnsupported
	}
	return PromptCacheStatusUnreported
}

// PromptCacheHitRate returns cache-read tokens as a percentage of the
// provider's prompt total. Zero when the prompt is empty. A ReAct turn that
// reuses the system prefix therefore reads as a high hit rather than a miss.
func (u TokenUsage) PromptCacheHitRate() float64 {
	if u.PromptTokens <= 0 {
		return 0
	}
	return float64(u.CacheReadTokens) / float64(u.PromptTokens) * 100
}

// Value persists the usage as a jsonb column (assistant messages carry the
// turn's aggregate); nil writes SQL NULL. Mirrors the nullable-pointer
// pattern APIPrincipalConfig uses.
func (u *TokenUsage) Value() (driver.Value, error) {
	if u == nil {
		return nil, nil
	}
	return json.Marshal(u)
}

// Scan restores a jsonb usage column; NULL leaves the receiver zero-valued.
func (u *TokenUsage) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	return json.Unmarshal(b, u)
}

// LLMToolCall represents a function/tool call from the LLM
type LLMToolCall struct {
	ID               string           `json:"id"`
	Type             string           `json:"type"` // "function"
	Function         FunctionCall     `json:"function"`
	ProviderMetadata ToolCallMetadata `json:"provider_metadata,omitempty"`

	// ModelArguments and the resolution fields are request-local observability
	// state. ModelArguments preserves the exact JSON emitted by the model while
	// Function.Arguments is decoded to durable application identifiers before
	// tool execution. These fields must never be sent back to a provider or
	// persisted in chat history.
	ModelArguments     string   `json:"-"`
	ArgumentResolution string   `json:"-"`
	UnresolvedHandles  []string `json:"-"`
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
	// ResponseTypeArtifactsPending is sent while skill/sandbox output is being
	// copied into persistent storage after the answer has already streamed.
	// The UI shows a toolbar placeholder until ResponseTypeComplete carries
	// the file list.
	ResponseTypeArtifactsPending ResponseType = "artifacts_pending"
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
	// MemoryRecalled: the long-term memories injected into this answer, so
	// the UI can show and let the user delete what influenced it.
	ResponseTypeMemoryRecalled ResponseType = "memory_recalled"
	// ResponseTypeContextCompacted is older conversation summarized away to
	// fit the context window. Surfaced because it changes what the agent
	// remembers — an answer that forgets an earlier instruction is otherwise
	// indistinguishable from the model ignoring it.
	ResponseTypeContextCompacted ResponseType = "context_compacted"
	// ResponseTypeInstallPrompt is the instruction a skill install handed to
	// the installer agent. Only the skill install transcript emits this, and
	// it emits it first, so replaying the log alone shows what was asked for
	// — the console does not have to read the durable prompt row to caption
	// the run.
	ResponseTypeInstallPrompt ResponseType = "install_prompt"
)

// StreamResponse stream response
// FinishReasonIncomplete marks a stream that broke before the provider sent a
// finish reason (read error, timeout, stall). Whatever content and tool calls
// were collected are a partial response: callers must not treat them as a
// completed turn.
const FinishReasonIncomplete = "incomplete"

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
