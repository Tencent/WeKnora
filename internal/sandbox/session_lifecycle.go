package sandbox

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/Tencent/WeKnora/internal/sandbox/admission"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	remoteMetadataTenantID       = "weknora_tenant_id"
	remoteMetadataSessionID      = "weknora_session_id"
	remoteMetadataBindingVersion = "weknora_binding_version"
	remoteMetadataProvider       = "weknora_provider"

	// remoteMetadataConfigID records which sandbox config created the sandbox.
	// Two configs in one workspace may share a provider account, so cleanup
	// must filter by config as well as tenant/session ownership.
	remoteMetadataConfigID = "weknora_sandbox_config_id"

	// User machines are identified by their allocation digest, never by a
	// session. weknora_tenant_id on a user machine is the CONFIG owner, so
	// the config inventory and the credential-change sweep include every
	// borrower's machine; the owner tenant is recorded separately.
	remoteMetadataOwnerTenantID = "weknora_owner_tenant_id"
	remoteMetadataMachineDigest = "weknora_machine_digest"
	remoteMetadataAllocation    = "weknora_allocation"
)

// ErrSandboxSessionDeleted reports that the owning WeKnora session no longer
// exists. Callers must not execute work with the returned nil handle.
var ErrSandboxSessionDeleted = errors.New("sandbox session no longer exists")

// SessionExistenceChecker checks the tenant-scoped durable session record.
type SessionExistenceChecker interface {
	SessionExists(context.Context, SessionSandboxKey) (bool, error)
}

// remoteSessionLifecycle coordinates one provider's persistent sandboxes using
// an authoritative binding store. It contains no provider-native types.
type remoteSessionLifecycle struct {
	client          RemoteSandboxClient
	bindings        SessionSandboxBindingStore
	sessionChecker  SessionExistenceChecker
	createRequest   RemoteCreateRequest
	cleanupTimeout  time.Duration
	sandboxConfigID string
	now             func() time.Time

	// bootstrapper customises the first create of individual sessions
	// (session fork). Optional; nil keeps the default behaviour.
	bootstrapper SessionBootstrapper

	// namespace and admission are set only when the deployment can host
	// shared user machines; user keys are refused without them.
	namespace string
	admission admission.Registry

	// createGate registers every provider Create on the config (C13). Nil
	// means creates are not registered.
	createGate     admission.Registry
	configTenantID uint64
}

type lifecycleOption func(*remoteSessionLifecycle)

// withUserMachines enables shared user machines on this lifecycle.
func withUserMachines(namespace string, reg admission.Registry) lifecycleOption {
	return func(l *remoteSessionLifecycle) {
		l.namespace = namespace
		l.admission = reg
	}
}

// withConfigCreateGate makes every provider Create register with the
// config first (C13). Without a registry creates are not registered.
func withConfigCreateGate(reg admission.Registry, configTenantID uint64) lifecycleOption {
	return func(l *remoteSessionLifecycle) {
		l.createGate, l.configTenantID = reg, configTenantID
	}
}

func newRemoteSessionLifecycle(
	client RemoteSandboxClient,
	bindings SessionSandboxBindingStore,
	sessionChecker SessionExistenceChecker,
	createRequest RemoteCreateRequest,
	cleanupTimeout time.Duration,
	sandboxConfigID string,
	bootstrapper SessionBootstrapper,
	opts ...lifecycleOption,
) (*remoteSessionLifecycle, error) {
	if client == nil {
		return nil, errors.New("remote sandbox client is required")
	}
	if bindings == nil {
		return nil, errors.New("session sandbox binding store is required")
	}
	if sessionChecker == nil {
		return nil, errors.New("session existence checker is required")
	}
	if !isRemoteProvider(client.Provider()) {
		return nil, fmt.Errorf("unsupported remote sandbox provider %q", client.Provider())
	}
	if !client.Capabilities().SupportsReconnect {
		return nil, fmt.Errorf("remote sandbox provider %q does not support reconnect", client.Provider())
	}
	if strings.TrimSpace(createRequest.TemplateID) == "" {
		return nil, errors.New("remote sandbox template ID is required")
	}
	if cleanupTimeout <= 0 {
		return nil, errors.New("remote sandbox cleanup timeout must be positive")
	}
	if strings.TrimSpace(sandboxConfigID) == "" {
		sandboxConfigID = types.SandboxConfigIDGlobalDefault
	}
	createRequest.Metadata = cloneMetadata(createRequest.Metadata)
	createRequest.EnvVars = cloneMetadata(createRequest.EnvVars)
	l := &remoteSessionLifecycle{
		client:          client,
		bindings:        bindings,
		sessionChecker:  sessionChecker,
		createRequest:   createRequest,
		cleanupTimeout:  cleanupTimeout,
		sandboxConfigID: sandboxConfigID,
		now:             time.Now,
		bootstrapper:    bootstrapper,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l, nil
}

func (l *remoteSessionLifecycle) digest(key SessionSandboxKey) string {
	return key.User.Digest(l.namespace)
}

// Resolve returns the current provider's sandbox for key, creating or
// recovering one under the distributed lifecycle lock when necessary.
func (l *remoteSessionLifecycle) Resolve(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if key.IsUserScoped() && l.admission == nil {
		return nil, errors.New("resolve remote sandbox: user machines require an admission registry")
	}

	var handle RemoteSandboxHandle
	err := l.bindings.WithLifecycleLock(ctx, key.Machine(), func(lockCtx context.Context) error {
		resolved, err := l.resolveLocked(lockCtx, key)
		if err != nil {
			return err
		}
		handle = resolved
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("resolve remote sandbox for session: %w", err)
	}
	if handle == nil {
		return nil, errors.New("resolve remote sandbox returned no handle")
	}
	return handle, nil
}

// Destroy removes the bound remote sandbox and then compare-deletes its
// binding. It is idempotent for absent and already-deleted sandboxes.
func (l *remoteSessionLifecycle) Destroy(
	ctx context.Context,
	key SessionSandboxKey,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	err := l.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		binding, err := l.readBinding(lockCtx, key)
		if err != nil || binding == nil {
			return err
		}
		return l.destroyBindingLocked(lockCtx, key, *binding)
	})
	if err != nil {
		return fmt.Errorf("destroy remote sandbox for session: %w", err)
	}
	return nil
}

