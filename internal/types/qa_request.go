package types

import (
	"context"
)

// SteerMessageContent adds delivery context only to model input. The persisted
// user message and the UI always retain the user's original text.
func SteerMessageContent(content string) string {
	return "<steer_message>\n" + content + "\n</steer_message>\n<continue_task>\n" +
		"This is guidance for the task in progress. Apply it and continue unfinished work " +
		"unless the user explicitly changes or cancels the task.\n</continue_task>"
}

// SteerSink abstracts the handler-side back half of mid-run message
// injection: draining the queued steer events and persisting accepted
// messages. Declared in types (not agent/interfaces) because both packages
// need the shape and neither may import the other; the engine accepts any
// value implementing it.
type SteerSink interface {
	// PollSteer returns pending inject events as {id, content, mentioned_items,
	// channel} maps. lastOffset is unused: consumed flags (not a numeric
	// offset) are what skip already-handled events, so an after→inject
	// promote of a previously skipped item stays visible. An empty/absent
	// queue is an empty result. PersistSteerMessage is what marks an event
	// consumed, after the user row exists.
	PollSteer(ctx context.Context, sessionID, messageID string, lastOffset int) ([]map[string]interface{}, int, error)
	// PersistSteerMessage stores the accepted message as a user-role row
	// under the run's request ID and returns the new row's ID. An empty ID
	// means persistence failed: the caller must not append the text, so the
	// next drain can retry. Mentions are recorded for history only — the
	// running turn's scope is not widened mid-flight.
	// channel is the source of the steered send ("web", "api", "im"); empty
	// is stored as "web".
	PersistSteerMessage(
		ctx context.Context, sessionID, messageID, steerID, content string,
		mentionedItems MentionedItems,
		channel string,
	) string
}

// QARequest consolidates all parameters for KnowledgeQA and AgentQA service calls,
// replacing the previous 14-parameter method signatures.
// EventBus is passed separately to avoid circular dependency with the event package.
type QARequest struct {
	Session             *Session           // The conversation session
	Query               string             // User query text
	AssistantMessageID  string             // Pre-created assistant message ID
	SummaryModelID      string             // Optional model override; empty = use agent/KB default
	CustomAgent         *CustomAgent       // Optional custom agent for config override
	SharedAgentReadOnly bool               // True only when access came from an agent share; source-workspace writes are forbidden
	KnowledgeBaseIDs    []string           // Knowledge base IDs to search (from request + @mentions)
	KnowledgeIDs        []string           // Specific knowledge (file) IDs to search
	TagScopes           []TagScope         // Tag-constrained KB scopes from @mentions
	MCPServiceIDs       []string           // Per-request MCP service IDs from @mentions
	SkillNames          []string           // Per-request skill names from @mentions
	ImageURLs           []string           // Image URLs for multimodal input
	ImageDescription    string             // VLM-generated image description (fallback for non-vision models)
	UserMessageID       string             // Created user message ID
	LocalBrowserEnabled bool               // Explicit browser source preference for this request
	WebSearchEnabled    bool               // Whether web search is enabled for this request
	QuotedContext       string             // Quoted message content from IM quote-reply (appended at LLM prompt stage, not used for retrieval)
	Attachments         MessageAttachments // File attachments (processed and ready for prompt injection)
	// SteerSink, when set, enables mid-run message injection for this run:
	// the engine drains user-appended messages at every round boundary and
	// persists accepted ones through this sink. A structural interface so
	// neither package imports the other; handler-owned, nil for IM/embed.
	SteerSink SteerSink
}
