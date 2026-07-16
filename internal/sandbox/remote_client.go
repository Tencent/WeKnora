// Package sandbox: provider-neutral remote sandbox contract.
//
// This file introduces the RemoteSandboxClient interface and the neutral
// data-transfer types that SessionBoundManager depends on. Concrete backends
// (Cube, E2B, future E2B-compatible providers) each provide an implementation
// in a separate adapter file.
//
// The interface is deliberately minimal: it covers only the operations
// SessionBoundManager and RemoteSandbox use in production today. Optional
// provider capabilities (pause/resume, timeout refresh, metadata recovery,
// etc.) are exposed as RemoteSandboxCapabilities so higher layers can degrade
// gracefully instead of relying on backend-specific type assertions.
package sandbox

import (
	"context"
	"time"
)

// RemoteProvider identifies a remote sandbox backend. Values match the
// user-facing WEKNORA_SANDBOX_MODE strings and the existing SandboxType
// aliases so wiring/logging stays uniform.
type RemoteProvider = SandboxType

// RemoteSandboxHandle is an opaque, provider-issued reference to a live
// sandbox. Adapters may wrap their SDK-specific object (e.g.
// *cubesandbox.Sandbox, *e2b.Sandbox) inside the concrete handle type; the
// manager only reads the stable identifiers exposed here.
type RemoteSandboxHandle interface {
	// ID returns the provider-scoped sandbox identifier.
	ID() string

	// Provider returns the backend that issued this handle.
	Provider() RemoteProvider

	// Metadata returns the metadata originally recorded with the sandbox on
	// creation, when the provider preserves it. Returns nil when the provider
	// does not support metadata recovery.
	Metadata() map[string]string
}

// RemoteTimeoutMode describes how the remote provider should treat the
// requested idle timeout.
type RemoteTimeoutMode string

const (
	// RemoteTimeoutServerDefault leaves the timeout unspecified so the
	// provider applies its configured default.
	RemoteTimeoutServerDefault RemoteTimeoutMode = "server"

	// RemoteTimeoutExplicit uses RemoteTimeoutPolicy.Value verbatim. A zero
	// value asks for immediate on-timeout action; a negative value asks for
	// "never" when the provider supports it (adapters return
	// RemoteErrorKindUnsupported otherwise).
	RemoteTimeoutExplicit RemoteTimeoutMode = "explicit"
)

// RemoteTimeoutAction is the provider-side action taken when idle timeout
// elapses.
type RemoteTimeoutAction string

const (
	// RemoteOnTimeoutPause pauses the sandbox and preserves its filesystem
	// state so it can be resumed later.
	RemoteOnTimeoutPause RemoteTimeoutAction = "pause"

	// RemoteOnTimeoutKill destroys the sandbox and releases all resources.
	RemoteOnTimeoutKill RemoteTimeoutAction = "kill"
)

// RemoteTimeoutPolicy is the provider-neutral timeout configuration.
type RemoteTimeoutPolicy struct {
	Mode   RemoteTimeoutMode
	Value  time.Duration
	Action RemoteTimeoutAction
	// AutoResume asks the provider to resume a paused sandbox on the next
	// Connect. Adapters that cannot honour this must return
	// RemoteErrorKindUnsupported at Create time.
	AutoResume bool
}

// RemoteCreateRequest holds the neutral parameters for spawning a new sandbox.
type RemoteCreateRequest struct {
	// TemplateID references the pre-baked sandbox template. Required.
	TemplateID string

	// Timeout controls the idle-timeout policy.
	Timeout RemoteTimeoutPolicy

	// Metadata is a small key/value bag the provider stores alongside the
	// sandbox. WeKnora uses this to recover ownership of stray sandboxes
	// after a restart. Adapters that cannot persist metadata return
	// RemoteErrorKindUnsupported when non-empty metadata is supplied.
	Metadata map[string]string

	// EnvVars is baked into the sandbox at creation time. Optional.
	EnvVars map[string]string
}