func (l *remoteSessionLifecycle) resolveLocked(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
	machine := key.Machine()
	binding, err := l.readBinding(ctx, machine)
	if err != nil {
		return nil, err
	}

	// A per-session machine dies with its session. A shared machine never
	// does: the manager refuses a deleted session before calling here, and
	// the machine is reclaimed only by explicit release or the empty-machine
	// GC. Running this check on a user key would let one stale request
	// destroy every session's workspace.
	if !key.IsUserScoped() {
		exists, err := l.sessionChecker.SessionExists(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("check owning session: %w", err)
		}
		if !exists {
			if binding != nil {
				err = l.destroyBindingLocked(ctx, key, *binding)
			}
			return nil, errors.Join(ErrSandboxSessionDeleted, err)
		}
	}

	if binding != nil && binding.Provider != l.client.Provider() {
		deleted, err := l.bindings.DeleteIfMatch(ctx, machine, binding.Provider, binding.SandboxID)
		if err != nil {
			return nil, fmt.Errorf("delete mismatched provider binding: %w", err)
		}
		if !deleted {
			return nil, errors.New("mismatched provider binding changed during replacement")
		}
		binding = nil
	}

	// A stale binding is one whose sandbox boots an image the config has since
	// replaced. Rebuild waits for a turn boundary: the first resolve of a new
	// chat turn may destroy and recreate, later resolves of that same turn
	// keep the sandbox so /workspace scratch and in-flight execs survive an
	// install that landed mid-turn. A resolve with no turn lease (no AgentQA
	// in flight) still rebuilds immediately.
	//
	// Destroying before rebuilding is not optional: the recovery pass below
	// adopts any live sandbox carrying this session's metadata, so a surviving
	// one would simply be picked up again.
	if binding != nil && binding.StaleAt != nil {
		rebuilt, err := l.rebuildStale(ctx, key, *binding)
		if err != nil {
			return nil, err
		}
		if rebuilt {
			binding = nil
		}
	}
	// First resolve of a turn spends rebuildOnce even when nothing was stale,
	// so a later install in the same turn cannot tear the sandbox down.
	l.consumeTurnRebuild(ctx, key)

	if binding != nil {
		handle, replace, err := l.connectBinding(ctx, machine, *binding)
		if err != nil {
			return nil, err
		}
		if !replace {
			return handle, nil
		}
		deleted, err := l.bindings.DeleteIfMatch(ctx, machine, binding.Provider, binding.SandboxID)
		if err != nil {
			return nil, fmt.Errorf("delete terminal sandbox binding: %w", err)
		}
		if !deleted {
			return nil, errors.New("terminal sandbox binding changed during replacement")
		}
	}

	recovered, ok, err := l.recoverOwnedSandbox(ctx, machine)
	if err != nil {
		return nil, err
	}
	if ok {
		return recovered, nil
	}
	return l.createAndBind(ctx, machine)
}

// rebuildStale replaces a sandbox booted from a superseded image. A
// per-session machine rebuilds at its turn boundary. A shared machine also
// needs every operation except this request's own to be gone, and holds the
// machine barrier while it destroys so none can start in between; a closer
// already at work wins. Losing the barrier stops before any further step.
func (l *remoteSessionLifecycle) rebuildStale(
	ctx context.Context, key SessionSandboxKey, binding SessionSandboxBinding,
) (bool, error) {
	if !l.shouldRebuildStaleBinding(ctx, key) {
		return false, nil
	}
	if !key.IsUserScoped() {
		if err := l.destroyBindingLocked(ctx, key, binding); err != nil {
			return false, fmt.Errorf("release stale sandbox binding: %w", err)
		}
		return true, nil
	}
	scope := admission.MachineScope(l.digest(key))
	barrier, err := l.admission.Close(ctx, scope, redisLifecycleLockLease)
	if err != nil {
		if !errors.Is(err, admission.ErrClosed) {
			log.Printf("[sandbox] close %s for rebuild failed (%v); keeping the current sandbox", key, err)
		}
		return false, nil
	}
	defer barrier.Release()
	// C3: a lost barrier also cancels the provider call in flight.
	guarded, stop := admission.Bind(ctx, barrier)
	defer stop()
	others, err := l.admission.ActiveExcept(guarded, scope, admissionExemptFrom(ctx)...)
	if err != nil || others > 0 {
		return false, nil
	}
	if err := barrier.Check(); err != nil {
		return false, err
	}
	if err := l.destroyBindingLocked(guarded, key.Machine(), binding); err != nil {
		if cause := barrier.Check(); cause != nil {
			return false, cause
		}
		return false, fmt.Errorf("release stale sandbox binding: %w", err)
	}
	if err := barrier.Check(); err != nil {
		// The old machine is gone but admission may already be open again:
		// do not create a replacement under a lost barrier.
		return false, err
	}
	return true, nil
}

