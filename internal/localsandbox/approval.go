package localsandbox

import "context"

// ApprovalReason says why the sandbox is asking.
type ApprovalReason string

const (
	// ReasonDelete asks before a command that deletes files runs.
	ReasonDelete ApprovalReason = "delete"
	// ReasonDenied asks to widen the sandbox after it blocked a command.
	ReasonDenied ApprovalReason = "sandbox_denied"
	// ReasonFileWrite asks before a file tool writes outside the session scope.
	ReasonFileWrite ApprovalReason = "file_write"
)

// ReviewSpec is what an approval reads: the action as the model wrote it.
// The executed command only adds code-generated wrapping (stdin pipe, skill
// environment) around it.
type ReviewSpec struct {
	Tool    string
	Command string
	// Stdin is what the command receives. StdinIsProgram says it runs as code,
	// so delete detection reads it too; data is only shown to the approver.
	Stdin          string
	StdinIsProgram bool
	// Env is what the model passed in the env parameter. Stored skill
	// credentials are added by code later and never appear here.
	Env       map[string]string
	SkillName string
}

// Text is the program text delete detection reads. Data on stdin is left out:
// a file full of the word rm is not a delete.
func (s ReviewSpec) Text() string {
	if s.Stdin == "" || !s.StdinIsProgram {
		return s.Command
	}
	return s.Command + "\n" + s.Stdin
}

// TargetFact is metadata about a path an action touches. It never carries
// file contents.
type TargetFact struct {
	Path    string
	Type    string // "file" | "directory" | "missing" | "other"
	Size    int64
	Entries int
}

// ApprovalRequest is one question for the approver.
type ApprovalRequest struct {
	SessionID     string
	Spec          ReviewSpec
	Cwd           string
	WorkspaceRoot string
	Reason        ApprovalReason
	// Hidden marks a delete the segment split cannot name (bash -c, $(…)).
	Hidden bool
	// Proposed is what approving opens: a directory, or a single file.
	Proposed      *Grant
	DenialSnippet string
	// FirstAttemptRan warns that approving re-runs the whole command.
	FirstAttemptRan bool
	// AllowSession is false when only a one-time answer is acceptable.
	AllowSession bool
	// SessionRules is what a session approval of a delete remembers.
	SessionRules []string
	// WriteMode, WriteContent and EditDiff describe a file tool write.
	// WriteBytes is the size of the file after the write. An edit leaves
	// WriteContent empty and puts the diff in EditDiff, so the card reads
	// the size from here.
	WriteMode    string
	WriteContent string
	WriteBytes   int
	EditDiff     string
	// Targets are the paths the action touches. TargetsComplete is false when
	// more than maxReviewTargets were found, so a reviewer must not approve.
	Targets         []TargetFact
	TargetsComplete bool
	// Scripts are the agent-writable scripts the command runs. ScriptsIncomplete
	// means one could not be shown, so a reviewer must not approve.
	Scripts           []ScriptFact
	ScriptsIncomplete bool
}

// ApprovalDecision is the approver's answer.
type ApprovalDecision struct {
	Approved bool
	// Session remembers the answer for the rest of this session. Service
	// honours it only in ModeAsk.
	Session bool
	// Grant, when set, may only narrow Proposed from write to read.
	Grant *Grant
	// Reason is returned to the model when the approver refuses.
	Reason string
	// RiskLevel is the review model's assessment; empty for the user.
	RiskLevel string
}

// Approver answers one question and blocks until it has. Any error is a
// refusal.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (ApprovalDecision, error)
}
