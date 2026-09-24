// Package localsandbox runs untrusted commands on the user's own machine
// under OS-enforced restrictions. It is independent of the remote sandbox
// stack in internal/sandbox: the two share no domain model.
package localsandbox

import "github.com/Tencent/WeKnora/internal/localsandbox/core"

// The facade re-exports the core contract as aliases (not new types), so a
// core.Policy and a localsandbox.Policy are the same type to the compiler and
// no conversion is ever needed at the boundary.
type (
	// Policy is the platform-neutral permission declaration compiled by a Backend.
	Policy = core.Policy
	// WritableRoot is one writable subtree plus read-only carve-outs inside it.
	WritableRoot = core.WritableRoot
	// NetworkMode is the coarse network stance of a Policy.
	NetworkMode = core.NetworkMode
	// Command is one execution request.
	Command = core.Command
	// ExitStatus is the platform-neutral outcome of one sandboxed process.
	ExitStatus = core.ExitStatus
	// Denial is the heuristic verdict on whether a failure came from the sandbox.
	Denial = core.Denial
	// DenialReason classifies why a sandboxed process is believed to have failed.
	DenialReason = core.DenialReason
	// Backend is the contract every platform implementation satisfies.
	Backend = core.Backend
	// Prepared is an opaque handle to one compiled policy.
	Prepared = core.Prepared
	// Process is a running sandboxed command.
	Process = core.Process
	// PathGuard validates filesystem access performed by WeKnora itself.
	PathGuard = core.PathGuard
	// Workspace is where one session's work happens.
	Workspace = core.Workspace
	// WorkspaceKind distinguishes a user-picked project from a session dir.
	WorkspaceKind = core.WorkspaceKind
	// WorkspaceResolver maps a session to the directory the agent may work in.
	WorkspaceResolver = core.WorkspaceResolver
	// ProjectLookup reports whether a session is bound to a user-selected project.
	ProjectLookup = core.ProjectLookup
	// DirLayout names the roots the resolver allocates under.
	DirLayout = core.DirLayout
	// ApprovalMode is the user's chosen permission stance for a session.
	ApprovalMode = core.ApprovalMode
	// PolicyBuilder derives a Policy from an approval mode and a workspace.
	PolicyBuilder = core.PolicyBuilder
	// Grant is a permission the user has already approved.
	Grant = core.Grant
)

const (
	// NetworkDenied blocks all traffic. Default.
	NetworkDenied = core.NetworkDenied
	// NetworkLoopback permits only the ports listed in Policy.LoopbackPorts.
	NetworkLoopback = core.NetworkLoopback
	// NetworkUnrestricted applies no network restriction.
	NetworkUnrestricted = core.NetworkUnrestricted

	// ModeAsk reads the workspace but asks before every write or command.
	ModeAsk = core.ModeAsk
	// ModeAuto works freely inside the workspace. Default and currently shipped.
	ModeAuto = core.ModeAuto
	// ModeFull runs without any sandbox. Not shipped.
	ModeFull = core.ModeFull

	// WorkspaceProject is a directory the user picked.
	WorkspaceProject = core.WorkspaceProject
	// WorkspaceSession is auto-allocated per session under the session root.
	WorkspaceSession = core.WorkspaceSession

	// DenialNone means the failure was not classified as a sandbox denial.
	DenialNone = core.DenialNone
	// DenialOperationNotPermitted matches "operation not permitted" in process output.
	DenialOperationNotPermitted = core.DenialOperationNotPermitted
	// DenialPermissionDenied matches "permission denied" in process output.
	DenialPermissionDenied = core.DenialPermissionDenied
	// DenialReadOnlyFileSystem matches "read-only file system" in process output.
	DenialReadOnlyFileSystem = core.DenialReadOnlyFileSystem
	// DenialPolicy matches an explicit sandbox marker or a withheld-network failure.
	DenialPolicy = core.DenialPolicy
)

var (
	// ErrUnsupportedPlatform is returned where no OS-enforced sandbox exists.
	ErrUnsupportedPlatform = core.ErrUnsupportedPlatform
	// ErrFullAccessHasNoPolicy is returned when building a policy for ModeFull.
	ErrFullAccessHasNoPolicy = core.ErrFullAccessHasNoPolicy
	// ErrApprovalModeNotShipped is returned when Service is asked for an unshipped mode.
	ErrApprovalModeNotShipped = core.ErrApprovalModeNotShipped
	// ErrPathDenied is returned for every rejected path.
	ErrPathDenied = core.ErrPathDenied
	// ErrWorkspaceTooBroad is returned when a workspace or grant is too wide.
	ErrWorkspaceTooBroad = core.ErrWorkspaceTooBroad
	// ErrProjectDirRevoked is returned when a stored host project is no longer approved.
	ErrProjectDirRevoked = core.ErrProjectDirRevoked
)

// BuildCommandEnv filters the host environment and overlays explicit vars.
// Adapters must not pass os.Environ(); skill keys belong in the explicit map.
func BuildCommandEnv(explicit map[string]string, extraPATH []string) map[string]string {
	return core.BuildCommandEnv(explicit, extraPATH)
}

// ParseApprovalMode normalizes a stored preference. Unknown values become
// Auto; known-but-unshipped modes are returned unchanged so Service can refuse
// them instead of silently widening access.
func ParseApprovalMode(raw string) ApprovalMode { return core.ParseApprovalMode(raw) }

// NewPathGuard returns a PathGuard for p.
func NewPathGuard(p Policy) *PathGuard { return core.NewPathGuard(p) }

// NewPolicyBuilder returns a PolicyBuilder scoped to homeDir and appDataDir.
func NewPolicyBuilder(homeDir, appDataDir string) *PolicyBuilder {
	return core.NewPolicyBuilder(homeDir, appDataDir)
}

// NewWorkspaceResolver returns a WorkspaceResolver for layout and projects.
func NewWorkspaceResolver(layout DirLayout, projects ProjectLookup) WorkspaceResolver {
	return core.NewWorkspaceResolver(layout, projects)
}

// PathUnder reports whether child is inside root after cleaning both paths.
func PathUnder(child, root string) bool { return core.PathUnder(child, root) }

// ClassifyDenial inspects a failed execution for signs of a sandbox denial.
func ClassifyDenial(status ExitStatus, stdout, stderr string) Denial {
	return core.ClassifyDenial(status, stdout, stderr)
}

// ClassifyRunDenial is the verdict Service acts on, including network denials
// when the policy withheld the network.
func ClassifyRunDenial(p Policy, status ExitStatus, stdout, stderr string) Denial {
	return core.ClassifyRunDenial(p, status, stdout, stderr)
}
