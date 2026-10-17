// Package hitl coordinates human-in-the-loop prompts raised by agent runs:
// tool approvals, in-conversation authorizations, questions for the user.
//
// The prompt row in Store is the source of truth for every answer. The
// replica that raised a prompt keeps an in-memory waiter only as a wake-up
// hook: Resolve records the answer with a compare-and-set, then wakes the
// waiter locally or over Redis pub/sub, and the waiter also polls the row in
// case a wake-up is lost. Kind-specific behaviour (request/response shape and
// validation) is registered per kind; the broker never interprets payloads.
package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// pubsubChannelBase is the Redis channel prefix used to wake the replica that
// holds a waiter after another replica recorded the answer. The actual
// channel is suffixed with WEKNORA_REDIS_NAMESPACE (when set) so multiple
// deployments sharing the same Redis don't cross-talk.
const pubsubChannelBase = "weknora:agent_prompt:wake"

// instanceID identifies this process for pub/sub wake-up suppression.
var instanceID = uuid.New().String()

// Timing of the owner-side wait loop. The poll covers lost pub/sub wake-ups;
// the lease lets other replicas notice an owner that disappeared.
const (
	defaultPollInterval  = 3 * time.Second
	defaultLeaseTTL      = 30 * time.Second
	defaultRenewInterval = 10 * time.Second
)

// consumeRetryDelays spaces the retries of a Consume the store failed to record.
var consumeRetryDelays = []time.Duration{100 * time.Millisecond, 400 * time.Millisecond}

var (
	// ErrPromptNotFound is returned when Resolve targets an unknown prompt,
	// or one outside the caller's session or kind.
	ErrPromptNotFound = errors.New("agent prompt not found")
	// ErrTenantMismatch is returned when the caller's tenant does not own the prompt.
	ErrTenantMismatch = errors.New("workspace mismatch for agent prompt")
	// ErrUserMismatch is returned when the caller does not own the session.
	ErrUserMismatch = errors.New("user mismatch for agent prompt")
	// ErrAlreadyResolved is returned when the prompt was already answered or
	// ended by timeout / cancellation.
	ErrAlreadyResolved = errors.New("agent prompt already resolved")
	// ErrInvalidResponse wraps a kind validator's rejection of an answer.
	ErrInvalidResponse = errors.New("invalid agent prompt response")
)

// ResponseValidator checks an answer against the prompt that asked for it.
type ResponseValidator func(request, response json.RawMessage) error

// Outcome is how an awaited prompt ended. Prompt is the row as last read.
type Outcome struct {
	Prompt *types.AgentPrompt
	// Answered means the user responded; Prompt.Response holds the answer.
	Answered bool
	TimedOut bool
	Canceled bool
	// Suspended means the prompt outlived the in-memory wait: the run must
	// end now and be resumed once the prompt is answered.
	Suspended bool
	// Detached means another owner already took over this run. The old
	// waiter must exit without publishing or persisting completion.
	Detached  bool
	ExpiresAt *time.Time
	Reason    string
}

// Spec describes a prompt to raise and wait on.
type Spec struct {
	// Prompt carries kind, scope and request. Status, ownership and lease are
	// filled in by Await; an empty ID is generated.
	Prompt        *types.AgentPrompt
	Timeout       time.Duration
	TimeoutReason string
	// Emit notifies the UI. It runs after the row is persisted and the
	// waiter is registered, so an immediate answer is never missed.
	Emit func(ctx context.Context, prompt *types.AgentPrompt) error
	// Suspend makes the prompt suspendable: when Timeout elapses the run is
	// suspended instead of the prompt expiring. Nil keeps a plain wait.
	Suspend *SuspendSpec
	// FallbackTimeout, when set, turns a suspendable prompt whose run state
	// cannot be saved into a plain wait of this length instead of failing.
	// Emit sees the downgrade as Prompt.Suspendable == false.
	FallbackTimeout time.Duration
}

// SuspendSpec carries the run state saved with a suspendable prompt. It is
// written before the prompt is raised, so a replica that dies while waiting
// still leaves a resumable run behind.
type SuspendSpec struct {
	Snapshot        []byte
	SnapshotVersion int
}

// ResolveRequest is a user answer to a prompt. SessionID and Kind, when set,
// must match the prompt; a mismatch reads as not found so callers cannot
// probe prompts outside the session or surface they are authorized for.
type ResolveRequest struct {
	TenantID  uint64
	UserID    string
	SessionID string
	Kind      string
	PromptID  string
	Response  json.RawMessage
}

