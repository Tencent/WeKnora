package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// AgentStreamEvent represents a streaming event from the agent
type AgentStreamEvent struct {
	Type      string                 `json:"type"`      // "thought", "tool_call", "tool_result", "final_answer", "error", "references"
	Content   string                 `json:"content"`   // Incremental content
	Data      map[string]interface{} `json:"data"`      // Additional structured data
	Done      bool                   `json:"done"`      // Whether this is the last event
	Iteration int                    `json:"iteration"` // Current iteration number
}

// AgentEngine defines the interface for agent execution engine
type AgentEngine interface {
	// Execute executes the agent with conversation history and returns a stream of events
	Execute(
		ctx context.Context,
		sessionID, messageID, query string,
		llmContext []chat.Message,
	) (*types.AgentState, error)
}

// AgentService defines the interface for agent-related operations
type AgentService interface {
	// CreateAgentEngine creates an agent engine with the given configuration, EventBus, and ContextManager
	CreateAgentEngine(
		ctx context.Context,
		config *types.AgentConfig,
		chatModel chat.Chat,
		rerankModel rerank.Reranker,
		eventBus *event.EventBus,
		contextManager ContextManager,
		sessionID string,
	) (AgentEngine, error)

	// ValidateConfig validates an agent configuration
	ValidateConfig(config *types.AgentConfig) error
}
