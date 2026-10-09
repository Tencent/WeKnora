package localsandbox

import "context"

// ApprovalReason says why the sandbox is asking the user.
type ApprovalReason string

const (
	// ReasonDelete asks before a command that deletes files runs.
	ReasonDelete ApprovalReason = "delete"
	// ReasonDangerous asks before a recursive delete of root or home. It can
	// only be approved once.
	ReasonDangerous ApprovalReason = "dangerous"
	// ReasonDenied asks to widen the sandbox after it blocked a command.
	ReasonDenied ApprovalReason = "sandbox_denied"
)

// ApprovalRequest is one question for the user.
type ApprovalRequest struct {
	SessionID string
	// Command is what the model wrote, before stdin or skill wrapping.
	Command string
	Cwd     string
	Reason  ApprovalReason
	// Proposed is set only for ReasonDenied.
	Proposed      *Grant
	DenialSnippet string
	// FirstAttemptRan warns that approving re-runs the whole command.
	FirstAttemptRan bool
	// AllowSession is false when only a one-time answer is acceptable.
	AllowSession bool
	// SessionRules is what a session approval of a delete remembers, shown
	// on the card: later deletes these rules cover are not asked again.
	SessionRules []string
}

// ApprovalDecision is the user's answer.
type ApprovalDecision struct {
	Approved bool
	// Session remembers the answer for the rest of this session.
	Session bool
	// Grant, when set, may only narrow Proposed from write to read.
	Grant *Grant
	// Reason is returned to the model when the user refuses.
	Reason string
}

// Approver asks the user and blocks for the answer. Any error is a refusal.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}
