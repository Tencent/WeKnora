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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`       // "kb", "file", "tag", "mcp", "skill"
	KBType    string `json:"kb_type"`    // "document" or "faq" (only for kb type)
	KBID      string `json:"kb_id"`      // Parent knowledge base for file/tag mentions
	KBName    string `json:"kb_name"`    // Display name for parent KB
	ServiceID string `json:"service_id"` // Parent MCP service for MCP tool mentions
	SkillName string `json:"skill_name"` // Preloaded agent skill name
}

// ImageAttachment represents an image in a chat request.
// Frontend sends base64 data in the Data field; the backend saves, runs VLM analysis,
// and populates URL/Caption before proceeding with the chat pipeline.
type ImageAttachment struct {
	Data    string `json:"data,omitempty"`    // base64 data URI from frontend (data:image/png;base64,...)
	URL     string `json:"url,omitempty"`     // serving URL after saving to storage
	Caption string `json:"caption,omitempty"` // VLM analysis result (context-aware, single call)
}

// CreateKnowledgeQARequest defines the request structure for knowledge QA
type CreateKnowledgeQARequest struct {
	// Query text for knowledge base search
	Query string `json:"query" binding:"required"`
	// Selected knowledge base ID for this request
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`
	KnowledgeIDs     []string `json:"knowledge_ids"` // Selected knowledge ID for this request
	// Whether agent mode is enabled for this request
	AgentEnabled bool `json:"agent_enabled"`
	// Selected custom agent ID (backend resolves shared agent and its workspace from share relation)
	AgentID string `json:"agent_id"`
	// Whether web search is enabled for this request
	WebSearchEnabled bool `json:"web_search_enabled"`
	// Optional summary model ID for this request (overrides session default)
	SummaryModelID string `json:"summary_model_id"`
	// Per-request MCP services selected via @mention
	MCPServiceIDs []string `json:"mcp_service_ids"`
	// Per-request Skills selected via @mention
	SkillNames []string `json:"skill_names"`
	// @mentioned tag IDs (display/debug; scoped via MentionedItems)
	TagIDs []string `json:"tag_ids"`
	// @mentioned knowledge bases and files
	MentionedItems []MentionedItemRequest `json:"mentioned_items"`
	// Whether to disable auto title generation
	DisableTitle bool `json:"disable_title"`
	// Attached images for multimodal chat
	Images []ImageAttachment `json:"images"`
	// Attached files (documents, audio, etc.)
	AttachmentUploads []AttachmentUpload `json:"attachment_uploads,omitempty"`
	//nolint:lll
	AttachmentIDs []string `json:"attachment_ids,omitempty"` // Pre-uploaded session-scoped document IDs
	//nolint:lll
	Channel               string                       `json:"channel"` // Source channel: "web", "api", "im", etc.
	SuggestionAttribution *types.SuggestionAttribution `json:"suggestion_attribution,omitempty"`
}

// AttachmentUpload represents a file attachment upload from the client
type AttachmentUpload struct {
	Data     string `json:"data"`      // Base64-encoded file content
	FileName string `json:"file_name"` // Original filename
	FileSize int64  `json:"file_size"` // File size in bytes
}

// SearchKnowledgeRequest defines the request structure for searching knowledge without LLM summarization
type SearchKnowledgeRequest struct {
	Query string `json:"query"              binding:"required"` // Query text to search for
	//nolint:lll
	KnowledgeBaseID string `json:"knowledge_base_id"` // Single knowledge base ID (for backward compatibility)
	// IDs of knowledge bases to search (multi-KB support)
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`
	KnowledgeIDs     []string `json:"knowledge_ids"` // IDs of specific knowledge (files) to search
	// Tag IDs for filtering within a single KB
	TagIDs         []string               `json:"tag_ids"`
	MentionedItems []MentionedItemRequest `json:"mentioned_items"` // Optional scoped tag mentions
}

// StopSessionRequest represents the stop session request
type StopSessionRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}
