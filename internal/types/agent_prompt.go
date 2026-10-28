package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Kinds of human-in-the-loop prompts an agent run can raise.
const (
	AgentPromptKindToolApproval = "tool_approval"
	AgentPromptKindMCPOAuth     = "mcp_oauth"
	AgentPromptKindAskUser      = "ask_user"
)

// Lifecycle states of an AgentPrompt. Transitions are compare-and-set on the
// row, so every replica agrees on a single outcome.
const (
	// AgentPromptPending: an owner replica is blocked waiting in memory.
	AgentPromptPending = "pending"
	// AgentPromptSuspended: the run ended; an answer will resume it.
	AgentPromptSuspended = "suspended"
	// AgentPromptAnswered: the user responded; Response holds the answer.
	AgentPromptAnswered = "answered"
	// AgentPromptExecuting: the answer authorized a side effect that is
	// running; it must not be replayed if the executor disappears.
	AgentPromptExecuting = "executing"
	AgentPromptDone      = "done"
	AgentPromptFailed    = "failed"
	AgentPromptCanceled  = "canceled"
	AgentPromptExpired   = "expired"
	// AgentPromptSuperseded: the user moved on (new message, rewind,
	// deletion) while the prompt was suspended.
	AgentPromptSuperseded = "superseded"
)