// RemoteSandboxSummary is the neutral view of a sandbox listing / probe.
type RemoteSandboxSummary struct {
	// ID is the provider-scoped sandbox identifier.
	ID string

	// TemplateID is the template the sandbox was created from.
	TemplateID string

	// State is the normalized lifecycle state. See RemoteSandboxState.
	State RemoteSandboxState

	// RawState is the provider-native state string, retained for diagnostics
	// only. SessionBoundManager must not branch on RawState.
	RawState string

	// Metadata is the sandbox metadata bag. May be nil when the provider does
	// not support metadata.
	Metadata map[string]string

	// StartedAt records when the sandbox was created; zero value when the
	// provider does not report it.
	StartedAt time.Time

	// EndAt records when the sandbox was terminated; zero when unknown or
	// still running.
	EndAt time.Time
}

// RemoteSandboxState is the coordinator-facing lifecycle state.
type RemoteSandboxState string

const (
	// RemoteStateRunning: sandbox is up and reachable.
	RemoteStateRunning RemoteSandboxState = "running"

	// RemoteStatePaused: sandbox is paused; resumable.
	RemoteStatePaused RemoteSandboxState = "paused"

	// RemoteStateTransitioning: sandbox is in a transient lifecycle state
	// (pausing, resuming, provisioning, ...). Treated as "still owned" by
	// SessionBoundManager but not immediately usable.
	RemoteStateTransitioning RemoteSandboxState = "transitioning"

	// RemoteStateTerminal: sandbox is gone. Bindings referencing this state
	// can be replaced.
	RemoteStateTerminal RemoteSandboxState = "terminal"

	// RemoteStateUnknown: adapter could not classify the raw state. Treated
	// as transient (do not replace the binding).
	RemoteStateUnknown RemoteSandboxState = "unknown"
)

// RemoteListFilter narrows a List call. Empty fields mean "no filter".
type RemoteListFilter struct {
	// Metadata: only return sandboxes whose metadata contains all these
	// key/value pairs. Adapters that cannot filter server-side may filter
	// client-side and MUST return the same set.
	Metadata map[string]string

	// States restricts the response to the given normalized states. Empty
	// means "any state".
	States []RemoteSandboxState
}

// RemoteExecRequest describes a single command invocation. See the
// RemoteSandboxClient.Exec contract for how Shell interacts with Args.
type RemoteExecRequest struct {
	// Command is the executable name (Shell=false) or the shell expression
	// (Shell=true).
	Command string

	// Args are argv[1:] when Shell=false; must be empty when Shell=true.
	Args []string

	// Shell selects between direct exec (false) and shell interpretation
	// (true). RemoteSandboxClient implementations must reject requests that
	// combine Shell=true with a non-empty Args.
	Shell bool

	// Stdin is written to the process before it starts reading.
	Stdin string

	// Env is merged into the process environment.
	Env map[string]string

	// WorkDir is the process working directory. Empty means "provider
	// default".
	WorkDir string

	// Timeout bounds a single exec call. Zero means "use provider default".
	Timeout time.Duration
}

// RemoteExecResult is the neutral shape returned by Exec.
type RemoteExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Killed   bool
}

// RemoteDirEntry describes one entry inside a directory listing.
type RemoteDirEntry struct {
	Name string
	Path string
	Type RemoteDirEntryType
	Size int64
}

// RemoteDirEntryType is the coordinator-facing entry kind.
type RemoteDirEntryType string

const (
	RemoteEntryFile RemoteDirEntryType = "file"
	RemoteEntryDir  RemoteDirEntryType = "dir"
	// RemoteEntryOther covers symlinks, sockets, devices, etc. WeKnora
	// artifact code treats these as opaque and skips them.
	RemoteEntryOther RemoteDirEntryType = "other"
)

// RemoteStatEntry is the neutral shape returned by Stat.
type RemoteStatEntry struct {
	Path    string
	Type    RemoteDirEntryType
	Size    int64
	ModTime time.Time
}

