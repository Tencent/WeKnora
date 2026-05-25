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
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

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