// Broker raises, awaits and resolves prompts.
type Broker struct {
	store       Store
	suspensions SuspensionStore
	rdb         *redis.Client

	mu         sync.Mutex
	waiters    map[string]chan struct{}
	validators map[string]ResponseValidator

	pollInterval  time.Duration
	leaseTTL      time.Duration
	renewInterval time.Duration
	// suspendExpiry bounds how long a suspended prompt stays answerable;
	// zero never expires.
	suspendExpiry time.Duration
	// runHooks finish the message of a run Resolve takes over from an owner
	// that stopped renewing its lease; see SetRunHooks.
	runHooks RunHooks
}

// SetRunHooks installs the hooks Resolve uses when it suspends a prompt on
// behalf of an owner that disappeared, as the reaper would.
func (b *Broker) SetRunHooks(h RunHooks) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runHooks = h
}

func (b *Broker) hooks() RunHooks {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runHooks
}

// Option customizes a Broker.
type Option func(*Broker)

// WithPollInterval overrides how often a waiter re-reads its row.
func WithPollInterval(d time.Duration) Option {
	return func(b *Broker) {
		if d > 0 {
			b.pollInterval = d
		}
	}
}

// WithSuspensionStore sets where suspendable prompts save their run state.
func WithSuspensionStore(s SuspensionStore) Option {
	return func(b *Broker) {
		if s != nil {
			b.suspensions = s
		}
	}
}

// WithSuspendExpiry sets how long a suspended prompt stays answerable; zero
// never expires.
func WithSuspendExpiry(d time.Duration) Option {
	return func(b *Broker) { b.suspendExpiry = d }
}

// withLease shortens lease timing for tests.
func withLease(ttl, renew time.Duration) Option {
	return func(b *Broker) { b.leaseTTL, b.renewInterval = ttl, renew }
}

// NewBroker builds a broker. store may be nil (process memory; single
// replica only). rdb may be nil (no cross-instance wake-ups; waiters still
// observe answers by polling the store).
func NewBroker(store Store, rdb *redis.Client, opts ...Option) *Broker {
	if store == nil {
		store = NewMemoryStore()
	}
	b := &Broker{
		store:         store,
		suspensions:   NewMemorySuspensionStore(),
		rdb:           rdb,
		waiters:       make(map[string]chan struct{}),
		validators:    make(map[string]ResponseValidator),
		pollInterval:  defaultPollInterval,
		leaseTTL:      defaultLeaseTTL,
		renewInterval: defaultRenewInterval,
		suspendExpiry: config.DefaultPromptExpire,
	}
	for _, opt := range opts {
		opt(b)
	}
	if rdb != nil {
		go b.runSubscriber()
	}
	return b
}

// Store exposes the backing store to kind adapters and tests.
func (b *Broker) Store() Store { return b.store }

// Suspensions exposes the run-state store.
func (b *Broker) Suspensions() SuspensionStore { return b.suspensions }

// SessionPrompts lists the session's prompts in the given statuses, oldest
// first.
func (b *Broker) SessionPrompts(
	ctx context.Context, sessionID string, statuses []string,
) ([]*types.AgentPrompt, error) {
	return b.store.ListBySession(ctx, sessionID, statuses)
}

// RegisterKind installs the answer validator for kind. Resolve rejects
// answers to kinds without a validator.
func (b *Broker) RegisterKind(kind string, validate ResponseValidator) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.validators[kind] = validate
}

