package sandbox

import "context"

// CommandApprovalRequest is one question for the user about a Lite host
// command. Reason is "delete", "dangerous" or "sandbox_denied".
type CommandApprovalRequest struct {
	SessionID string
	Command   string
	Cwd       string
	Reason    string
	// GrantPath and GrantAccess describe the widening asked for: after a
	// "sandbox_denied" block, or with a delete outside the workspace.
	GrantPath       string
	GrantAccess     string
	DenialSnippet   string
	FirstAttemptRan bool
	AllowSession    bool
	// SessionRules is what a session approval of a delete remembers, such as
	// "rm -f -r"; later deletes these rules cover are not asked again.
	SessionRules []string
}

// CommandApprovalDecision is the user's answer. Access may only narrow the
// proposed grant from "write" to "read".
type CommandApprovalDecision struct {
	Approved bool
	Session  bool
	Access   string
	Reason   string
}

// CommandApprover asks the user about a host command and blocks for the answer.
type CommandApprover interface {
	ApproveCommand(ctx context.Context, req CommandApprovalRequest) (CommandApprovalDecision, error)
}

// CommandApproval travels with one shell_exec call on a host workspace.
type CommandApproval struct {
	// Approver is nil when nobody can be asked; the host sandbox then treats
	// every question as refused.
	Approver CommandApprover
	// Command is what the model wrote, before stdin or skill wrapping.
	Command string
}

type commandApprovalKey struct{}

// WithCommandApproval attaches the approval port for one host command.
func WithCommandApproval(ctx context.Context, approval CommandApproval) context.Context {
	return context.WithValue(ctx, commandApprovalKey{}, approval)
}

// CommandApprovalFrom returns the approval port attached to ctx, if any.
func CommandApprovalFrom(ctx context.Context) (CommandApproval, bool) {
	approval, ok := ctx.Value(commandApprovalKey{}).(CommandApproval)
	return approval, ok
}

// SessionStateReleaser forgets per-session approvals when a chat is deleted.
type SessionStateReleaser interface {
	ReleaseSessionState(ctx context.Context, sessionID string)
}