// RemoteSandboxCapabilities advertises which optional operations a client
// supports natively. SessionBoundManager reads this to skip provider-specific
// paths (e.g. metadata-based recovery) on backends that do not support them.
//
// Missing capabilities never cause a failure by themselves; the manager
// falls back to less-optimal but always-correct behaviour (e.g. rely on the
// binding store instead of scanning provider metadata).
type RemoteSandboxCapabilities struct {
	// SupportsReconnect is true when Connect can recover an operable handle
	// from a provider-scoped sandbox ID after a WeKnora process restart.
	SupportsReconnect bool

	// SupportsMetadata is true when Create+List preserve the Metadata bag,
	// enabling orphan-sandbox recovery after a WeKnora restart.
	SupportsMetadata bool

	// SupportsListSandboxes is true when List enumerates existing sandboxes
	// (independent of metadata support).
	SupportsListSandboxes bool

	// SupportsPauseResume signals that idle sandboxes can be paused and
	// resumed instead of destroyed. Purely informational for now; the
	// current SessionBoundManager does not itself pause/resume.
	SupportsPauseResume bool

	// SupportsTimeoutRefresh indicates the provider can extend a sandbox's
	// idle timeout after creation. Informational.
	SupportsTimeoutRefresh bool
}

// RemoteSandboxClient is the contract SessionBoundManager talks to. All
// backends (Cube, E2B, ...) must satisfy this interface via a thin adapter.
//
// Concurrency: implementations MUST be safe for concurrent use.
//
// Cancellation: every method must honour ctx.Done. Cancellation returns a
// RemoteError whose Kind is RemoteErrorKindTimeout when the deadline elapsed
// server-side, or the wrapped ctx.Err() otherwise.
type RemoteSandboxClient interface {
	// Provider identifies the backend. Used by binding schema v2 to detect
	// provider mismatches after a mode switch.
	Provider() RemoteProvider

	// Capabilities returns the static capability set of this client. It is
	// safe to call before Health succeeds.
	Capabilities() RemoteSandboxCapabilities

	// Health probes the provider's control plane. Returns nil when reachable.
	Health(ctx context.Context) error

	// --- lifecycle ---

	// Create spawns a new sandbox and returns an opaque handle. The handle
	// is owned by the caller; Delete must eventually be called.
	Create(ctx context.Context, req RemoteCreateRequest) (RemoteSandboxHandle, error)

	// Connect re-attaches to an already-running sandbox by ID. Adapters that
	// cannot support reconnect must return RemoteErrorKindUnsupported here
	// and set SupportsReconnect=false.
	Connect(ctx context.Context, sandboxID string) (RemoteSandboxHandle, error)

	// Get fetches a single sandbox summary by ID. Returns nil summary and
	// RemoteErrorKindNotFound when the sandbox is gone.
	Get(ctx context.Context, sandboxID string) (*RemoteSandboxSummary, error)

	// List enumerates sandboxes visible to this client, optionally filtered.
	List(ctx context.Context, filter RemoteListFilter) ([]RemoteSandboxSummary, error)

	// Delete destroys a sandbox. Deleting a non-existent sandbox returns
	// RemoteErrorKindNotFound; callers typically treat that as success.
	Delete(ctx context.Context, sandboxID string) error

	// --- execution ---

	// Exec runs one command inside the sandbox. See RemoteExecRequest for
	// the Shell/Args contract.
	Exec(ctx context.Context, handle RemoteSandboxHandle, req RemoteExecRequest) (*RemoteExecResult, error)

	// --- filesystem ---

	WriteFile(ctx context.Context, handle RemoteSandboxHandle, path string, content []byte) error
	ReadFile(ctx context.Context, handle RemoteSandboxHandle, path string) ([]byte, error)
	ListDir(ctx context.Context, handle RemoteSandboxHandle, path string) ([]RemoteDirEntry, error)
	MakeDir(ctx context.Context, handle RemoteSandboxHandle, path string) error
	Remove(ctx context.Context, handle RemoteSandboxHandle, path string) error
	Stat(ctx context.Context, handle RemoteSandboxHandle, path string) (*RemoteStatEntry, error)
}
