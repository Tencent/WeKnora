package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SessionKindAutomation holds the conversations of automation runs; they are
// opened from the automation, not the ordinary chat list.
const SessionKindAutomation = "automation"

// Automation trigger types.
const (
	AutomationTriggerCron = "cron"
	AutomationTriggerOnce = "once"
)

// Automation session modes.
const (
	// AutomationSessionPerRun starts a new conversation for every run.
	AutomationSessionPerRun = "per_run"
	// AutomationSessionPersistent writes every run into one conversation.
	AutomationSessionPersistent = "persistent"
)

// Automation misfire and overlap policies.
const (
	AutomationPolicySkip    = "skip"
	AutomationPolicyRunOnce = "run_once"
	AutomationPolicyQueue   = "queue"
)

// Automation run trigger kinds.
const (
	AutomationRunSchedule = "schedule"
	AutomationRunManual   = "manual"
	AutomationRunMisfire  = "misfire"
)

// Automation run statuses.
const (
	AutomationRunQueued           = "queued"
	AutomationRunRunning          = "running"
	AutomationRunAwaitingApproval = "awaiting_approval"
	AutomationRunSucceeded        = "succeeded"
	AutomationRunFailed           = "failed"
	AutomationRunCancelled        = "cancelled"
	AutomationRunSkipped          = "skipped"
	// AutomationRunNeedsAttention: the executor died mid-run, so whether its
	// tool calls took effect is unknown. It is never replayed.
	AutomationRunNeedsAttention = "needs_attention"
)

// AutomationRunActive reports whether a run in status still holds the
// automation: it is executing or waiting for the user.
func AutomationRunActive(status string) bool {
	return status == AutomationRunRunning || status == AutomationRunAwaitingApproval
}

// AutomationRunFinished reports whether status is final.
func AutomationRunFinished(status string) bool {
	switch status {
	case AutomationRunSucceeded, AutomationRunFailed, AutomationRunCancelled,
		AutomationRunSkipped, AutomationRunNeedsAttention:
		return true
	}
	return false
}

// AutomationTrigger is when an automation runs.
type AutomationTrigger struct {
	// Type is cron or once.
	Type string `json:"type" extensions:"x-agent"`
	// Expr is a cron expression: five fields (minute first) or six (seconds
	// first), or a descriptor such as @daily.
	Expr string `json:"expr,omitempty" extensions:"x-agent"`
	// At is when a once trigger fires, RFC3339 or a wall-clock time in
	// Timezone, such as "2006-01-02T15:04:05" or "2006-01-02 15:04".
	At string `json:"at,omitempty" extensions:"x-agent"`
	// Timezone is an IANA zone, e.g. Asia/Shanghai; empty means UTC.
	Timezone string `json:"timezone,omitempty" extensions:"x-agent"`
}

// AutomationLimits bound one run.
type AutomationLimits struct {
	// MaxDurationMinutes caps a run's execution time, not counting time spent
	// waiting for approval. 0 uses the default.
	MaxDurationMinutes int `json:"max_duration_minutes,omitempty" extensions:"x-agent"`
}