// Await persists the prompt as pending and owned by this process, emits it,
// and blocks until it is answered, the timeout elapses, or ctx ends. The
// outcome always reflects the row's final state, so an answer that lands
// concurrently with a timeout or cancellation is never lost.
//
// A suspendable prompt's run state is kept only while the run may still be
// resumed from it: it is dropped when the prompt ends any other way.
func (b *Broker) Await(ctx context.Context, spec Spec) (out Outcome, err error) {
	p := spec.Prompt
	if p == nil {
		return Outcome{}, errors.New("agent prompt: nil prompt")
	}
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	delegation, delegated := ctx.Value(delegationKey{}).(Delegation)
	if delegated {
		if err := CheckDelegation(ctx); err != nil {
			return Outcome{}, err
		}
		if spec.Suspend == nil {
			return Outcome{}, errors.New("delegated prompts require a durable snapshot")
		}
		// Persist or fail closed; a worker must never silently become a hot waiter.
		spec.FallbackTimeout = 0
		spec.Timeout = 0
	}
	dbCtx := context.WithoutCancel(ctx)
	// A fresh token also fences a takeover by another run on this same replica.
	owner := uuid.NewString()
	if spec.Suspend != nil {
		if p.AssistantMessageID == "" {
			return Outcome{}, errors.New("agent prompt: a suspendable prompt needs an assistant message")
		}
		p.Suspendable = true
		err := b.suspensions.Save(dbCtx, &types.AgentRunSuspension{
			AssistantMessageID: p.AssistantMessageID,
			TenantID:           p.TenantID,
			SessionID:          p.SessionID,
			PromptID:           p.ID,
			SnapshotVersion:    spec.Suspend.SnapshotVersion,
			Snapshot:           types.JSON(spec.Suspend.Snapshot),
		})
		switch {
		case err == nil:
			defer func() {
				if !out.Suspended {
					row, readErr := b.store.Get(dbCtx, p.ID)
					if readErr == nil && (row == nil || row.OwnerInstance == owner) {
						b.dropSnapshot(dbCtx, p.AssistantMessageID, p.ID)
					}
				}
			}()
		case spec.FallbackTimeout > 0:
			logger.GetLogger(ctx).Warnf("persist run state of prompt %s, waiting in memory instead: %v", p.ID, err)
			spec.Suspend, spec.Timeout, p.Suspendable = nil, spec.FallbackTimeout, false
		default:
			return Outcome{}, fmt.Errorf("persist run state: %w", err)
		}
	}
	if delegated && delegation.Register != nil {
		if err := delegation.Register(ctx, p); err != nil {
			return Outcome{}, err
		}
	}
	lease := time.Now().Add(b.leaseTTL)
	p.Status = types.AgentPromptPending
	p.OwnerInstance = owner
	p.LeaseUntil = &lease
	if err := b.store.Create(dbCtx, p); err != nil {
		return Outcome{}, fmt.Errorf("persist agent prompt: %w", err)
	}

	wake := b.register(p.ID)
	defer b.unregister(p.ID)

	if spec.Emit != nil {
		if err := spec.Emit(ctx, p); err != nil {
			b.closeAsOwner(dbCtx, p.ID, owner, types.AgentPromptCanceled, "prompt could not be delivered")
			return Outcome{}, fmt.Errorf("emit agent prompt: %w", err)
		}
	}

	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()
	poll := time.NewTicker(b.pollInterval)
	defer poll.Stop()
	renew := time.NewTicker(b.renewInterval)
	defer renew.Stop()

	for {
		select {
		case <-wake:
			if out, ok := b.closed(dbCtx, p.ID, owner); ok {
				return out, nil
			}
		case <-poll.C:
			if out, ok := b.closed(dbCtx, p.ID, owner); ok {
				return out, nil
			}
		case <-renew.C:
			if _, err := b.store.RenewLease(dbCtx, p.ID, owner, time.Now().Add(b.leaseTTL)); err != nil {
				logger.GetLogger(ctx).Warnf("renew agent prompt lease %s: %v", p.ID, err)
			}
		case <-timer.C:
			if spec.Suspend != nil {
				return b.suspend(dbCtx, p.ID, owner), nil
			}
			return b.closeAsOwner(dbCtx, p.ID, owner, types.AgentPromptExpired, spec.TimeoutReason), nil
		case <-ctx.Done():
			return b.closeAsOwner(dbCtx, p.ID, owner, types.AgentPromptCanceled, "request canceled"), nil
		}
	}
}

// suspend moves a pending prompt to suspended. When an answer won the race,
// that answer is returned instead.
func (b *Broker) suspend(ctx context.Context, id, owner string) Outcome {
	expiresAt := b.expiresAt(time.Now())
	ok, err := b.store.Transition(ctx, id, types.AgentPromptTransition{
		From:          []string{types.AgentPromptPending},
		To:            types.AgentPromptSuspended,
		ExpiresAt:     expiresAt,
		ExpectedOwner: &owner,
	})
	if err != nil {
		logger.GetLogger(ctx).Warnf("suspend agent prompt %s: %v", id, err)
	}
	if !ok {
		if out, closed := b.closed(ctx, id, owner); closed {
			return out
		}
		// The row could not be read or moved; fail the prompt rather than
		// leave a run suspended on state nobody can confirm.
		return b.closeAsOwner(ctx, id, owner, types.AgentPromptExpired, "prompt could not be suspended")
	}
	return Outcome{
		Prompt:    &types.AgentPrompt{ID: id, Status: types.AgentPromptSuspended, ExpiresAt: expiresAt},
		Suspended: true,
		ExpiresAt: expiresAt,
	}
}

