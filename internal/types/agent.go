package types

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"time"
)

// AgentConfig represents the full agent configuration (used at tenant level and runtime)
// This includes all configuration parameters for agent execution
type AgentConfig struct {
	MaxIterations     int      `json:"max_iterations"`          // Maximum number of ReAct iterations
	ReflectionEnabled bool     `json:"reflection_enabled"`      // Whether to enable reflection
	AllowedTools      []string `json:"allowed_tools"`           // List of allowed tool names
	Temperature       float64  `json:"temperature"`             // LLM temperature for agent
	KnowledgeBases    []string `json:"knowledge_bases"`         // Accessible knowledge base IDs
	KnowledgeIDs      []string `json:"knowledge_ids"`           // Accessible knowledge IDs (individual documents)
	SystemPrompt      string   `json:"system_prompt,omitempty"` // Unified system prompt (uses {{web_search_status}} placeholder for dynamic behavior)
	// Deprecated: Use SystemPrompt instead. Kept for backward compatibility during migration.
	SystemPromptWebEnabled  string        `json:"system_prompt_web_enabled,omitempty"`  // Deprecated: Custom prompt when web search is enabled
	SystemPromptWebDisabled string        `json:"system_prompt_web_disabled,omitempty"` // Deprecated: Custom prompt when web search is disabled
	UseCustomSystemPrompt   bool          `json:"use_custom_system_prompt"`             // Whether to use custom system prompt instead of default
	WebSearchEnabled        bool          `json:"web_search_enabled"`                   // Whether web search tool is enabled
	WebSearchMaxResults     int           `json:"web_search_max_results"`               // Maximum number of web search results (default: 5)
	MultiTurnEnabled        bool          `json:"multi_turn_enabled"`                   // Whether multi-turn conversation is enabled
	HistoryTurns            int           `json:"history_turns"`                        // Number of history turns to keep in context
	SearchTargets           SearchTargets `json:"-"`                                    // Pre-computed unified search targets (runtime only)
	// MCP service selection
	MCPSelectionMode string   `json:"mcp_selection_mode"` // MCP selection mode: "all", "selected", "none"
	MCPServices      []string `json:"mcp_services"`       // Selected MCP service IDs (when mode is "selected")
	// Whether to enable thinking mode (for models that support extended thinking)
	Thinking *bool `json:"thinking"`
	// Whether to retrieve knowledge base only when explicitly mentioned with @ (default: false)
	RetrieveKBOnlyWhenMentioned bool `json:"retrieve_kb_only_when_mentioned"`
}

// SessionAgentConfig represents session-level agent configuration
// Sessions only store Enabled and KnowledgeBases; other configs are read from Tenant at runtime
type SessionAgentConfig struct {
	AgentModeEnabled bool     `json:"agent_mode_enabled"` // Whether agent mode is enabled for this session
	WebSearchEnabled bool     `json:"web_search_enabled"` // Whether web search is enabled for this session
	KnowledgeBases   []string `json:"knowledge_bases"`    // Accessible knowledge base IDs for this session
	KnowledgeIDs     []string `json:"knowledge_ids"`      // Accessible knowledge IDs (individual documents) for this session
}

// Value implements driver.Valuer interface for AgentConfig
func (c AgentConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for AgentConfig
func (c *AgentConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements driver.Valuer interface for SessionAgentConfig
func (c SessionAgentConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements sql.Scanner interface for SessionAgentConfig
func (c *SessionAgentConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// ResolveSystemPrompt returns the prompt template for the given web search state.
// It uses the unified SystemPrompt field, falling back to deprecated fields for backward compatibility.
func (c *AgentConfig) ResolveSystemPrompt(webSearchEnabled bool) string {
	if c == nil {
		return ""
	}

	// First, try the new unified SystemPrompt field
	if c.SystemPrompt != "" {
		return c.SystemPrompt
	}

	// Fallback to deprecated fields for backward compatibility
	if webSearchEnabled {
		if c.SystemPromptWebEnabled != "" {
			return c.SystemPromptWebEnabled
		}
	} else {
		if c.SystemPromptWebDisabled != "" {
			return c.SystemPromptWebDisabled
		}
	}

	return ""
}

// Tool defines the interface that all agent tools must implement
type Tool interface {
	// Name returns the unique identifier for this tool
	Name() string

	// Description returns a human-readable description of what the tool does
	Description() string

	// Parameters returns the JSON Schema for the tool's parameters
	Parameters() json.RawMessage

	// Execute runs the tool with the given arguments
	Execute(ctx context.Context, args json.RawMessage) (*ToolResult, error)
}

// ToolResult represents the result of a tool execution
type ToolResult struct {
	Success bool                   `json:"success"`         // Whether the tool executed successfully
	Output  string                 `json:"output"`          // Human-readable output
	Data    map[string]interface{} `json:"data,omitempty"`  // Structured data for programmatic use
	Error   string                 `json:"error,omitempty"` // Error message if execution failed
}

// ToolCall represents a single tool invocation within an agent step
type ToolCall struct {
	ID         string                 `json:"id"`                   // Function call ID from LLM
	Name       string                 `json:"name"`                 // Tool name
	Args       map[string]interface{} `json:"args"`                 // Tool arguments
	Result     *ToolResult            `json:"result"`               // Execution result (contains Output)
	Reflection string                 `json:"reflection,omitempty"` // Agent's reflection on this tool call result (if enabled)
	Duration   int64                  `json:"duration"`             // Execution time in milliseconds
}

// AgentStep represents one iteration of the ReAct loop
type AgentStep struct {
	Iteration int        `json:"iteration"`  // Iteration number (0-indexed)
	Thought   string     `json:"thought"`    // LLM's reasoning/thinking (Think phase)
	ToolCalls []ToolCall `json:"tool_calls"` // Tools called in this step (Act phase)
	Timestamp time.Time  `json:"timestamp"`  // When this step occurred
}

// GetObservations returns observations from all tool calls in this step
// This is a convenience method to maintain backward compatibility
func (s *AgentStep) GetObservations() []string {
	observations := make([]string, 0, len(s.ToolCalls))
	for _, tc := range s.ToolCalls {
		if tc.Result != nil && tc.Result.Output != "" {
			observations = append(observations, tc.Result.Output)
		}
		if tc.Reflection != "" {
			observations = append(observations, "Reflection: "+tc.Reflection)
		}
	}
	return observations
}

// AgentState tracks the execution state of an agent across iterations
type AgentState struct {
	CurrentRound  int             `json:"current_round"`  // Current round number
	RoundSteps    []AgentStep     `json:"round_steps"`    // All steps taken so far in the current round
	IsComplete    bool            `json:"is_complete"`    // Whether agent has finished
	FinalAnswer   string          `json:"final_answer"`   // The final answer to the query
	KnowledgeRefs []*SearchResult `json:"knowledge_refs"` // Collected knowledge references
}

// FunctionDefinition represents a function definition for LLM function calling
type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}