// AgentPrompt is one prompt raised by an agent run. Kind decides the shape of
// Request and Response; the row is the source of truth for the answer and
// process memory only holds a wake-up hook.
type AgentPrompt struct {
	ID                 string `json:"id"                    gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64 `json:"tenant_id"             gorm:"not null"`
	UserID             string `json:"user_id"               gorm:"type:varchar(64);not null;default:''"`
	SessionID          string `json:"session_id"            gorm:"type:varchar(36);not null"`
	AssistantMessageID string `json:"assistant_message_id"  gorm:"type:varchar(36);not null;default:''"`
	RequestID          string `json:"request_id"            gorm:"type:varchar(128);not null;default:''"`
	Kind               string `json:"kind"                  gorm:"type:varchar(32);not null"`
	ToolCallID         string `json:"tool_call_id"          gorm:"type:varchar(255);not null;default:''"`
	// Suspendable prompts may outlive the run that raised them. Prompts raised
	// while an external operation is in flight cannot.
	Suspendable bool   `json:"suspendable"           gorm:"not null;default:false"`
	Request     JSON   `json:"request,omitempty"`
	Response    JSON   `json:"response,omitempty"`
	Status      string `json:"status"                gorm:"type:varchar(32);not null"`
	// CloseReason explains a system-side ending (timeout, cancel, supersede).
	CloseReason   string     `json:"close_reason"          gorm:"type:text;not null;default:''"`
	ResolvedBy    string     `json:"resolved_by"           gorm:"type:varchar(64);not null;default:''"`
	ResolvedAt    *time.Time `json:"resolved_at,omitempty"`
	OwnerInstance string     `json:"owner_instance"        gorm:"type:varchar(64);not null;default:''"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// TableName pins the table name.
func (AgentPrompt) TableName() string { return "agent_prompts" }

// BeforeCreate sets ID for AgentPrompt.
func (p *AgentPrompt) BeforeCreate(_ *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return nil
}

// IsOpen reports whether the prompt still waits for an answer.
func (p *AgentPrompt) IsOpen() bool {
	return p != nil && (p.Status == AgentPromptPending || p.Status == AgentPromptSuspended)
}

// AgentPromptTransition is a compare-and-set update applied only when the
// row is in one of the From states. Nil / empty fields are left unchanged.
type AgentPromptTransition struct {
	From        []string
	To          string
	Response    JSON
	CloseReason *string
	ResolvedBy  *string
	ResolvedAt  *time.Time
	ExpiresAt   *time.Time
	// Owner and LeaseUntil hand the row to a new owner replica.
	Owner      *string
	LeaseUntil *time.Time
	// UnexpiredAt additionally requires the row not to have expired at that
	// time (expires_at empty or later).
	UnexpiredAt *time.Time
	// ExpectedOwner and LeaseBefore fence a stale reader against a takeover
	// or renewal between reading the row and updating it.
	ExpectedOwner *string
	LeaseBefore   *time.Time
}

// AgentRunSuspension is the persisted state of a run blocked on a prompt, so
// any replica can resume it once the prompt is answered. Snapshot is opaque
// outside the agent engine and versioned by SnapshotVersion.
type AgentRunSuspension struct {
	AssistantMessageID string    `json:"assistant_message_id" gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64    `json:"tenant_id"            gorm:"not null"`
	SessionID          string    `json:"session_id"           gorm:"type:varchar(36);not null"`
	PromptID           string    `json:"prompt_id"            gorm:"type:varchar(36);not null"`
	SnapshotVersion    int       `json:"snapshot_version"     gorm:"not null"`
	Snapshot           JSON      `json:"snapshot"             gorm:"not null"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// TableName pins the table name.
func (AgentRunSuspension) TableName() string { return "agent_run_suspensions" }

// PromptSuspension describes a run that ended waiting on a prompt.
type PromptSuspension struct {
	// Detached is runtime-only: another owner already controls the message.
	Detached  bool       `json:"-"`
	PromptID  string     `json:"pending_id"`
	Kind      string     `json:"kind"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// PendingPromptView restores both open prompts and resolved timeline records.
type PendingPromptView struct {
	Status        string     `json:"status,omitempty"`
	Response      JSON       `json:"response,omitempty"`
	CloseReason   string     `json:"close_reason,omitempty"`
	DelegatedTask string     `json:"delegated_task,omitempty"`
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	ToolCallID    string     `json:"tool_call_id"`
	Request       JSON       `json:"request,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// AgentPromptHistoryStatuses includes answered and terminal prompts for timeline replay.
var AgentPromptHistoryStatuses = []string{
	AgentPromptPending,
	AgentPromptSuspended,
	AgentPromptAnswered,
	AgentPromptExecuting,
	AgentPromptDone,
	AgentPromptFailed,
	AgentPromptCanceled,
	AgentPromptExpired,
	AgentPromptSuperseded,
}

// TimelineView returns the prompt fields needed to restore a historical timeline card.
func (p *AgentPrompt) TimelineView() *PendingPromptView {
	return &PendingPromptView{
		ID: p.ID, Kind: p.Kind, ToolCallID: p.ToolCallID,
		Request: p.Request, Response: p.Response, Status: p.Status, CloseReason: p.CloseReason,
		ExpiresAt: p.ExpiresAt, CreatedAt: p.CreatedAt,
	}
}

// ResumableAgentRequest is the part of a QARequest needed to rebuild the run
// on another replica. It holds the original query and durable request settings.
type ResumableAgentRequest struct {
	// ActingVia keeps scheduler-owned runs out of ordinary chat recovery.
	ActingVia string `json:"acting_via,omitempty"`
	AgentID   string `json:"agent_id"`
	Query     string `json:"query,omitempty"`
	// AgentTenantID is the agent owner's workspace; it differs from the
	// session's when the agent is shared.
	AgentTenantID       uint64          `json:"agent_tenant_id,omitempty"`
	SummaryModelID      string          `json:"summary_model_id,omitempty"`
	ReasoningEffort     string          `json:"reasoning_effort,omitempty"`
	SharedAgentReadOnly bool            `json:"shared_agent_read_only,omitempty"`
	KnowledgeBaseIDs    []string        `json:"knowledge_base_ids,omitempty"`
	KnowledgeIDs        []string        `json:"knowledge_ids,omitempty"`
	TagScopes           []TagScope      `json:"tag_scopes,omitempty"`
	MCPServiceIDs       []string        `json:"mcp_service_ids,omitempty"`
	SkillNames          []string        `json:"skill_names,omitempty"`
	WebSearchEnabled    bool            `json:"web_search_enabled,omitempty"`
	QuestionOrigin      *QuestionOrigin `json:"question_origin,omitempty"`
	PageContext         *PageContext    `json:"page_context,omitempty"`
	// AssistantAccess is the level the user asked for; the ceiling is
	// applied again on resume, so lowering it in between takes effect.
	AssistantAccess AssistantAccessLevel `json:"assistant_access,omitempty"`
	UserMessageID   string               `json:"user_message_id,omitempty"`
	Language        string               `json:"language,omitempty"`
}

// ToolApprovalPromptRequest is the Request of an AgentPromptKindToolApproval.
type ToolApprovalPromptRequest struct {
	ServiceID          string          `json:"service_id"`
	ServiceName        string          `json:"service_name"`
	MCPToolName        string          `json:"mcp_tool_name"`
	RegisteredToolName string          `json:"registered_tool_name"`
	Description        string          `json:"description,omitempty"`
	Args               json.RawMessage `json:"args,omitempty"`
	// Grant, when set, names the kind of call this approval can allow for the
	// rest of the session: approving with AllowInSession lets later calls
	// with the same Grant in the same session run without asking. The server
	// derives it from the call; GrantScope describes it to the user.
	Grant      string `json:"grant,omitempty"`
	GrantScope string `json:"grant_scope,omitempty"`
	// GrantLapsed, when set, is why an earlier grant for this call no longer
	// applies (an AgentGrantLapse value).
	GrantLapsed string `json:"grant_lapsed,omitempty"`
	// GrantRisk is the risk Grant would be given for, and GrantLabel its
	// description, whether or not the card offers it: an automation's owner
	// may later allow the same call for its future runs.
	GrantRisk  string `json:"grant_risk,omitempty"`
	GrantLabel string `json:"grant_label,omitempty"`
}

// ToolApprovalPromptResponse is the Response of an AgentPromptKindToolApproval.
type ToolApprovalPromptResponse struct {
	Approved bool `json:"approved"`
	// ModifiedArgs, when set on approval, replaces the original arguments.
	ModifiedArgs json.RawMessage `json:"modified_args,omitempty"`
	Reason       string          `json:"reason,omitempty"`
	// AllowInSession approves later calls with the request's Grant too.
	AllowInSession bool `json:"allow_in_session,omitempty"`
	// Scope is "once" or "session" for Lite host approvals; empty means once.
	Scope string `json:"scope,omitempty"`
	// Access narrows a host grant to "read"; empty keeps the proposal.
	Access string `json:"access,omitempty"`
}

// MCPOAuthPromptRequest is the Request of an AgentPromptKindMCPOAuth.
type MCPOAuthPromptRequest struct {
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	MCPToolName string `json:"mcp_tool_name,omitempty"`
}

// MCPOAuthPromptResponse is the Response of an AgentPromptKindMCPOAuth.
type MCPOAuthPromptResponse struct {
	Authorized bool   `json:"authorized"`
	Reason     string `json:"reason,omitempty"`
}