// withAdmissionExempt records the admission members that belong to the
// current request, so a rebuild it triggers does not wait for itself. The key
// is a types.ContextKey that survives logger.CloneContext: the async QA run
// (internal/handler/session/qa.go setupSSEStream) is built through it, and a
// dropped exemption would make every turn block its own rebuild forever.
func withAdmissionExempt(ctx context.Context, members ...string) context.Context {
	prev := admissionExemptFrom(ctx)
	all := make([]string, 0, len(prev)+len(members))
	all = append(all, prev...)
	all = append(all, members...)
	return context.WithValue(ctx, types.SandboxAdmissionExemptContextKey, all)
}

func admissionExemptFrom(ctx context.Context) []string {
	members, _ := ctx.Value(types.SandboxAdmissionExemptContextKey).([]string)
	return members
}

func (l *remoteSessionLifecycle) connectBinding(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) (RemoteSandboxHandle, bool, error) {
	handle, err := connectRemoteSession(ctx, l.client, RemoteConnectRequest{
		SandboxID:          binding.SandboxID,
		TrafficAccessToken: binding.TrafficAccessToken,
	})
	if err != nil {
		if CanReplaceRemoteBinding(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("connect bound remote sandbox: %w", err)
	}
	if err := l.validateHandle(handle, binding.SandboxID); err != nil {
		return nil, false, err
	}
	persistInboundToken(ctx, l.bindings, key, binding, handle)
	return handle, false, nil
}

// persistInboundToken writes a provider-reissued traffic token back onto the
// binding. Connect may return a fresher credential than Redis has (pause then
// resume); without this a later restart restores the stale copy, data-plane
// calls 403, and CanReplaceRemoteBinding will not swap the binding.
func persistInboundToken(
	ctx context.Context,
	store SessionSandboxBindingStore,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
	handle RemoteSandboxHandle,
) {
	if store == nil {
		return
	}
	token := InboundTokenOf(handle)
	if token == "" || token == binding.TrafficAccessToken {
		return
	}
	if _, err := store.ReplaceTrafficTokenIfMatch(ctx, key, binding, token); err != nil {
		log.Printf(
			"[sandbox] persist inbound token of %s failed: %v",
			key, err,
		)
	}
}

// inboundTokenRequired reports whether the resolved create policy requires
// the per-sandbox traffic token on every data-plane call. Production resolve
// always sets AllowPublicTraffic=false. nil means the policy was never
// resolved (baseline configs and some tests) and predates the token.
func (l *remoteSessionLifecycle) inboundTokenRequired() bool {
	allowPublic := l.createRequest.Network.AllowPublicTraffic
	return allowPublic != nil && !*allowPublic
}

// missingRequiredInboundToken reports a handle that would 403 on every
// data-plane call: the provider issues a traffic token, this config requires
// it, and the handle does not have one. Docker is excluded because its handle
// does not implement RemoteInboundTokenCarrier.
func (l *remoteSessionLifecycle) missingRequiredInboundToken(handle RemoteSandboxHandle) bool {
	_, carriesInboundToken := handle.(RemoteInboundTokenCarrier)
	return carriesInboundToken && l.inboundTokenRequired() && InboundTokenOf(handle) == ""
}

func (l *remoteSessionLifecycle) recoverOwnedSandbox(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, bool, error) {
	return l.recoverOwned(ctx, key, false)
}

// recoverOwned lists key's machine and binds it again. keepUnusable is the
// directory-cleanup path: it returns a user machine even when the inbound
// token is gone, so the cleanup command can run, and does not bind it.
// A normal resolve of a tokenless user machine returns an error instead of
// deleting it or creating another. A tokenless per-session sandbox is still
// deleted, because that machine belongs to the one session.
func (l *remoteSessionLifecycle) recoverOwned(
	ctx context.Context,
	key SessionSandboxKey,
	keepUnusable bool,
) (RemoteSandboxHandle, bool, error) {
	capabilities := l.client.Capabilities()
	if !capabilities.SupportsMetadata || !capabilities.SupportsListSandboxes {
		return nil, false, nil
	}

	metadata := l.metadata(key)
	summaries, err := l.client.List(ctx, RemoteListFilter{Metadata: metadata})
	if err != nil {
		return nil, false, fmt.Errorf("list owned remote sandboxes: %w", err)
	}
	sort.Slice(summaries, func(i, j int) bool {
		left, right := summaries[i], summaries[j]
		if left.StartedAt.Equal(right.StartedAt) {
			return left.ID < right.ID
		}
		if left.StartedAt.IsZero() {
			return false
		}
		if right.StartedAt.IsZero() {
			return true
		}
		return left.StartedAt.Before(right.StartedAt)
	})

	for _, summary := range summaries {
		if summary.ID == "" || summary.State == RemoteStateTerminal {
			continue
		}
		if !metadataMatches(summary.Metadata, metadata) {
			return nil, false, fmt.Errorf(
				"remote provider returned sandbox %q outside metadata filter",
				summary.ID,
			)
		}
		handle, err := l.client.Connect(ctx, RemoteConnectRequest{SandboxID: summary.ID})
		if err != nil {
			if CanReplaceRemoteBinding(err) {
				continue
			}
			return nil, false, fmt.Errorf("connect owned remote sandbox: %w", err)
		}
		if err := l.validateHandle(handle, summary.ID); err != nil {
			return nil, false, err
		}
		// A sandbox recovered by metadata has no binding to supply its inbound
		// token, and both token-carrying providers issue that token only at
		// create time. Adopting one under a closed-inbound policy would wedge
		// the session: every data-plane call answers 403, which httpErrorKind
		// classifies as authentication rather than NotFound, so
		// CanReplaceRemoteBinding never lets the binding be replaced.
		//
		// The test is whether the provider HAS the concept, not whether the
		// token is empty. Docker's handle omits RemoteInboundTokenCarrier
		// because it has no inbound credential and no provider gateway, so an
		// empty token there means "not applicable" rather than "lost" — and
		// destroying a healthy container over it would cost /workspace on
		// every binding loss.
		if l.missingRequiredInboundToken(handle) && key.IsUserMachine() {
			// Deleting a shared machine takes every sibling session's files,
			// and binding the empty token wedges every later call: the
			// provider issued it only at create time, and an authentication
			// error is not replaceable. Directory cleanup still needs the
			// handle for its one command. A normal resolve stops here so it
			// neither deletes this machine nor starts another.
			if keepUnusable {
				return handle, true, nil
			}
			return nil, false, fmt.Errorf(
				"adopt user machine %s: inbound traffic token missing under a closed-inbound policy",
				summary.ID,
			)
		}
		if l.missingRequiredInboundToken(handle) {
			// Deleting rather than walking away is safe for the same reason
			// cleanupOwnedDuplicates below is: this loop holds the lifecycle
			// lock for key, and l.metadata(key) scopes every candidate to this
			// session. It is also necessary, because nothing in-tree reclaims
			// orphans (ReapOrphanSandboxes has no production caller) and
			// Connect above has already resumed this sandbox and refreshed its
			// TTL, so leaving it would strand a billable sandbox per restart.
			log.Printf(
				"[sandbox] deleting remote sandbox %s of %s: "+
					"adopted by metadata without the inbound traffic token "+
					"this config's network policy requires",
				summary.ID, key,
			)
			if err := l.cleanupSandboxID(ctx, summary.ID); err != nil {
				return nil, false, fmt.Errorf(
					"delete unusable owned remote sandbox %q: %w",
					summary.ID,
					err,
				)
			}
			continue
		}
		if err := l.cleanupOwnedDuplicates(ctx, summaries, summary.ID, metadata); err != nil {
			return nil, false, err
		}
		templateID := summary.TemplateID
		if templateID == "" {
			templateID = l.createRequest.TemplateID
		}
		// Reached only when inbound is public or the provider re-issued the
		// token above, so an empty token here is a policy that does not need
		// one rather than a credential we lost.
		binding := l.newBinding(
			key,
			summary.ID,
			templateID,
			InboundTokenOf(handle),
			summary.StartedAt,
		)
		created, err := l.bindings.Create(ctx, key, binding)
		if err != nil {
			return nil, false, fmt.Errorf("bind owned remote sandbox: %w", err)
		}
		if created {
			return handle, true, nil
		}
		winner, err := l.connectWinner(ctx, key)
		if err != nil {
			return nil, false, err
		}
		return winner, true, nil
	}
	return nil, false, nil
}

// recoverWithoutCreating finds key's machine after its binding was lost and
// binds it again. (nil, false, nil) means the provider listed nothing with
// key's identity: the machine is gone. A provider that cannot list by
// metadata cannot prove that, so it returns ErrMachineAbsenceUnprovable.
func (l *remoteSessionLifecycle) recoverWithoutCreating(
	ctx context.Context, key SessionSandboxKey,
) (RemoteSandboxHandle, bool, error) {
	caps := l.client.Capabilities()
	if !caps.SupportsMetadata || !caps.SupportsListSandboxes {
		return nil, false, ErrMachineAbsenceUnprovable
	}
	var handle RemoteSandboxHandle
	var found bool
	err := l.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		if binding, err := l.readBinding(lockCtx, key); err != nil || binding != nil {
			if err != nil {
				return err
			}
			// Bound meanwhile by another request: use that machine.
			// connectKnownWinner takes the binding, not the key.
			h, err := l.connectKnownWinner(lockCtx, *binding)
			handle, found = h, err == nil && h != nil
			return err
		}
		var err error
		// Keep a tokenless machine: deleting it would take every other
		// session's files. The cleanup command is the only call we make.
		handle, found, err = l.recoverOwned(lockCtx, key, true)
		return err
	})
	return handle, found, err
}