func (b *Broker) expiresAt(now time.Time) *time.Time {
	if b.suspendExpiry <= 0 {
		return nil
	}
	at := now.Add(b.suspendExpiry)
	return &at
}

func (b *Broker) dropSnapshot(ctx context.Context, messageID, promptID string) {
	if err := b.suspensions.Delete(ctx, messageID, promptID); err != nil {
		logger.GetLogger(ctx).Warnf("drop run state of message %s: %v", messageID, err)
	}
}

// closeAsOwner ends a pending prompt from the waiting side. When an answer
// won the race, that answer is returned instead.
func (b *Broker) closeAsOwner(ctx context.Context, id, owner, status, reason string) Outcome {
	ok, err := b.store.Transition(ctx, id, types.AgentPromptTransition{
		From:          []string{types.AgentPromptPending},
		To:            status,
		CloseReason:   &reason,
		ExpectedOwner: &owner,
	})
	if err != nil {
		logger.GetLogger(ctx).Warnf("close agent prompt %s as %s: %v", id, status, err)
	}
	if !ok {
		if out, closed := b.closed(ctx, id, owner); closed {
			return out
		}
	}
	return Outcome{
		Prompt:   &types.AgentPrompt{ID: id, Status: status, CloseReason: reason},
		TimedOut: status == types.AgentPromptExpired,
		Canceled: status == types.AgentPromptCanceled,
		Reason:   reason,
	}
}

// closed reads the row and reports its outcome once the owner should stop
// waiting: the prompt ended, or another replica suspended it after this
// owner's lease lapsed.
func (b *Broker) closed(ctx context.Context, id, owner string) (Outcome, bool) {
	row, err := b.store.Get(ctx, id)
	if err != nil {
		logger.GetLogger(ctx).Warnf("read agent prompt %s: %v", id, err)
		return Outcome{}, false
	}
	if row == nil || row.Status == types.AgentPromptPending {
		return Outcome{}, false
	}
	if row.OwnerInstance != owner {
		return Outcome{Prompt: row, Suspended: true, Detached: true, ExpiresAt: row.ExpiresAt}, true
	}
	if row.Status == types.AgentPromptSuspended {
		return Outcome{Prompt: row, Suspended: true, ExpiresAt: row.ExpiresAt}, true
	}
	return Outcome{
		Prompt:   row,
		Answered: row.Status == types.AgentPromptAnswered,
		TimedOut: row.Status == types.AgentPromptExpired,
		Canceled: row.Status == types.AgentPromptCanceled,
		Reason:   row.CloseReason,
	}, true
}

// Consume claims an answered prompt before its owner acts on it:
// AgentPromptExecuting when the answer authorized a side effect still to run,
// AgentPromptDone otherwise. Only the winner may continue the run.
// Prompts closed by the system are left alone.
//
// A storage error is distinct from a lost claim: callers must fail the tool
// without detaching the run, so ordinary completion can persist its message.
// Neither outcome permits executing the authorized side effect.
func (b *Broker) Consume(ctx context.Context, id, to, owner string) (bool, error) {
	if err := CheckDelegation(ctx); err != nil {
		return false, err
	}
	ctx = context.WithoutCancel(ctx)
	transition := types.AgentPromptTransition{
		From:          []string{types.AgentPromptAnswered},
		To:            to,
		ExpectedOwner: &owner,
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		ok, err := b.store.Transition(ctx, id, transition)
		if err == nil && (ok || attempt == 0) {
			return ok, nil
		}
		if err == nil {
			// An earlier attempt may have committed before failing.
			break
		}
		lastErr = err
		if attempt == len(consumeRetryDelays) {
			break
		}
		time.Sleep(consumeRetryDelays[attempt])
	}
	row, readErr := b.store.Get(ctx, id)
	if readErr == nil {
		if row == nil || row.OwnerInstance != owner {
			return false, nil
		}
		if row.Status == to {
			return true, nil
		}
		if row.Status != types.AgentPromptAnswered {
			return false, nil
		}
	}
	// We could not confirm a durable claim. Close only this owner's prompt;
	// a concurrent takeover must not be canceled by the stale caller.
	reason := "could not persist prompt consumption; tool was not executed"
	_, closeErr := b.store.Transition(ctx, id, types.AgentPromptTransition{
		From:          []string{types.AgentPromptAnswered, types.AgentPromptExecuting},
		To:            types.AgentPromptFailed,
		ExpectedOwner: &owner,
		CloseReason:   &reason,
	})
	err := fmt.Errorf("consume agent prompt %s: %w", id, errors.Join(lastErr, readErr, closeErr))
	logger.GetLogger(ctx).Warnf("%v", err)
	return false, err
}

