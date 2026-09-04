// Package sandbox: provider-neutral remote sandbox contract.
//
// This file introduces the RemoteSandboxClient interface and the neutral
// data-transfer types that SessionBoundManager depends on. Concrete backends
// (Cube, E2B, Docker) each provide an implementation
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

	"github.com/Tencent/WeKnora/internal/types"
)

// RemoteProvider identifies a remote sandbox backend. Values match the
// stored SandboxType strings so wiring/logging stays uniform.
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

// RemoteConnectRequest re-attaches to an existing sandbox. TrafficAccessToken
// is the inbound credential recovered from the session binding; adapters apply
// it when the provider's connect response omits it, which is the normal case
// because the token is only ever issued at create time.
type RemoteConnectRequest struct {
	SandboxID          string
	TrafficAccessToken string
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

// RemoteVolumeMount describes a volume to mount into the sandbox at creation
// time. Both Cube and E2B support named-volume mounts, making this a
// provider-neutral concept. Each entry references a pre-created volume by
// Name and the sandbox-internal Path at which it should appear.
type RemoteVolumeMount struct {
	// Name identifies the volume. Required.
	Name string

	// Path is the mount point inside the sandbox. Required.
	Path string
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

	// Network is the neutral outbound-network policy for the new sandbox.
	// A zero-value policy asks the adapter to apply the provider's default
	// behaviour (see RemoteNetworkPolicy for the exact semantics).
	Network RemoteNetworkPolicy

	// VolumeMounts specifies volumes to mount into the sandbox at creation
	// time. Optional. When non-empty, the adapter maps each entry to the
	// provider-native volume mount API (e2b.VolumeMount for E2B, etc.).
	VolumeMounts []RemoteVolumeMount
}

// RemoteNetworkPolicy is the provider-neutral outbound-network policy for a
// new sandbox. The fields both Cube and E2B accept are shared; the two L7
// extensions they do not share are carried here as separate slices rather
// than merged, because their shapes have nothing in common. Each adapter
// consumes its own and ignores the other's. Extensions with no admin-facing
// surface yet (egress proxies, masked host names) still stay in the adapters.
//
// Field semantics match the underlying providers:
//
//   - AllowInternetAccess: top-level egress switch. Both adapters materialise
//     nil as true rather than leaving it unset, because the provider default
//     is the template's rather than the protocol's. ResolveEffectiveConfig
//     always fills it, so nil reaches an adapter only from a hand-built
//     Config. When false the sandbox has no default egress; specific hosts
//     must appear in AllowOut.
//   - AllowPublicTraffic: whether the sandbox is reachable from the public
//     internet by URL. ResolveEffectiveConfig always sets this to false
//     (credential required). nil reaches an adapter only from a hand-built
//     Config and is materialised as false there too. true is leftover for
//     tests; production create never sends it.
//   - AllowOut / DenyOut: CIDR blocks or domain names. Cube treats these as
//     L3/L4 filters; E2B applies domain-level filtering only on HTTP(S).
//     Both providers require that specific domain allow-lists be paired
//     with a deny-all; types.ValidateSandboxNetworkPolicy enforces that at
//     save time, so the adapters never see the provider-native error.
type RemoteNetworkPolicy struct {
	AllowInternetAccess *bool
	AllowPublicTraffic  *bool
	AllowOut            []string
	DenyOut             []string
	// CubeRules / E2BHostRules are the provider-specific L7 extensions. Each
	// adapter consumes its own and ignores the other's, so this type stays a
	// superset rather than a lowest common denominator.
	CubeRules    []RemoteCubeEgressRule
	E2BHostRules []RemoteE2BHostRule
}

// DeniesEgressByDefault reports whether outbound traffic falls back to deny,
// leaving only AllowOut (and L7 rule targets) reachable.
//
// It accepts both spellings the drawer and the validator accept: the top-level
// switch turned off, or a deny-all entry in DenyOut. A caller that checks only
// AllowInternetAccess misreads a strict-but-valid config as an open one, which
// is how the deep connectivity check came to report a correct deny-all policy
// as a hard egress failure.
func (p RemoteNetworkPolicy) DeniesEgressByDefault() bool {
	if p.AllowInternetAccess != nil && !*p.AllowInternetAccess {
		return true
	}
	return types.DenyOutCoversAllIPv4(p.DenyOut)
}

// RemoteInboundTokenCarrier is implemented by handles whose provider issues
// a per-sandbox inbound traffic token. Cube and E2B always do; Docker does
// not. It is an optional capability in the same spirit as RemoteSnapshotManager.
type RemoteInboundTokenCarrier interface {
	TrafficAccessToken() string
}

// InboundTokenOf returns the inbound credential a handle carries, or "" when
// the provider has none. Callers persist it so a later reconnect can restore
// it.
func InboundTokenOf(handle RemoteSandboxHandle) string {
	carrier, ok := handle.(RemoteInboundTokenCarrier)
	if !ok {
		return ""
	}
	return carrier.TrafficAccessToken()
}

// RemoteCubeEgressRule is one CubeEgress L7 rule in neutral form. Allow is
// phrased positively here even though the stored config says Deny: adapters
// map onto provider payloads whose field is also an allow flag, and having
// the negation happen exactly once (in ResolveEffectiveConfig) is what keeps
// a double negative from creeping in.
type RemoteCubeEgressRule struct {
	Name    string
	Scheme  string
	SNI     string
	Host    string
	Methods []string
	Path    string
	Allow   bool
	Audit   string
	Inject  []RemoteHeaderInject
}

// RemoteHeaderInject is one credential header injected by the egress proxy.
// Format defaults to "${SECRET}" provider-side when empty.
type RemoteHeaderInject struct {
	Header string
	Secret string
	Format string
}

// RemoteE2BHostRule is one E2B per-host request transform.
type RemoteE2BHostRule struct {
	Host    string
	Headers map[string]string
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

	// User is the OS user the process runs as. Empty means "provider
	// default". Both backends support selecting it (E2B WithUser, Cube
	// CommandOptions.User).
	//
	// Callers that rely on filesystem permissions for isolation MUST set a
	// non-root user: root bypasses mode bits entirely, which would defeat
	// read-only protection on shared volumes.
	User string

	// Timeout bounds a single exec call. Zero means "use provider default".
	Timeout time.Duration
}

// DefaultSandboxExecUser is the non-root account WeKnora runs sandboxed
// scripts as. The sandbox template must provision this user; E2B base
// templates ship a "user" account, and Cube templates are expected to match.
const DefaultSandboxExecUser = "user"

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
	Name    string
	Path    string
	Type    RemoteDirEntryType
	Size    int64
	ModTime time.Time
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
// Missing metadata/list capabilities use less-optimal but correct behaviour
// (rely on the binding store instead of scanning provider metadata).
// SupportsReconnect is required for persistent session lifecycle management.
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

	// SupportsFilesystemEnumeration is true when the provider implements
	// ListDir, MakeDir, Stat, and Remove. SessionBoundManager only
	// advertises the SessionFileStore capability when this is true — the
	// application layer then knows to deregister list/read/attachment
	// staging tools that would otherwise fail at request time.
	SupportsFilesystemEnumeration bool

	// SupportsSnapshots reports whether the provider can snapshot a running
	// sandbox into a reusable image. Snapshot IDs double as template IDs on
	// Cube, E2B, and Docker, which is what makes skill images work.
	SupportsSnapshots bool

	// SupportsVolumes is true when the provider can mount a named volume into
	// a sandbox at creation time (RemoteCreateRequest.VolumeMounts). Callers
	// use this to tell an operator up front that a backend cannot serve
	// volume-based features, instead of failing later at first use.
	SupportsVolumes bool
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
	// Provider identifies the backend. Used by the binding schema to detect
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

	// Connect re-attaches to an already-running sandbox. Adapters that
	// cannot support reconnect must return RemoteErrorKindUnsupported here
	// and set SupportsReconnect=false.
	Connect(ctx context.Context, req RemoteConnectRequest) (RemoteSandboxHandle, error)

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
	// MakeDir creates path, including parents. A directory that already
	// exists is success: envd's MakeDir is not mkdir -p, and adapters must
	// hide that so writing a second file into the same folder (or seeding
	// SKILL.md after resetSkillDir) does not fail.
	MakeDir(ctx context.Context, handle RemoteSandboxHandle, path string) error
	Remove(ctx context.Context, handle RemoteSandboxHandle, path string) error
	Stat(ctx context.Context, handle RemoteSandboxHandle, path string) (*RemoteStatEntry, error)
}