// ensureBinding records handle under key when the key is unbound. A cleanup
// that re-adopted a machine without its inbound token calls this only after
// the directory command succeeds, so a failed command does not leave a
// binding that wedges every later call.
func (l *remoteSessionLifecycle) ensureBinding(
	ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	if handle == nil || handle.ID() == "" {
		return errors.New("sandbox: cannot bind an empty recovered handle")
	}
	// A tokenless handle under a closed-inbound policy must stay unbound.
	// The directory delete can still be reported complete; the next resolve
	// refuses to delete the user machine or to create a second one.
	if l.missingRequiredInboundToken(handle) {
		return nil
	}
	return l.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		existing, err := l.readBinding(lockCtx, key)
		if err != nil || existing != nil {
			return err
		}
		binding := l.newBinding(key, handle.ID(), l.createRequest.TemplateID, InboundTokenOf(handle), l.now())
		_, err = l.bindings.Create(lockCtx, key, binding)
		if err != nil {
			return fmt.Errorf("bind recovered remote sandbox: %w", err)
		}
		return nil
	})
}

func (l *remoteSessionLifecycle) createAndBind(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
	if l.createGate != nil {
		token, err := redislock.NewToken()
		if err != nil {
			return nil, err
		}
		lease, err := l.createGate.Hold(ctx, admission.ConfigCreateScope(l.configTenantID, l.sandboxConfigID),
			"create:"+token, admission.DefaultHoldTTL)
		if errors.Is(err, admission.ErrClosed) {
			return nil, fmt.Errorf("%w: %s", ErrSandboxConfigCordoned, l.sandboxConfigID)
		}
		if err != nil {
			return nil, fmt.Errorf("register sandbox create: %w", err)
		}
		defer lease.Release()
		var stop func()
		ctx, stop = admission.Bind(ctx, lease)
		defer stop()
	}

	// request is a VALUE copy of l.createRequest, and every mutation below
	// must stay on the copy. l.createRequest is shared by every session this
	// config serves, so writing the override onto it would make unrelated
	// sessions boot from another session's fork snapshot — a failure that
	// reproduces only under specific interleavings and is brutal to diagnose.
	request := l.createRequest
	request.Metadata = nil
	if l.client.Capabilities().SupportsMetadata {
		request.Metadata = cloneMetadata(l.createRequest.Metadata)
		if request.Metadata == nil {
			request.Metadata = make(map[string]string)
		}
		for metadataKey, value := range l.metadata(key) {
			request.Metadata[metadataKey] = value
		}
	}
	request.EnvVars = cloneMetadata(l.createRequest.EnvVars)
	userMachine := key.IsUserMachine()
	if userMachine {
		// Session paths are per call on a shared machine; a container-level
		// value would point every session at one tree.
		delete(request.EnvVars, skillOutputEnvVar)
		delete(request.EnvVars, sessionInputEnvVar)
	}

	usedOverride := false
	if l.bootstrapper != nil && !userMachine {
		override, err := l.bootstrapper.TemplateOverride(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("resolve session template override: %w", err)
		}
		if strings.TrimSpace(override) != "" {
			request.TemplateID = override
			request.FromSnapshot = true
			usedOverride = true
		}
	}

	handle, err := l.client.Create(ctx, request)
	if err != nil {
		if usedOverride {
			l.notifyCreateFailed(ctx, key, err)
		}
		return nil, fmt.Errorf("create remote sandbox: %w", err)
	}
	if err := l.validateHandle(handle, ""); err != nil {
		return nil, errors.Join(err, l.cleanupCreated(ctx, handle))
	}
	// Same 403 wedge as metadata adoption: the provider issues this token
	// only at create time, and authentication errors are not replaceable.
	// Binding an empty token would leave the session permanently stuck, so
	// fail the create and destroy the unusable sandbox instead.
	if l.missingRequiredInboundToken(handle) {
		return nil, errors.Join(
			fmt.Errorf(
				"create remote sandbox %q: inbound traffic token missing under a closed-inbound policy",
				handle.ID(),
			),
			l.cleanupCreated(ctx, handle),
		)
	}

	if !userMachine {
		exists, checkErr := l.sessionChecker.SessionExists(ctx, key)
		if checkErr != nil {
			return nil, errors.Join(
				fmt.Errorf("recheck owning session: %w", checkErr),
				l.cleanupCreated(ctx, handle),
			)
		}
		if !exists {
			return nil, errors.Join(ErrSandboxSessionDeleted, l.cleanupCreated(ctx, handle))
		}
	}

	binding := l.newBinding(
		key,
		handle.ID(),
		request.TemplateID,
		InboundTokenOf(handle),
		l.now().UTC(),
	)
	created, bindErr := l.bindings.Create(ctx, key, binding)
	if bindErr != nil {
		return nil, errors.Join(
			fmt.Errorf("create sandbox binding: %w", bindErr),
			l.cleanupCreated(ctx, handle),
		)
	}
	if created {
		if !userMachine {
			if err := l.runBootstrap(ctx, key, handle); err != nil {
				return nil, err
			}
		}
		return handle, nil
	}

	winner, winnerErr := l.readBinding(ctx, key)
	if winnerErr != nil {
		// The authoritative winner is unknown, so deleting this sandbox could
		// destroy the resource another coordinator just bound.
		return nil, fmt.Errorf("read winning sandbox binding: %w", winnerErr)
	}
	if winner != nil &&
		winner.Provider == l.client.Provider() &&
		winner.SandboxID == handle.ID() {
		return handle, nil
	}
	if cleanupErr := l.cleanupCreated(ctx, handle); cleanupErr != nil {
		return nil, fmt.Errorf("cleanup losing remote sandbox: %w", cleanupErr)
	}
	if winner == nil {
		return nil, errors.New("sandbox binding create lost without a winner")
	}
	return l.connectKnownWinner(ctx, *winner)
}

