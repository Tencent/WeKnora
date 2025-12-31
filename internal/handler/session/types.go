package session

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// CreateSessionRequest represents a request to create a new session
// Sessions are now knowledge-base-independent and serve as conversation containers.
// All configuration (knowledge bases, model settings, etc.) comes from custom agent at query time.
type CreateSessionRequest struct {
	// Title for the session (optional)
	Title string `json:"title"`
	// Description for the session (optional)
	Description string `json:"description"`
}

// GenerateTitleRequest defines the request structure for generating a session title
type GenerateTitleRequest struct {
	Messages []types.Message `json:"messages" binding:"required"` // Messages to use as context for title generation
}

// MentionedItemRequest represents a mentioned item in the request
type MentionedItemRequest struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`    // "kb" for knowledge base, "file" for file
	KBType string `json:"kb_type"` // "document" or "faq" (only for kb type)
}

// CreateKnowledgeQARequest defines the request structure for knowledge QA
type CreateKnowledgeQARequest struct {
	Query            string                 `json:"query"              binding:"required"` // Query text for knowledge base search
	KnowledgeBaseIDs []string               `json:"knowledge_base_ids"`                    // Selected knowledge base ID for this request
	KnowledgeIds     []string               `json:"knowledge_ids"`                         // Selected knowledge ID for this request
	AgentEnabled     bool                   `json:"agent_enabled"`                         // Whether agent mode is enabled for this request
	AgentID          string                 `json:"agent_id"`                              // Selected custom agent ID for this request
	WebSearchEnabled bool                   `json:"web_search_enabled"`                    // Whether web search is enabled for this request
	SummaryModelID   string                 `json:"summary_model_id"`                      // Optional summary model ID for this request (overrides session default)
	MentionedItems   []MentionedItemRequest `json:"mentioned_items"`                       // @mentioned knowledge bases and files
	DisableTitle     bool                   `json:"disable_title"`                         // Whether to disable auto title generation
}

// SearchKnowledgeRequest defines the request structure for searching knowledge without LLM summarization
type SearchKnowledgeRequest struct {
	Query            string   `json:"query"              binding:"required"` // Query text to search for
	KnowledgeBaseID  string   `json:"knowledge_base_id"`                     // Single knowledge base ID (for backward compatibility)
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`                    // IDs of knowledge bases to search (multi-KB support)
	KnowledgeIDs     []string `json:"knowledge_ids"`                         // IDs of specific knowledge (files) to search
}

// StopSessionRequest represents the stop session request
type StopSessionRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}
