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
	Query            string   `json:"query" binding:"required"` // Query text for knowledge base search
	KnowledgeBaseIDs []string `json:"knowledge_base_ids"`       // Selected knowledge base ID for this request
	// KnowledgeIDs are the selected knowledge (file) IDs for this request.
	KnowledgeIDs []string `json:"knowledge_ids"`
	AgentEnabled bool     `json:"agent_enabled"` // Whether agent mode is enabled for this request
	// AgentID selects the custom agent; the backend resolves the shared agent
	// and its workspace from the share relation.
	AgentID string `json:"agent_id"`
	// AgentSourceTenantID disambiguates the share relation; the backend still
	// verifies it.
	AgentSourceTenantID uint64 `json:"agent_source_tenant_id,omitempty"`
	// LocalBrowserEnabled toggles the local-browser source (upstream browser
	// feature).
	LocalBrowserEnabled bool `json:"local_browser_enabled"` // Browser source
	WebSearchEnabled    bool `json:"web_search_enabled"`    // Whether web search is enabled for this request
	// SummaryModelID overrides the session default summary model.
	SummaryModelID string `json:"summary_model_id"`
	// Optional per-request thinking level override (platform vocabulary).
	ThinkingLevel string `json:"thinking_level,omitempty"`
	// Optional per-request thinking on/off override. Nil = follow the agent
	// (or global) default; true/false forces the thinking param on the wire.
	Thinking *bool `json:"thinking,omitempty"`
	// Per-request MCP services / Skills / @mentions (tag IDs are display and
	// debug only; scoped via MentionedItems).
	MCPServiceIDs  []string               `json:"mcp_service_ids"`
	SkillNames     []string               `json:"skill_names"`
	TagIDs         []string               `json:"tag_ids"`
	MentionedItems []MentionedItemRequest `json:"mentioned_items"`
	// DisableTitle turns off auto title generation.
	DisableTitle bool `json:"disable_title"`
	// Images: attached images for multimodal chat. AttachmentIDs: pre-uploaded
	// session-scoped document IDs. AttachmentUploads: attached files.
	Images        []ImageAttachment `json:"images"`
	AttachmentIDs []string          `json:"attachment_ids,omitempty"`
	// Channel: source channel, e.g. "web", "api", "im".
	Channel string `json:"channel"`

	AttachmentUploads []AttachmentUpload `json:"attachment_uploads,omitempty"` // attached files (docs, audio, etc.)

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
	Query            string                 `json:"query"              binding:"required"` // Query text to search for
	KnowledgeBaseID  string                 `json:"knowledge_base_id"`                     // Single knowledge base ID (for backward compatibility)
	KnowledgeBaseIDs []string               `json:"knowledge_base_ids"`                    // IDs of knowledge bases to search (multi-KB support)
	KnowledgeIDs     []string               `json:"knowledge_ids"`                         // IDs of specific knowledge (files) to search
	TagIDs           []string               `json:"tag_ids"`                               // Tag IDs for filtering within a single KB
	MentionedItems   []MentionedItemRequest `json:"mentioned_items"`                       // Optional scoped tag mentions
}

// StopSessionRequest represents the stop session request
type StopSessionRequest struct {
	MessageID string `json:"message_id" binding:"required"`
}