// runBootstrap applies the session bootstrapper to a freshly bound sandbox.
//
// Failure is all-or-nothing: the sandbox is destroyed and the binding deleted
// so the next resolve starts from a clean slate. Leaving a half-bootstrapped
// sandbox in place would hand the user an environment that looks right and is
// not.
func (l *remoteSessionLifecycle) runBootstrap(
	ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	if l.bootstrapper == nil {
		return nil
	}
	bootstrapErr := l.bootstrapper.AfterCreate(ctx, key, handle)
	if bootstrapErr == nil {
		return nil
	}
	// Use a cancellation-immune context: the caller may already be gone, and
	// abandoning the rollback would leak both a sandbox and a wrong binding.
	cleanupCtx := context.WithoutCancel(ctx)
	if _, delErr := l.bindings.DeleteIfMatch(
		cleanupCtx, key, l.client.Provider(), handle.ID(),
	); delErr != nil {
		return errors.Join(bootstrapErr, fmt.Errorf("delete binding after failed bootstrap: %w", delErr))
	}
	return errors.Join(bootstrapErr, l.cleanupCreated(cleanupCtx, handle))
}

func (l *remoteSessionLifecycle) notifyCreateFailed(
	ctx context.Context, key SessionSandboxKey, createErr error,
) {
	handler, ok := l.bootstrapper.(SessionCreateFailureHandler)
	if !ok {
		return
	}
	handler.OnCreateFailed(context.WithoutCancel(ctx), key, createErr)
}