// MarkExecuted records that the side effect an answer authorized has run.
func (b *Broker) MarkExecuted(ctx context.Context, id string) {
	if id == "" {
		return
	}
	_, err := b.store.Transition(context.WithoutCancel(ctx), id, types.AgentPromptTransition{
		From: []string{types.AgentPromptExecuting},
		To:   types.AgentPromptDone,
	})
	if err != nil {
		logger.GetLogger(ctx).Warnf("mark agent prompt %s executed: %v", id, err)
	}
}

// Resolution is the result of a recorded answer.
type Resolution struct {
	// Prompt is the answered row.
	Prompt *types.AgentPrompt
	// Resume means the prompt was suspended: its run has ended and the
	// caller must resume it. This replica now holds the prompt's lease; see
	// KeepLease.
	Resume bool
	// OwnerGone means the run was suspended on behalf of an owner that
	// stopped renewing its lease, so nothing will tear down what that owner
	// left behind (such as its live-run marker). Otherwise the owner suspended
	// the run itself and may still be finishing it.
	OwnerGone bool
	// OwnerLeaseUntil bounds how long the previous waiter may still finish.
	OwnerLeaseUntil *time.Time
}

// Resolve records a user answer on an open prompt. A pending prompt's waiter
// is woken; a suspended prompt is handed to the caller to resume. The tenant
// must match the prompt; when the prompt was registered with a user, the
// caller must present that same non-empty user.
func (b *Broker) Resolve(ctx context.Context, req ResolveRequest) (Resolution, error) {
	row, err := b.store.Get(ctx, req.PromptID)
	if err != nil {
		return Resolution{}, err
	}
	if row == nil ||
		(req.Kind != "" && row.Kind != req.Kind) ||
		(req.SessionID != "" && row.SessionID != req.SessionID) {
		return Resolution{}, ErrPromptNotFound
	}
	if row.TenantID != req.TenantID {
		return Resolution{}, ErrTenantMismatch
	}
	// An empty caller userID is a mismatch (fail-close) so a missing auth
	// middleware cannot bypass the per-user check.
	if row.UserID != "" && row.UserID != req.UserID {
		return Resolution{}, ErrUserMismatch
	}
	if !row.IsOpen() {
		return Resolution{}, ErrAlreadyResolved
	}

	b.mu.Lock()
	validate := b.validators[row.Kind]
	b.mu.Unlock()
	if validate == nil {
		return Resolution{}, fmt.Errorf("%w: unsupported prompt kind %q", ErrInvalidResponse, row.Kind)
	}
	if err := validate(json.RawMessage(row.Request), req.Response); err != nil {
		return Resolution{}, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}

	now := time.Now()
	// A pending prompt whose owner stopped renewing has no waiter left to
	// wake. Suspend it as the reaper would, so the answer resumes the run
	// instead of being recorded for nobody.
	if row.Status == types.AgentPromptPending && row.Suspendable &&
		row.LeaseUntil != nil && row.LeaseUntil.Before(now) {
		b.reapPending(ctx, now, row, b.hooks())
		if row, err = b.store.Get(ctx, req.PromptID); err != nil {
			return Resolution{}, err
		}
		if row == nil {
			return Resolution{}, ErrPromptNotFound
		}
		if !row.IsOpen() {
			return Resolution{}, ErrAlreadyResolved
		}
	}
	ownerLeaseUntil := row.LeaseUntil
	ownerGone := row.OwnerInstance == "" ||
		(row.Status == types.AgentPromptSuspended && ownerLeaseUntil != nil && ownerLeaseUntil.Before(now))
	resolvedBy := req.UserID
	answer := func(t types.AgentPromptTransition) (bool, error) {
		t.To = types.AgentPromptAnswered
		t.Response = types.JSON(req.Response)
		t.ResolvedBy = &resolvedBy
		t.ResolvedAt = &now
		return b.store.Transition(ctx, req.PromptID, t)
	}

	owner, lease := uuid.NewString(), now.Add(b.leaseTTL)
	ok, err := answer(types.AgentPromptTransition{
		From:        []string{types.AgentPromptSuspended},
		UnexpiredAt: &now,
		Owner:       &owner,
		LeaseUntil:  &lease,
	})
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		row.Status, row.Response = types.AgentPromptAnswered, types.JSON(req.Response)
		row.ResolvedBy, row.ResolvedAt = resolvedBy, &now
		row.OwnerInstance, row.LeaseUntil = owner, &lease
		return Resolution{Prompt: row, Resume: true, OwnerGone: ownerGone, OwnerLeaseUntil: ownerLeaseUntil}, nil
	}

	ok, err = answer(types.AgentPromptTransition{From: []string{types.AgentPromptPending}})
	if err != nil {
		return Resolution{}, err
	}
	if !ok {
		return Resolution{}, ErrAlreadyResolved
	}
	b.wake(ctx, req.PromptID)
	row.Status, row.Response = types.AgentPromptAnswered, types.JSON(req.Response)
	row.ResolvedBy, row.ResolvedAt = resolvedBy, &now
	return Resolution{Prompt: row}, nil
}

