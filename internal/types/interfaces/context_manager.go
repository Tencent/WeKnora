package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

// ContextManager manages LLM context for sessions
// It maintains conversation context separately from message storage
// and provides context compression when context window is exceeded
type ContextManager interface {
	// AddMessage adds a message to the session context
	// The message will be added to the context window for LLM
	AddMessage(ctx context.Context, sessionID string, message chat.Message) error

	// GetContext retrieves the current context for a session
	// Returns messages that fit within the context window
	// May apply compression if context is too large
	GetContext(ctx context.Context, sessionID string) ([]chat.Message, error)

	// ClearContext clears all context for a session
	ClearContext(ctx context.Context, sessionID string) error

	// GetContextStats returns statistics about the context
	GetContextStats(ctx context.Context, sessionID string) (*ContextStats, error)

	// SetSystemPrompt sets or updates the system prompt for a session
	// If a system message exists, it will be replaced; otherwise, a new one will be added at the beginning
	SetSystemPrompt(ctx context.Context, sessionID string, systemPrompt string) error
}

// ContextStats contains statistics about session context
type ContextStats struct {
	// Total number of messages in context
	MessageCount int `json:"message_count"`
	// Estimated token count
	TokenCount int `json:"token_count"`
	// Whether context was compressed
	IsCompressed bool `json:"is_compressed"`
	// Number of original messages before compression
	OriginalMessageCount int `json:"original_message_count"`
}

