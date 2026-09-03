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
}

func newRemoteSessionLifecycle(
	client RemoteSandboxClient,
	bindings SessionSandboxBindingStore,
	sessionChecker SessionExistenceChecker,
	createRequest RemoteCreateRequest,
	cleanupTimeout time.Duration,
	sandboxConfigID string,
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
	return &remoteSessionLifecycle{
		client:          client,
		bindings:        bindings,
		sessionChecker:  sessionChecker,
		createRequest:   createRequest,
		cleanupTimeout:  cleanupTimeout,
		sandboxConfigID: sandboxConfigID,
		now:             time.Now,
	}, nil
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

	var handle RemoteSandboxHandle
	err := l.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
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
	binding, err := l.readBinding(ctx, key)
	if err != nil {
		return nil, err
	}

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

	if binding != nil && binding.Provider != l.client.Provider() {
		deleted, err := l.bindings.DeleteIfMatch(
			ctx,
			key,
			binding.Provider,
			binding.SandboxID,
		)
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
	if binding != nil && binding.StaleAt != nil && l.shouldRebuildStaleBinding(ctx, key) {
		if err := l.destroyBindingLocked(ctx, key, *binding); err != nil {
			return nil, fmt.Errorf("release stale sandbox binding: %w", err)
		}
		binding = nil
	}
	// First resolve of a turn spends rebuildOnce even when nothing was stale,
	// so a later install in the same turn cannot tear the sandbox down.
	l.consumeTurnRebuild(ctx, key)

	if binding != nil {
		handle, replace, err := l.connectBinding(ctx, key, *binding)
		if err != nil {
			return nil, err
		}
		if !replace {
			return handle, nil
		}
		deleted, err := l.bindings.DeleteIfMatch(
			ctx,
			key,
			binding.Provider,
			binding.SandboxID,
		)
		if err != nil {
			return nil, fmt.Errorf("delete terminal sandbox binding: %w", err)
		}
		if !deleted {
			return nil, errors.New("terminal sandbox binding changed during replacement")
		}
	}

	recovered, ok, err := l.recoverOwnedSandbox(ctx, key)
	if err != nil {
		return nil, err
	}
	if ok {
		return recovered, nil
	}
	return l.createAndBind(ctx, key)
}

func (l *remoteSessionLifecycle) connectBinding(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) (RemoteSandboxHandle, bool, error) {
	summary, err := l.client.Get(ctx, binding.SandboxID)
	if err != nil {
		if CanReplaceRemoteBinding(err) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("get bound remote sandbox: %w", err)
	}
	if summary == nil {
		return nil, false, errors.New("remote sandbox Get returned nil summary")
	}
	if summary.ID != binding.SandboxID {
		return nil, false, fmt.Errorf(
			"remote sandbox Get returned ID %q for binding %q",
			summary.ID,
			binding.SandboxID,
		)
	}
	if summary.State == RemoteStateTerminal {
		return nil, true, nil
	}

	handle, err := l.client.Connect(ctx, RemoteConnectRequest{
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
			"[sandbox] persist inbound token of session %s failed: %v",
			key.SessionID, err,
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
		if l.missingRequiredInboundToken(handle) {
			// Deleting rather than walking away is safe for the same reason
			// cleanupOwnedDuplicates below is: this loop holds the lifecycle
			// lock for key, and l.metadata(key) scopes every candidate to this
			// session. It is also necessary, because nothing in-tree reclaims
			// orphans (ReapOrphanSandboxes has no production caller) and
			// Connect above has already resumed this sandbox and refreshed its
			// TTL, so leaving it would strand a billable sandbox per restart.
			log.Printf(
				"[sandbox] deleting remote sandbox %s of session %s: "+
					"adopted by metadata without the inbound traffic token "+
					"this config's network policy requires",
				summary.ID, key.SessionID,
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

func (l *remoteSessionLifecycle) createAndBind(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
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

	handle, err := l.client.Create(ctx, request)
	if err != nil {
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
	handle, replace, err := l.connectBinding(ctx, SessionSandboxKey{
		TenantID:  winner.TenantID,
		SessionID: winner.SessionID,
	}, winner)
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
	return SessionSandboxBinding{
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
}

func (l *remoteSessionLifecycle) metadata(key SessionSandboxKey) map[string]string {
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
	if !ok {
		return true
	}
	active, rebuildOnce, err := leaser.TurnState(ctx, key)
	if err != nil {
		log.Printf(
			"[sandbox] turn lease of session %s unavailable (%v); keeping the current sandbox",
			key.SessionID, err,
		)
		return false
	}
	if !active {
		return true
	}
	return rebuildOnce
}

func (l *remoteSessionLifecycle) consumeTurnRebuild(
	ctx context.Context,
	key SessionSandboxKey,
) {
	leaser, ok := l.bindings.(sessionTurnLeaseStore)
	if !ok {
		return
	}
	if err := leaser.ConsumeTurnRebuild(ctx, key); err != nil {
		log.Printf(
			"[sandbox] consume turn rebuild of session %s failed: %v",
			key.SessionID, err,
		)
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