func (l *remoteSessionLifecycle) connectWinner(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
	winner, err := l.readBinding(ctx, key)
	if err != nil {
		return nil, err
	}
	if winner == nil {
		return nil, errors.New("sandbox binding create lost without a winner")
	}
	return l.connectKnownWinner(ctx, *winner)
}

func (l *remoteSessionLifecycle) connectKnownWinner(
	ctx context.Context,
	winner SessionSandboxBinding,
) (RemoteSandboxHandle, error) {
	if winner.Provider != l.client.Provider() {
		return nil, fmt.Errorf(
			"sandbox binding winner uses provider %q, current provider is %q",
			winner.Provider,
			l.client.Provider(),
		)
	}
	handle, replace, err := l.connectBinding(ctx, bindingConnectKey(winner), winner)
	if err != nil {
		return nil, err
	}
	if replace {
		return nil, errors.New("sandbox binding winner is already terminal")
	}
	return handle, nil
}

func (l *remoteSessionLifecycle) cleanupOwnedDuplicates(
	ctx context.Context,
	summaries []RemoteSandboxSummary,
	selectedID string,
	metadata map[string]string,
) error {
	for _, candidate := range summaries {
		if candidate.ID == "" ||
			candidate.ID == selectedID ||
			candidate.State == RemoteStateTerminal {
			continue
		}
		if !metadataMatches(candidate.Metadata, metadata) {
			return fmt.Errorf(
				"remote provider returned sandbox %q outside metadata filter",
				candidate.ID,
			)
		}
		if err := l.cleanupSandboxID(ctx, candidate.ID); err != nil {
			return fmt.Errorf(
				"delete duplicate owned remote sandbox %q: %w",
				candidate.ID,
				err,
			)
		}
	}
	return nil
}

func (l *remoteSessionLifecycle) destroyBindingLocked(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) error {
	if binding.Provider == l.client.Provider() {
		err := l.client.Delete(ctx, binding.SandboxID)
		if err != nil && !CanReplaceRemoteBinding(err) {
			return fmt.Errorf("delete remote sandbox: %w", err)
		}
	}

	deleted, err := l.bindings.DeleteIfMatch(
		ctx,
		key,
		binding.Provider,
		binding.SandboxID,
	)
	if err != nil {
		return fmt.Errorf("delete sandbox binding: %w", err)
	}
	if deleted {
		return nil
	}
	current, err := l.readBinding(ctx, key)
	if err != nil {
		return err
	}
	if current == nil {
		return nil
	}
	return errors.New("sandbox binding changed during destroy")
}