// KeepLease renews this replica's lease on a prompt it resumes until stop is
// called or the prompt leaves the leased states. Without it the reaper
// treats the resumed run as abandoned.
func (b *Broker) KeepLease(ctx context.Context, id, owner string) (stop func()) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		ticker := time.NewTicker(b.renewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ok, err := b.store.RenewLease(ctx, id, owner, time.Now().Add(b.leaseTTL))
				if err != nil {
					logger.GetLogger(ctx).Warnf("renew agent prompt lease %s: %v", id, err)
					continue
				}
				if !ok {
					return
				}
			}
		}
	}()
	return cancel
}

// Fail closes a prompt the caller holds (answered or executing) whose run
// could not go on.
func (b *Broker) Fail(ctx context.Context, id, reason string) {
	_, err := b.store.Transition(context.WithoutCancel(ctx), id, types.AgentPromptTransition{
		From:        []string{types.AgentPromptAnswered, types.AgentPromptExecuting},
		To:          types.AgentPromptFailed,
		CloseReason: &reason,
	})
	if err != nil {
		logger.GetLogger(ctx).Warnf("fail agent prompt %s: %v", id, err)
	}
}

func (b *Broker) register(id string) <-chan struct{} {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	b.waiters[id] = ch
	b.mu.Unlock()
	return ch
}

func (b *Broker) unregister(id string) {
	b.mu.Lock()
	delete(b.waiters, id)
	b.mu.Unlock()
}

// wakeLocal nudges a waiter on this process; it reports whether one exists.
func (b *Broker) wakeLocal(id string) bool {
	b.mu.Lock()
	ch, ok := b.waiters[id]
	b.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	return true
}

type wakeMessage struct {
	PromptID string `json:"prompt_id"`
	OriginID string `json:"origin_id,omitempty"`
}

func pubsubChannel() string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return pubsubChannelBase + ":" + ns
	}
	return pubsubChannelBase
}

// wake nudges the waiter wherever it lives. Delivery is best-effort: the
// waiter's poll observes the answer even if the pub/sub message is lost.
func (b *Broker) wake(ctx context.Context, id string) {
	if b.wakeLocal(id) || b.rdb == nil {
		return
	}
	payload, err := json.Marshal(wakeMessage{PromptID: id, OriginID: instanceID})
	if err != nil {
		return
	}
	pubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := b.rdb.Publish(pubCtx, pubsubChannel(), payload).Err(); err != nil {
		logger.GetLogger(ctx).Warnf("agent prompt pubsub wake: %v", err)
	}
}

// runSubscriber listens for cross-instance wake-ups for the process lifetime.
func (b *Broker) runSubscriber() {
	ctx := context.Background()
	channel := pubsubChannel()
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		sub := b.rdb.Subscribe(ctx, channel)
		for msg := range sub.Channel() {
			backoff = time.Second
			var m wakeMessage
			if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
				logger.GetLogger(ctx).Warnf("agent prompt pubsub: bad payload: %v", err)
				continue
			}
			if m.OriginID != instanceID {
				b.wakeLocal(m.PromptID)
			}
		}
		_ = sub.Close()
		time.Sleep(backoff)
		backoff = min(backoff*2, maxBackoff)
	}
}
