package types

import "time"

// Agent inbox — durable event stream for agent situational awareness
// (works/agent-sidebar-inbox). Events are persisted here instead of the
// messages table on purpose: agent history assembly, memory extraction and
// session fork all iterate message rows, and inbox events must never leak
// into any of them. The agent reaches the inbox exclusively through the
// check_inbox tool, so events only enter the LLM context when the agent
// explicitly asks for them.

// Inbox priority tiers. P0 = user-facing failures (parse/image failures,
// error toasts); P1 = significant activity (uploads, edits, agent receipts,
// backlog warnings); P2 = informational (views, ingest completion).
const (
	InboxPriorityP0 = "P0"
	InboxPriorityP1 = "P1"
	InboxPriorityP2 = "P2"
)

// Inbox event types (MVP set). Names are stable API for the policy file —
// adding a type is fine, renaming one is not.
const (
	// User actions (frontend emitters + write-op middleware).
	InboxTypeUserViewedPage  = "user.viewed_page"  // sidebar view tracking (debounced)
	InboxTypeUserSwitchedTab = "user.switched_tab" // KB tab switch inside KnowledgeBase view
	InboxTypeUserErrorToast  = "user.error_toast"  // frontend error toast forwarding
	InboxTypeUserUploadedDoc = "user.uploaded_doc" // document upload (middleware)
	InboxTypeUserDeletedDoc  = "user.deleted_doc"  // document deletion (middleware)
	InboxTypeUserReparseDoc  = "user.reparse_doc"  // reparse request (middleware)
	InboxTypeUserEditedWiki  = "user.edited_wiki"  // wiki page edit (middleware)
	InboxTypeUserEditChunk   = "user.edited_chunk" // chunk edit (middleware)

	// Sidebar view tracking beyond wiki pages: the document reader and the
	// gallery image viewer report what the user is looking at, so the agent
	// can anchor on it. Payload carries id/title/kind; debounced by
	// frontend-supplied dedup_key.
	InboxTypeUserViewedDocument = "user.viewed_document"
	InboxTypeUserViewedImage    = "user.viewed_image"
	// Emitted when the user opens or switches knowledge base in the main
	// view. With kb_selection_mode "all" this is the only signal telling the
	// agent which KB the user is currently working in.
	InboxTypeUserSwitchedKB = "user.switched_kb"

	// KB backend lifecycle events.
	InboxTypeParseCompleted = "parse.completed" // document parse finished successfully
	InboxTypeParseFailed    = "parse.failed"    // document parse exhausted retries

	// Agent action receipts (write operations performed by the agent).
	InboxTypeAgentWikiWrite = "agent_action.wiki_write"
)

// InboxEventTypes lists every event type the inbox recognizes. The policy
// loader uses it to validate config entries.
var InboxEventTypes = []string{
	InboxTypeUserViewedPage, InboxTypeUserSwitchedTab, InboxTypeUserErrorToast,
	InboxTypeUserUploadedDoc, InboxTypeUserDeletedDoc, InboxTypeUserReparseDoc,
	InboxTypeUserEditedWiki, InboxTypeUserEditChunk,
	InboxTypeUserViewedDocument, InboxTypeUserViewedImage, InboxTypeUserSwitchedKB,
	InboxTypeParseCompleted, InboxTypeParseFailed,
	InboxTypeAgentWikiWrite,
}

// AgentInboxEvent is one persisted inbox entry.
type AgentInboxEvent struct {
	ID        string     `gorm:"type:varchar(36);primaryKey" json:"id"`
	TenantID  uint64     `gorm:"column:tenant_id" json:"tenant_id"`
	KBID      string     `gorm:"type:varchar(36);column:kb_id" json:"kb_id"`
	UserID    string     `gorm:"type:varchar(64);column:user_id" json:"user_id,omitempty"`
	Type      string     `gorm:"type:varchar(64)" json:"type"`
	Priority  string     `gorm:"type:varchar(8)" json:"priority"`
	Payload   JSONMap    `gorm:"type:jsonb" json:"payload,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
}

// TableName pins the gorm table name.
func (AgentInboxEvent) TableName() string { return "agent_inbox" }

// InboxQueryFilters narrows a check_inbox / trail query.
type InboxQueryFilters struct {
	TenantID   uint64
	KBIDs      []string // empty = no KB restriction
	UserID     string   // empty = no owner restriction
	Types      []string // empty = all types
	UnreadOnly bool
	Since      *time.Time
	Limit      int
}

// InboxDigest is the compact per-event form returned to the agent.
type InboxDigest struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"`
	Priority string  `json:"priority"`
	Time     string  `json:"time"`
	Payload  JSONMap `json:"payload,omitempty"`
}