// reclaimUserMachine deletes a shared machine. Under the lifecycle lock it
// closes the machine scope, requires that nothing is registered, then runs
// check, which must re-read everything that makes the machine reclaimable:
// a session placed meanwhile either waits on the closed scope and gets a new
// machine, or makes check false. The barrier is checked before each
// destructive step. Busy or no-longer-eligible is (false, nil).
//
// Destroy goes through the binding id and a metadata sweep, not metadata
// adoption. A missing inbound token must not skip that destroy: once check
// says the machine is empty, sibling directories are already gone.
func (l *remoteSessionLifecycle) reclaimUserMachine(
	ctx context.Context, key SessionSandboxKey, check func(context.Context) (bool, error),
) (bool, error) {
	if !key.IsUserMachine() {
		return false, errors.New("reclaim: not a user machine key")
	}
	if l.admission == nil {
		return false, errors.New("reclaim: user machines require an admission registry")
	}
	reclaimed := false
	err := l.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		scope := admission.MachineScope(l.digest(key))
		barrier, err := l.admission.Close(lockCtx, scope, redisLifecycleLockLease)
		if errors.Is(err, admission.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		defer barrier.Release()
		// C3: every provider call below stops if the barrier is lost.
		guarded, stop := admission.Bind(lockCtx, barrier)
		defer stop()
		active, err := l.admission.ActiveExcept(guarded, scope)
		if err != nil || active > 0 {
			return err
		}
		ok, err := check(guarded)
		if err != nil || !ok {
			return err
		}
		binding, err := l.readBinding(guarded, key)
		if err != nil {
			return err
		}
		if binding != nil {
			if err := barrier.Check(); err != nil {
				return err
			}
			if err := l.destroyBindingLocked(guarded, key, *binding); err != nil {
				return errors.Join(err, barrier.Check())
			}
		}
		if err := l.deleteOwnedByMetadata(guarded, key, barrier); err != nil {
			return err
		}
		reclaimed = true
		return nil
	})
	return reclaimed, err
}

// deleteOwnedByMetadata removes every provider sandbox carrying key's
// identity. A binding lost to a Redis flush leaves exactly such a machine.
// guard is checked before each delete.
func (l *remoteSessionLifecycle) deleteOwnedByMetadata(
	ctx context.Context, key SessionSandboxKey, guard admission.Signal,
) error {
	caps := l.client.Capabilities()
	if !caps.SupportsMetadata || !caps.SupportsListSandboxes {
		return nil
	}
	if err := guard.Check(); err != nil {
		return err
	}
	summaries, err := l.client.List(ctx, RemoteListFilter{Metadata: l.metadata(key)})
	if err != nil {
		return errors.Join(fmt.Errorf("list owned remote sandboxes: %w", err), guard.Check())
	}
	for _, s := range summaries {
		if s.ID == "" || s.State == RemoteStateTerminal {
			continue
		}
		if err := guard.Check(); err != nil {
			return err
		}
		// Delete on the barrier-bound context, not the lifecycle-ownership
		// context cleanupSandboxID uses. A lost barrier must cancel this Kill.
		// Create-failure cleanup still goes through cleanupSandboxID.
		if err := l.deleteListedSandbox(ctx, s.ID); err != nil {
			return errors.Join(fmt.Errorf("delete owned remote sandbox %q: %w", s.ID, err), guard.Check())
		}
	}
	return nil
}

// deleteListedSandbox kills one metadata-listed sandbox. ctx is the caller's
// context (the barrier-bound one during reclaim), so a lost barrier cancels
// the provider call. A missing sandbox is success, as in cleanupSandboxID.
func (l *remoteSessionLifecycle) deleteListedSandbox(ctx context.Context, sandboxID string) error {
	cleanupCtx, cancel := context.WithTimeout(ctx, l.cleanupTimeout)
	defer cancel()
	err := l.client.Delete(cleanupCtx, sandboxID)
	if err != nil && !CanReplaceRemoteBinding(err) {
		return err
	}
	return nil
}

func (l *remoteSessionLifecycle) readBinding(
	ctx context.Context,
	key SessionSandboxKey,
) (*SessionSandboxBinding, error) {
	binding, err := l.bindings.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get sandbox binding: %w", err)
	}
	if binding == nil {
		return nil, nil
	}
	if err := binding.Validate(key); err != nil {
		return nil, fmt.Errorf("validate sandbox binding: %w", err)
	}
	return binding, nil
}

func (l *remoteSessionLifecycle) cleanupCreated(
	parent context.Context,
	handle RemoteSandboxHandle,
) error {
	if handle == nil || handle.ID() == "" {
		return errors.New("cannot clean up remote sandbox without an ID")
	}
	return l.cleanupSandboxID(parent, handle.ID())
}

func (l *remoteSessionLifecycle) cleanupSandboxID(
	parent context.Context,
	sandboxID string,
) error {
	cleanupCtx, cancel := context.WithTimeout(
		lifecycleOwnershipContext(parent),
		l.cleanupTimeout,
	)
	defer cancel()
	err := l.client.Delete(cleanupCtx, sandboxID)
	if err != nil && !CanReplaceRemoteBinding(err) {
		return err
	}
	return nil
}

func (l *remoteSessionLifecycle) validateHandle(
	handle RemoteSandboxHandle,
	expectedID string,
) error {
	if handle == nil {
		return errors.New("remote sandbox client returned nil handle")
	}
	if strings.TrimSpace(handle.ID()) == "" {
		return errors.New("remote sandbox client returned handle without ID")
	}
	if expectedID != "" && handle.ID() != expectedID {
		return fmt.Errorf(
			"remote sandbox handle ID %q does not match expected %q",
			handle.ID(),
			expectedID,
		)
	}
	if handle.Provider() != l.client.Provider() {
		return fmt.Errorf(
			"remote sandbox handle provider %q does not match client %q",
			handle.Provider(),
			l.client.Provider(),
		)
	}
	return nil
}