// Automation is a task the WeKnora assistant runs on a schedule on its owner's behalf.
type Automation struct {
	ID       string `json:"id"        gorm:"type:varchar(36);primaryKey" extensions:"x-agent=automation_id,x-agent-response"`
	TenantID uint64 `json:"tenant_id" gorm:"not null;index:idx_automations_owner"`
	// OwnerUserID is the account the automation runs as.
	OwnerUserID string `json:"owner_user_id" gorm:"type:varchar(36);not null;index:idx_automations_owner"`
	Name        string `json:"name"          gorm:"type:varchar(255);not null" extensions:"x-agent"`
	Description string `json:"description"   gorm:"type:text" extensions:"x-agent"`
	Enabled     bool   `json:"enabled"       gorm:"not null;default:true;index:idx_automations_due" extensions:"x-agent,x-agent-response"`
	// ModelID overrides the assistant's chat model; empty follows the assistant default.
	ModelID string `json:"model_id" gorm:"type:varchar(64);not null;default:''" extensions:"x-agent"`
	// Instruction is the task, sent as the user message of each run after
	// its variables are filled in.
	Instruction string            `json:"instruction" gorm:"type:text;not null" extensions:"x-agent,x-agent-detail"`
	Trigger     AutomationTrigger `json:"trigger"     gorm:"column:trigger_config;serializer:json;type:text;not null" extensions:"x-agent"`
	SessionMode string            `json:"session_mode" gorm:"type:varchar(16);not null;default:'per_run'" extensions:"x-agent"`
	// PersistentSessionID is the conversation of a persistent automation,
	// created on its first run.
	PersistentSessionID string `json:"persistent_session_id" gorm:"type:varchar(36);not null;default:''"`
	// CarryLastResult adds the last successful answer to each per-run
	// conversation.
	CarryLastResult bool   `json:"carry_last_result" gorm:"not null;default:false" extensions:"x-agent"`
	MisfirePolicy   string `json:"misfire_policy"    gorm:"type:varchar(16);not null;default:'skip'" extensions:"x-agent"`
	OverlapPolicy   string `json:"overlap_policy"    gorm:"type:varchar(16);not null;default:'skip'" extensions:"x-agent"`
	// AccessLevel is how far runs may change data without asking, capped by
	// the workspace's assistant ceiling.
	AccessLevel AssistantAccessLevel `json:"access_level" gorm:"type:varchar(16);not null;default:'confirm'" extensions:"x-agent"`
	// ApprovalTimeoutMinutes is how long a run waits for an approval before
	// it fails.
	ApprovalTimeoutMinutes int              `json:"approval_timeout_minutes" gorm:"not null;default:1440" extensions:"x-agent"`
	Limits                 AutomationLimits `json:"limits"                   gorm:"serializer:json;type:text;not null"`
	NotifyOnSuccess        bool             `json:"notify_on_success"        gorm:"not null;default:false" extensions:"x-agent"`

	NextRunAt     *time.Time `json:"next_run_at"     gorm:"index:idx_automations_due" extensions:"x-agent,x-agent-response"`
	LastRunAt     *time.Time `json:"last_run_at"     extensions:"x-agent,x-agent-response"`
	LastSuccessAt *time.Time `json:"last_success_at" extensions:"x-agent,x-agent-response"`
	LastRunStatus string     `json:"last_run_status" gorm:"type:varchar(32);not null;default:''" extensions:"x-agent,x-agent-response"`
	// ActiveRunStatus is projected from live runs and is not persisted.
	ActiveRunStatus     string `json:"active_run_status" gorm:"-" extensions:"x-agent,x-agent-response"`
	ConsecutiveFailures int    `json:"consecutive_failures" gorm:"not null;default:0"`
	PausedReason        string `json:"paused_reason"   gorm:"type:text;not null;default:''" extensions:"x-agent,x-agent-response"`
	// Version guards claiming a due run against other instances.
	Version   int64          `json:"-"          gorm:"not null;default:0"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"-"          gorm:"index"`
}

// BeforeCreate sets the ID.
func (a *Automation) BeforeCreate(*gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	return nil
}

