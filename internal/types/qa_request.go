package types

import (
	"context"
	"strings"
)

// UploadOnlyQuestion is the question asked on the user's behalf when a message
// carries an image or file but no text. The stored user message keeps its
// empty text; only model input uses this question.
func UploadOnlyQuestion(locale string) string {
	if strings.HasPrefix(locale, "zh") {
		return "请根据我上传的内容回答。"
	}
	return "Please answer based on what I uploaded."
}

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

// QuestionOrigin names the knowledge source a suggested question was
// generated from. The client sends it with the question the user picked so
// the agent searches that source before answering; it is a hint inside the
// turn's resolved scope, never a scope change.
type QuestionOrigin struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id,omitempty"`
}

// MaxPageContextIssues bounds how many Wiki issues one page context names.
const MaxPageContextIssues = 20

// Page identifiers a client may report in PageContext.Page.
const (
	PageKnowledgeBase = "knowledge_base"
	PageWiki          = "wiki"
	PageAgentEditor   = "agent_editor"
	PageModelSettings = "settings_models"
	PageMCPServices   = "mcp_services"
	PageChat          = "chat"
)

var pageContextPages = map[string]struct{}{
	PageKnowledgeBase: {}, PageWiki: {}, PageAgentEditor: {},
	PageModelSettings: {}, PageMCPServices: {}, PageChat: {},
}

// IsPageContextPage reports whether page is a known page identifier.
func IsPageContextPage(page string) bool {
	_, ok := pageContextPages[page]
	return ok
}

// IsPageContextSection reports whether section looks like a UI section key
// such as "tools" or "knowledge": lowercase words joined by '_' or '-'.
func IsPageContextSection(section string) bool {
	if section == "" || len(section) > 32 || section[0] < 'a' || section[0] > 'z' {
		return false
	}
	for _, r := range section {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

// PageContext identifies the open page. Knowledge-base resources must be in
// the resolved search targets; agents are checked independently in the current tenant.
// Models, MCP services and chat messages are resolved for the assistant only:
// the first two in the current tenant, messages through the caller's session read access.
type PageContext struct {
	Page            string   `json:"page,omitempty"`
	Section         string   `json:"section,omitempty"` // part of the page in view, e.g. an editor tab
	AgentID         string   `json:"agent_id,omitempty"`
	KnowledgeBaseID string   `json:"knowledge_base_id,omitempty"`
	KnowledgeID     string   `json:"knowledge_id,omitempty"`
	WikiSlug        string   `json:"wiki_slug,omitempty"`
	WikiIssueIDs    []string `json:"wiki_issue_ids,omitempty"`
	ModelID         string   `json:"model_id,omitempty"`
	MCPServiceID    string   `json:"mcp_service_id,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	MessageID       string   `json:"message_id,omitempty"`
	// Title is the page's name as the client's navigation shows it, e.g.
	// "Agents"; it names pages without an identifier. Assistant only.
	Title string `json:"title,omitempty"`
	// View is the title of the dialog or drawer open over the page, as the
	// page shows it. Assistant only.
	View string `json:"view,omitempty"`
	// Selection is the text the user selected on the page. Assistant only.
	Selection *PageSelection `json:"selection,omitempty"`
}

// Bounds on the view and selection a page context carries.
const (
	PageViewMaxRunes           = 120
	PageSelectionMaxRunes      = 4000
	PageSelectionFieldMaxRunes = 200
)

// PageSelection is text the user selected on the page: in a text field
// (Field, Ref and the lines are set) or in the page's text.
type PageSelection struct {
	Text      string `json:"text"`
	Field     string `json:"field,omitempty"`      // the field's label
	Ref       string `json:"ref,omitempty"`        // the field's read_page ref
	StartLine int    `json:"start_line,omitempty"` // 1-based, multi-line fields only
	EndLine   int    `json:"end_line,omitempty"`
}

// Normalize trims and bounds a client-sent selection; nil when nothing is left.
func (s *PageSelection) Normalize() *PageSelection {
	if s == nil {
		return nil
	}
	text := truncateRunes(s.Text, PageSelectionMaxRunes)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out := &PageSelection{Text: text, Field: truncateRunes(strings.TrimSpace(s.Field), PageSelectionFieldMaxRunes)}
	if pageRefRE.MatchString(s.Ref) {
		out.Ref = s.Ref
	}
	if s.StartLine > 0 && s.EndLine >= s.StartLine {
		out.StartLine, out.EndLine = s.StartLine, s.EndLine
	}
	return out
}

// NormalizePageView trims and bounds a client-sent page or view title.
func NormalizePageView(view string) string {
	return truncateRunes(strings.Join(strings.Fields(view), " "), PageViewMaxRunes)
}

// QARequest consolidates all parameters for KnowledgeQA and AgentQA service calls,
// replacing the previous 14-parameter method signatures.
// EventBus is passed separately to avoid circular dependency with the event package.
type QARequest struct {
	Session             *Session           // The conversation session
	Query               string             // User query text
	AssistantMessageID  string             // Pre-created assistant message ID
	SummaryModelID      string             // Optional model override; empty = use agent/KB default
	ReasoningEffort     string             // Optional per-request override; empty = use agent default
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
	QuestionOrigin      *QuestionOrigin    // Source of a picked suggested question; a retrieval hint only
	PageContext         *PageContext       // Page the user has open; a hint inside the resolved scope only
	// AssistantAccess is the access level the user picked for the WeKnora
	// assistant; the workspace ceiling still caps it. Ignored by other agents.
	AssistantAccess AssistantAccessLevel
	// SteerSink, when set, enables mid-run message injection for this run:
	// the engine drains user-appended messages at every round boundary and
	// persists accepted ones through this sink. A structural interface so
	// neither package imports the other; handler-owned, nil for IM/embed.
	SteerSink SteerSink
	// TurnLeaseHeld reports that the caller already took the session's
	// send-side turn lease (and already rejected the send if a rewind holds
	// the session) before persisting this turn's messages. HTTP send does;
	// IM/MCP, which call the QA services directly, do not and leave this
	// false so the service takes the lease itself.
	TurnLeaseHeld bool
	// Suspendable lets the run end while it waits on a user prompt and be
	// resumed later. Set by callers whose client can render a suspended
	// message and answer it afterwards (web, embed, API); IM and MCP callers
	// wait synchronously.
	Suspendable bool
	// Mode says whether anyone can answer a prompt the agent raises, such as
	// an ask_user question or a tool approval. Empty is unattended: API-key,
	// IM and MCP-server callers cannot answer prompts.
	Mode RunMode
	// GrantSessionID is the session whose "allow for this session" grants
	// the run uses: the root conversation for a delegated child, empty for
	// the run's own session.
	GrantSessionID string
	// Resume, when set, continues a suspended run instead of starting one.
	Resume *AgentResume
	// RunNote is appended to the system prompt of this run only: how the
	// run was started, for runs nobody is watching.
	RunNote string
}

// AgentResume is what a resumed run continues from.
type AgentResume struct {
	// Snapshot is the run state saved when the run suspended.
	Snapshot []byte
	// Prompt is the answered prompt the run was suspended on.
	Prompt *AgentPrompt
}