func (l *remoteSessionLifecycle) newBinding(
	key SessionSandboxKey,
	sandboxID string,
	templateID string,
	trafficAccessToken string,
	createdAt time.Time,
) SessionSandboxBinding {
	if createdAt.IsZero() {
		createdAt = l.now().UTC()
	}
	b := SessionSandboxBinding{
		Version:            SessionSandboxBindingVersion,
		Provider:           l.client.Provider(),
		TenantID:           key.TenantID,
		SessionID:          key.SessionID,
		SandboxID:          sandboxID,
		TemplateID:         templateID,
		CreatedAt:          createdAt.UTC(),
		ConfigID:           l.sandboxConfigID,
		TrafficAccessToken: trafficAccessToken,
	}
	if key.IsUserMachine() {
		b.Version = UserSandboxBindingVersion
		b.SessionID = ""
		b.OwnerID = key.User.OwnerID
		b.ConfigTenantID = key.User.ConfigTenantID
		b.ConfigID = key.User.ConfigID
	}
	return b
}

func (l *remoteSessionLifecycle) metadata(key SessionSandboxKey) map[string]string {
	if key.IsUserMachine() {
		return map[string]string{
			remoteMetadataTenantID:       strconv.FormatUint(key.User.ConfigTenantID, 10),
			remoteMetadataOwnerTenantID:  strconv.FormatUint(key.User.OwnerTenantID, 10),
			remoteMetadataMachineDigest:  l.digest(key),
			remoteMetadataAllocation:     MetadataAllocationUser,
			remoteMetadataBindingVersion: strconv.Itoa(UserSandboxBindingVersion),
			remoteMetadataProvider:       string(l.client.Provider()),
			remoteMetadataConfigID:       l.sandboxConfigID,
		}
	}
	return map[string]string{
		remoteMetadataTenantID:       strconv.FormatUint(key.TenantID, 10),
		remoteMetadataSessionID:      key.SessionID,
		remoteMetadataBindingVersion: strconv.Itoa(SessionSandboxBindingVersion),
		remoteMetadataProvider:       string(l.client.Provider()),
		remoteMetadataConfigID:       l.sandboxConfigID,
	}
}

// shouldRebuildStaleBinding reports whether this resolve may tear down a
// stale sandbox. No turn, or the first resolve of a new turn, rebuilds. A
// turn that has already used the sandbox keeps it. A lease we cannot read is
// treated as an in-flight turn so a Redis blip cannot destroy /workspace.
func (l *remoteSessionLifecycle) shouldRebuildStaleBinding(
	ctx context.Context,
	key SessionSandboxKey,
) bool {
	leaser, ok := l.bindings.(sessionTurnLeaseStore)
	active, rebuildOnce := false, false
	if ok && key.SessionID != "" {
		var err error
		active, rebuildOnce, err = leaser.TurnState(ctx, key)
		if err != nil {
			log.Printf("[sandbox] turn lease of %s unavailable (%v); keeping the current sandbox", key, err)
			return false
		}
	}
	if active && !rebuildOnce {
		return false
	}
	if !key.IsUserScoped() {
		return true
	}
	others, err := l.admission.ActiveExcept(ctx, admission.MachineScope(l.digest(key)), admissionExemptFrom(ctx)...)
	if err != nil {
		log.Printf("[sandbox] admission of %s unavailable (%v); keeping the current sandbox", key, err)
		return false
	}
	return others == 0
}

func (l *remoteSessionLifecycle) consumeTurnRebuild(
	ctx context.Context,
	key SessionSandboxKey,
) {
	if key.SessionID == "" {
		return
	}
	leaser, ok := l.bindings.(sessionTurnLeaseStore)
	if !ok {
		return
	}
	if err := leaser.ConsumeTurnRebuild(ctx, key); err != nil {
		log.Printf(
			"[sandbox] consume turn rebuild of %s failed: %v",
			key, err,
		)
	}
}

// stateFilter is the provider listing filter that finds key's machine
// without Connect (lookup-only state peeks).
func (l *remoteSessionLifecycle) stateFilter(key SessionSandboxKey) map[string]string {
	if key.IsUserScoped() {
		return map[string]string{remoteMetadataMachineDigest: l.digest(key.Machine())}
	}
	return map[string]string{
		remoteMetadataTenantID:  strconv.FormatUint(key.TenantID, 10),
		remoteMetadataSessionID: key.SessionID,
	}
}

// bindingConnectKey is the address connectBinding must use for a winner.
// Per-session bindings stay {tenant, session}. A user-machine binding stores
// an empty session id, so the key is rebuilt from the binding's machine
// identity: connectKnownWinner takes the binding, not the key.
func bindingConnectKey(winner SessionSandboxBinding) SessionSandboxKey {
	if winner.Version != UserSandboxBindingVersion {
		return SessionSandboxKey{TenantID: winner.TenantID, SessionID: winner.SessionID}
	}
	return SessionSandboxKey{
		TenantID: winner.TenantID,
		User: allocation.UserMachine{
			OwnerTenantID:  winner.TenantID,
			OwnerID:        winner.OwnerID,
			ConfigTenantID: winner.ConfigTenantID,
			ConfigID:       winner.ConfigID,
		},
	}
}

func metadataMatches(candidate, required map[string]string) bool {
	for key, value := range required {
		if candidate[key] != value {
			return false
		}
	}
	return true
}
