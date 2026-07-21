package types

import (
	"database/sql/driver"
	"encoding/json"
)

// TokenUsage holds token consumption statistics returned by the model API.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CachedTokens is the subset of PromptTokens that hit a provider-side
	// prompt cache. Populated from `usage.prompt_tokens_details.cached_tokens`
	// in OpenAI-compatible responses.
	//
	// Whether this field is non-zero depends on the provider's caching mode:
	//
	//   - Implicit caching (OpenAI, Azure OpenAI, DeepSeek, …) — automatic.
	//     The field populates whenever the prompt prefix matches a previous
	//     request within the provider's cache TTL. No client-side opt-in.
	//
	//   - Explicit caching (Qwen on Aliyun, Anthropic Claude, …) — opt-in
	//     required. The caller must attach `cache_control: {"type":
	//     "ephemeral"}` to the relevant message or content block to make
	//     the provider create and read the cache. Until that opt-in is
	//     applied, CachedTokens stays zero even when the prompt prefix is
	//     otherwise byte-stable.
	//
	// Omitted from JSON when zero so payloads stay quiet for providers
	// that never populate it.
	CachedTokens int `json:"cached_tokens,omitempty"`
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
	// AnswerEventID is the Bus event ID under which the live answer
	// chunks were streamed, so the natural-stop branch can close the same
	// stream with a Done marker. Empty when AnswerStreamed is false.
	AnswerEventID string `json:"-"`
}

// ResponseType identifies a streaming chat/agent event kind.
type ResponseType string

const (
	// ResponseTypeAnswer is the main assistant answer payload.
	ResponseTypeAnswer ResponseType = "answer"
	// ResponseTypeReferences carries retrieved source citations.
	ResponseTypeReferences ResponseType = "references"
	// ResponseTypeThinking exposes agent thought process text.
	ResponseTypeThinking ResponseType = "thinking"
	// ResponseTypeToolCall announces a tool invocation.
	ResponseTypeToolCall ResponseType = "tool_call"
	// ResponseTypeToolResult carries a tool execution result.
	ResponseTypeToolResult ResponseType = "tool_result"
	// ResponseTypeError reports a terminal agent error.
	ResponseTypeError ResponseType = "error"
	// ResponseTypeReflection carries agent self-reflection text.
	ResponseTypeReflection ResponseType = "reflection"
	// ResponseTypeSessionTitle carries an auto-generated session title.
	ResponseTypeSessionTitle ResponseType = "session_title"
	// ResponseTypeAgentQuery marks query receipt and processing start.
	ResponseTypeAgentQuery ResponseType = "agent_query"
	// ResponseTypeComplete marks successful agent completion.
	ResponseTypeComplete ResponseType = "complete"
	// ResponseTypeToolApprovalRequired pauses for dangerous MCP tool approval.
	ResponseTypeToolApprovalRequired ResponseType = "tool_approval_required"
	// ResponseTypeToolApprovalResolved reports tool approval outcome for replay.
	ResponseTypeToolApprovalResolved ResponseType = "tool_approval_resolved"
	// ResponseTypeMCPOAuthRequired pauses until the user authorizes MCP OAuth.
	ResponseTypeMCPOAuthRequired ResponseType = "mcp_oauth_required"
	// ResponseTypeMCPOAuthResolved reports MCP OAuth completion for replay.
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
