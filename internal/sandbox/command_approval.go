package sandbox

import "context"

// CommandApprovalRequest is one question about a Lite host action, for the
// user or the review model. Reason is "delete", "sandbox_denied" or "file_write".
type CommandApprovalRequest struct {
	SessionID       string
	Tool            string
	Command         string
	Stdin           string
	StdinIsProgram  bool
	Env             map[string]string
	SkillName       string
	Cwd             string
	WorkspaceRoot   string
	Reason          string // "delete" | "sandbox_denied" | "file_write"
	Hidden          bool
	GrantPath       string
	GrantAccess     string
	GrantKind       string // "file" | "directory"
	DenialSnippet   string
	FirstAttemptRan bool
	AllowSession    bool
	SessionRules    []string
	FilePath        string
	WriteMode       string
	WriteContent    string
	WriteBytes      int
	EditDiff        string
	Targets         []TargetFact
	TargetsComplete bool
	Scripts         []ScriptFact
	// ScriptsIncomplete means the command runs a workspace script that could
	// not be shown (binary or too large), so a reviewer must not approve.
	ScriptsIncomplete bool
}

// TargetFact is metadata about a path an action touches. It never carries
// file contents.
type TargetFact struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int64  `json:"size,omitempty"`
	Entries int    `json:"entries,omitempty"`
}

// ScriptFact is a script the agent could have written that a command runs.
type ScriptFact struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// CommandApprovalDecision is the user's answer. Access may only narrow the
// proposed grant from "write" to "read".
type CommandApprovalDecision struct {
	Approved  bool
	Session   bool
	Access    string
	Reason    string
	RiskLevel string
}

// CommandApprover asks the user about a host command and blocks for the answer.
type CommandApprover interface {
	ApproveCommand(ctx context.Context, req CommandApprovalRequest) (CommandApprovalDecision, error)
}

// CommandApproval travels with one shell_exec call on a host workspace.
type CommandApproval struct {
	Approver CommandApprover
	Reviewer CommandApprover
	Tool     string
	Command  string
	// Stdin is shown to whoever approves. StdinIsProgram says the command
	// runs it as code, so delete detection reads it too.
	Stdin          string
	StdinIsProgram bool
	Env            map[string]string
	SkillName      string
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

// FileChange is a write a file tool wants to make, described before any I/O
// so it can be approved with its content.
type FileChange struct {
	Path string
	// Mode is "overwrite", "append" or "edit".
	Mode string
	// Apply computes the new file from the current one.
	Apply func(current []byte, exists bool) ([]byte, error)
	// Preview is an edit's requested replacements, written by the model and
	// holding nothing from the file. It stands in for a diff whenever the
	// file may not be read before approval.
	Preview string
}

// SessionFileChangeAuthorizer approves one write outside the session's
// current scope. The returned context carries the approval for the rest of
// the tool call; it is ctx unchanged when no approval was needed.
type SessionFileChangeAuthorizer interface {
	AuthorizeFileChange(ctx context.Context, sessionID string, change FileChange) (context.Context, error)
}