// AutomationRun is one execution of an automation.
type AutomationRun struct {
	ID           string `json:"id"            gorm:"type:varchar(36);primaryKey" extensions:"x-agent=run_id,x-agent-response"`
	AutomationID string `json:"automation_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_automation_runs_slot,priority:1;index:idx_automation_runs_automation"` //nolint:lll // one-line struct tag
	TenantID     uint64 `json:"tenant_id"     gorm:"not null"`
	// ScheduledAt is when the run was due; for a manual run, when it was
	// asked for.
	ScheduledAt time.Time `json:"scheduled_at" gorm:"not null;uniqueIndex:idx_automation_runs_slot,priority:2" extensions:"x-agent,x-agent-response"`
	TriggerKind string    `json:"trigger_kind" gorm:"type:varchar(16);not null;uniqueIndex:idx_automation_runs_slot,priority:3" extensions:"x-agent,x-agent-response"`
	Status      string    `json:"status"       gorm:"type:varchar(32);not null;index:idx_automation_runs_status" extensions:"x-agent,x-agent-response"`
	SessionID   string    `json:"session_id"   gorm:"type:varchar(36);not null;default:''" extensions:"x-agent,x-agent-response"`
	// UserMessageID and AssistantMessageID are the run's turn in SessionID.
	UserMessageID      string `json:"user_message_id"      gorm:"type:varchar(36);not null;default:''"`
	AssistantMessageID string `json:"assistant_message_id" gorm:"type:varchar(36);not null;default:''"`
	// Owner and LeaseUntil fence execution: only the instance holding an
	// unexpired lease may finish the run.
	Owner      string     `json:"-"           gorm:"type:varchar(36);not null;default:''"`
	LeaseUntil *time.Time `json:"-"`
	// PendingPromptID is the approval a waiting run is suspended on.
	PendingPromptID string `json:"pending_prompt_id" gorm:"type:varchar(36);not null;default:''"`
	// Resume asks the executor to continue the run from PendingPromptID's
	// answer rather than start it.
	Resume bool `json:"-" gorm:"not null;default:false"`
	// CancelRequested stops the run at its next check.
	CancelRequested bool `json:"cancel_requested" gorm:"not null;default:false"`
	// ExecutionDurationMS accumulates execution segments across approvals;
	// queued time and time spent waiting for an answer do not consume it.
	ExecutionDurationMS int64 `json:"-" gorm:"not null;default:0"`
	// Answer is the start of the final answer, for listings.
	Answer     string     `json:"answer"      gorm:"type:text;not null;default:''" extensions:"x-agent,x-agent-response"`
	Error      string     `json:"error"       gorm:"type:text;not null;default:''" extensions:"x-agent,x-agent-response"`
	Usage      JSON       `json:"usage"       gorm:"type:text"`
	StartedAt  *time.Time `json:"started_at"  extensions:"x-agent,x-agent-response"`
	FinishedAt *time.Time `json:"finished_at" extensions:"x-agent,x-agent-response"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// BeforeCreate sets the ID.
func (r *AutomationRun) BeforeCreate(*gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

// AutomationPatch creates an automation, or changes the fields it carries
// on an existing one; a field left out is kept.
type AutomationPatch struct {
	// What the automation is called
	Name *string `json:"name" extensions:"x-agent"`
	// What it is for
	Description *string `json:"description" extensions:"x-agent"`
	// Optional chat model from model.list; empty follows the assistant default
	ModelID *string `json:"model_id" extensions:"x-agent"`
	// The task, sent as the user message of each run. May name now,
	// last_run_at, last_success_at, run_id or automation_name inside double
	// curly braces to have it filled in
	Instruction *string `json:"instruction" extensions:"x-agent,x-agent-max-bytes=32000"`
	// When it runs
	Trigger *AutomationTrigger `json:"trigger" extensions:"x-agent"`
	// per_run starts a new conversation each run; persistent keeps one
	SessionMode *string `json:"session_mode" enums:"per_run,persistent" extensions:"x-agent"`
	// Whether each per_run conversation gets the last successful answer
	CarryLastResult *bool `json:"carry_last_result" extensions:"x-agent"`
	// What happens to runs missed while the service was down or paused
	MisfirePolicy *string `json:"misfire_policy" enums:"skip,run_once" extensions:"x-agent"`
	// What happens when a run is due while the previous one is still going
	OverlapPolicy *string `json:"overlap_policy" enums:"skip,queue" extensions:"x-agent"`
	// How far runs may change data without asking
	AccessLevel *AssistantAccessLevel `json:"access_level" enums:"read_only,confirm,auto_write" extensions:"x-agent"`
	// How long a run waits for an approval before it fails, in minutes
	ApprovalTimeoutMinutes *int              `json:"approval_timeout_minutes" extensions:"x-agent"`
	Limits                 *AutomationLimits `json:"limits"`
	// Whether a successful run notifies the owner
	NotifyOnSuccess *bool `json:"notify_on_success" extensions:"x-agent"`
	// Whether it is scheduled; on update use pause and resume instead
	Enabled *bool `json:"enabled"`
}

// AutomationActingVia is the Principal.ActingVia of a run of automation id.
func AutomationActingVia(id string) string { return "automation:" + id }

// Notification kinds.
const (
	NotificationAutomationSucceeded        = "automation.succeeded"
	NotificationAutomationFailed           = "automation.failed"
	NotificationAutomationAwaitingApproval = "automation.awaiting_approval"
	NotificationAutomationPaused           = "automation.paused"
)

// Notification is an in-app message for one user.
type Notification struct {
	ID       string `json:"id"        gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"not null;index:idx_notifications_user,priority:1"`
	UserID   string `json:"-"         gorm:"type:varchar(36);not null;index:idx_notifications_user,priority:2"`
	Kind     string `json:"kind"      gorm:"type:varchar(64);not null"`
	Title    string `json:"title"     gorm:"type:varchar(255);not null"`
	Body     string `json:"body"      gorm:"type:text;not null;default:''"`
	// Link is an in-app route the notification opens.
	Link       string     `json:"link"        gorm:"type:text;not null;default:''"`
	SourceType string     `json:"source_type" gorm:"type:varchar(32);not null;default:''"`
	SourceID   string     `json:"source_id"   gorm:"type:varchar(36);not null;default:''"`
	ReadAt     *time.Time `json:"read_at"`
	CreatedAt  time.Time  `json:"created_at"  gorm:"index:idx_notifications_user,priority:3"`
}

// BeforeCreate sets the ID.
func (n *Notification) BeforeCreate(*gorm.DB) error {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	return nil
}