// cloneMetadata returns a shallow copy of source. Nil input returns nil so
// callers can distinguish "explicitly empty" from "not set".
func cloneMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// RemoteSnapshotRef identifies one provider-side snapshot. ID can be passed
// straight back as RemoteCreateRequest.TemplateID: Cube and E2B store
// snapshots as templates, and Docker stores them as local image tags.
type RemoteSnapshotRef struct {
	ID    string
	Names []string
}

// RemoteSnapshotManager is an optional capability used only by the skill
// install/remove paths. Session execution never touches it.
type RemoteSnapshotManager interface {
	// CreateSnapshot snapshots a running sandbox. An empty name lets the
	// provider generate one. The provider pauses the sandbox while the
	// snapshot is taken.
	CreateSnapshot(ctx context.Context, sandboxID string, name string) (RemoteSnapshotRef, error)

	// DeleteSnapshot removes a snapshot. A missing snapshot is NOT an error:
	// both SDKs treat delete as idempotent and so must every adapter.
	DeleteSnapshot(ctx context.Context, snapshotID string) error

	// ListSnapshots lists snapshots. An empty sandboxID lists all of them.
	ListSnapshots(ctx context.Context, sandboxID string) ([]RemoteSnapshotRef, error)
}

// SnapshotManagerFrom narrows a client to its snapshot capability. It returns
// false for providers that cannot snapshot, so callers can fall back to the
// base template instead of failing.
//
// Both signals must agree: the type assertion finds the methods, and
// SupportsSnapshots is the advertised capability. A wrapper that happens to
// embed snapshot methods must not be treated as snapshot-capable when the
// flag is off.
func SnapshotManagerFrom(client RemoteSandboxClient) (RemoteSnapshotManager, bool) {
	if client == nil {
		return nil, false
	}
	mgr, ok := client.(RemoteSnapshotManager)
	if !ok {
		return nil, false
	}
	if !client.Capabilities().SupportsSnapshots {
		return nil, false
	}
	return mgr, true
}
