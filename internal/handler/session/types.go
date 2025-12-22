package session

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// SessionStrategy defines the configuration for a conversation session strategy
type SessionStrategy struct {
	// Maximum number of conversation rounds to maintain
	MaxRounds int `json:"max_rounds"`
	// Whether to enable query rewrite for multi-round conversations
	EnableRewrite bool `json:"enable_rewrite"`
	// Strategy to use when no relevant knowledge is found
	FallbackStrategy types.FallbackStrategy `json:"fallback_strategy"`
	// Fixed response content for fallback
	FallbackResponse string `json:"fallback_response"`
	// Number of top results to retrieve from vector search
	EmbeddingTopK int `json:"embedding_top_k"`
	// Threshold for keyword-based retrieval
	KeywordThreshold float64 `json:"keyword_threshold"`
	// Threshold for vector-based retrieval
	VectorThreshold float64 `json:"vector_threshold"`
	// ID of the model used for reranking results
	RerankModelID string `json:"rerank_model_id"`
	// Number of top results after reranking
	RerankTopK int `json:"rerank_top_k"`
	// Threshold for reranking results
	RerankThreshold float64 `json:"rerank_threshold"`
	// ID of the model used for summarization
	SummaryModelID string `json:"summary_model_id"`
	// Parameters for the summary model
	SummaryParameters *types.SummaryConfig `json:"summary_parameters" gorm:"type:json"`
	// Prefix for responses when no match is found
	NoMatchPrefix string `json:"no_match_prefix"`
}

// CreateSessionRequest represents a request to create a new session
// Sessions are now knowledge-base-independent and serve as conversation containers.
// Knowledge bases can be specified dynamically in each query request (AgentQA/KnowledgeQA).
type CreateSessionRequest struct {
	// ID of the associated knowledge base (optional, can be set/changed during queries)
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// Session strategy configuration
	SessionStrategy *SessionStrategy `json:"session_strategy"`
	// Agent configuration (optional, session-level config only: enabled and knowledge_bases)
	AgentConfig *types.SessionAgentConfig `json:"agent_config"`
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
	WebSearchEnabled bool                   `json:"web_search_enabled"`                    // Whether web search is enabled for this request
	SummaryModelID   string                 `json:"summary_model_id"`                      // Optional summary model ID for this request (overrides session default)
	MentionedItems   []MentionedItemRequest `json:"mentioned_items"`                       // @mentioned knowledge bases and files
}

// SearchKnowledgeRequest defines the request structure for searching knowledge without LLM summarization
type SearchKnowledgeRequest struct {
	Query           string `json:"query"             binding:"required"` // Query text to search for
	KnowledgeBaseID string `json:"knowledge_base_id" binding:"required"` // ID of the knowledge base to search
}

// StopSessionRequest represents the stop session request
type StopSessionRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}
