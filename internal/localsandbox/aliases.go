package localsandbox

import "github.com/Tencent/WeKnora/internal/localsandbox/core"

// The facade re-exports the core contract as aliases (not new types), so a
// core.Policy and a localsandbox.Policy are the same type to the compiler and
// no conversion is ever needed at the boundary.
type (
	Policy            = core.Policy
	WritableRoot      = core.WritableRoot
	NetworkMode       = core.NetworkMode
	Command           = core.Command
	ExitStatus        = core.ExitStatus
	Denial            = core.Denial
	DenialReason      = core.DenialReason
	Backend           = core.Backend
	Prepared          = core.Prepared
	Process           = core.Process
	PathGuard         = core.PathGuard
	Workspace         = core.Workspace
	WorkspaceKind     = core.WorkspaceKind
	WorkspaceResolver = core.WorkspaceResolver
	ProjectLookup     = core.ProjectLookup
	DirLayout         = core.DirLayout
	ApprovalMode      = core.ApprovalMode
	PolicyBuilder     = core.PolicyBuilder
	Grant             = core.Grant
)

const (
	NetworkDenied       = core.NetworkDenied
	NetworkLoopback     = core.NetworkLoopback
	NetworkUnrestricted = core.NetworkUnrestricted

	ModeAsk  = core.ModeAsk
	ModeAuto = core.ModeAuto
	ModeFull = core.ModeFull

	WorkspaceProject = core.WorkspaceProject
	WorkspaceSession = core.WorkspaceSession

	DenialNone                  = core.DenialNone
	DenialOperationNotPermitted = core.DenialOperationNotPermitted
	DenialPermissionDenied      = core.DenialPermissionDenied
	DenialReadOnlyFileSystem    = core.DenialReadOnlyFileSystem
	DenialPolicy                = core.DenialPolicy
)

var (
	ErrUnsupportedPlatform    = core.ErrUnsupportedPlatform
	ErrFullAccessHasNoPolicy  = core.ErrFullAccessHasNoPolicy
	ErrApprovalModeNotShipped = core.ErrApprovalModeNotShipped
	ErrPathDenied             = core.ErrPathDenied
)

// ParseApprovalMode normalizes a stored preference into a mode this build can
// serve. Callers reading user input go through it so the shipped-mode list
// stays in one place.
func ParseApprovalMode(raw string) ApprovalMode { return core.ParseApprovalMode(raw) }

// Constructors are forwarded as thin functions rather than function-valued
// vars so they keep their doc comments and cannot be reassigned.

func NewPathGuard(p Policy) *PathGuard { return core.NewPathGuard(p) }

func NewPolicyBuilder(homeDir, appDataDir string) *PolicyBuilder {
	return core.NewPolicyBuilder(homeDir, appDataDir)
}

func NewWorkspaceResolver(layout DirLayout, projects ProjectLookup) WorkspaceResolver {
	return core.NewWorkspaceResolver(layout, projects)
}

func PathUnder(child, root string) bool { return core.PathUnder(child, root) }

func ClassifyDenial(status ExitStatus, stdout, stderr string) Denial {
	return core.ClassifyDenial(status, stdout, stderr)
}

func ClassifyRunDenial(p Policy, status ExitStatus, stdout, stderr string) Denial {
	return core.ClassifyRunDenial(p, status, stdout, stderr)
}
