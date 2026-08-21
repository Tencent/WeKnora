package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SessionSandboxBindingVersion is the current persisted binding schema.
const SessionSandboxBindingVersion = 1

// SessionSandboxKey identifies one tenant-scoped persistent sandbox.
type SessionSandboxKey struct {
	TenantID  uint64
	SessionID string
}

// Validate rejects keys that cannot identify a tenant session.
func (k SessionSandboxKey) Validate() error {
	if k.TenantID == 0 || strings.TrimSpace(k.SessionID) == "" {
		return errors.New("sandbox binding requires tenant and session")
	}
	if strings.ContainsAny(k.SessionID, "{}") {
		return errors.New("sandbox binding session must not contain braces")
	}
	for _, r := range k.SessionID {
		if unicode.IsControl(r) {
			return errors.New("sandbox binding session must not contain control characters")
		}
	}
	return nil
}

// SessionSandboxBinding records the remote sandbox assigned to a session.
type SessionSandboxBinding struct {
	Version    int            `json:"version"`
	Provider   RemoteProvider `json:"provider,omitempty"`
	TenantID   uint64         `json:"tenant_id"`
	SessionID  string         `json:"session_id"`
	SandboxID  string         `json:"sandbox_id"`
	TemplateID string         `json:"template_id"`
	CreatedAt  time.Time      `json:"created_at"`

	// ConfigID is the sandbox config the sandbox was created from. It is what
	// makes "every sandbox of this config" answerable from the binding store:
	// the sandbox itself carries the same value in provider metadata, but a
	// staleness mark must not depend on the provider being reachable.
	//
	// Bindings written before this field existed carry an empty value. They are
	// deliberately still valid — refusing them would break every live session
	// on upgrade — and simply match no config until they are rebuilt.
	ConfigID string `json:"config_id,omitempty"`

	// StaleAt marks a binding whose sandbox boots an image the config has
	// since replaced. The sandbox keeps serving until the session's next
	// resolve, which destroys and recreates it; see InvalidateByConfig.
	StaleAt *time.Time `json:"stale_at,omitempty"`
}

// Validate checks a binding against the current schema and authoritative key.
func (b SessionSandboxBinding) Validate(key SessionSandboxKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if b.Version != SessionSandboxBindingVersion {
		return fmt.Errorf(
			"sandbox binding version must be %d, got %d",
			SessionSandboxBindingVersion,
			b.Version,
		)
	}
	if !isRemoteProvider(b.Provider) {
		return fmt.Errorf("unsupported sandbox binding provider %q", b.Provider)
	}
	if b.TenantID != key.TenantID || b.SessionID != key.SessionID {
		return errors.New("sandbox binding identity does not match its key")
	}
	if strings.TrimSpace(b.SandboxID) == "" {
		return errors.New("sandbox binding requires sandbox ID")
	}
	if strings.TrimSpace(b.TemplateID) == "" {
		return errors.New("sandbox binding requires template ID")
	}
	if b.CreatedAt.IsZero() {
		return errors.New("sandbox binding requires creation time")
	}
	return nil
}

// SessionSandboxBindingStore persists bindings and serializes lifecycle
// transitions. Implementations must use create-if-absent and compare-delete
// semantics.
type SessionSandboxBindingStore interface {
	Get(context.Context, SessionSandboxKey) (*SessionSandboxBinding, error)
	Create(context.Context, SessionSandboxKey, SessionSandboxBinding) (bool, error)
	DeleteIfMatch(
		context.Context,
		SessionSandboxKey,
		RemoteProvider,
		string,
	) (bool, error)
	// WithLifecycleLock passes fn a request context carrying a separate
	// ownership context. The ownership context survives caller cancellation
	// but is canceled when exclusive lock ownership is lost.
	WithLifecycleLock(context.Context, SessionSandboxKey, func(context.Context) error) error

	// InvalidateByConfig marks every binding of one workspace's sandbox config
	// stale and reports how many it marked. Already-marked bindings are not
	// counted again.
	InvalidateByConfig(ctx context.Context, tenantID uint64, configID string) (int, error)
}

// bindingInvalidateLockTimeout bounds the wait for ONE session's lifecycle
// lock while marking. The caller runs inside the per-config install lock, so a
// single session that happens to be creating a sandbox right now must cost only
// its own mark rather than everyone else's.
const bindingInvalidateLockTimeout = 2 * time.Second

// bindingInvalidateMarkTimeout bounds the marking itself, once the lock is
// held. It is deliberately separate from the wait: sharing one budget would
// leave a mark that waited most of bindingInvalidateLockTimeout for the lock
// with milliseconds for its read plus compare-and-set, failing with a deadline
// error indistinguishable from a Redis fault.
const bindingInvalidateMarkTimeout = 3 * time.Second

// tenantBindingScanner is the store-specific half of InvalidateByConfig:
// enumerating one workspace's bindings and writing the mark. The serialisation
// and matching rules are shared, in invalidateBindingsByConfig.
type tenantBindingScanner interface {
	SessionSandboxBindingStore

	// listTenantBindingKeys returns the keys of every binding the store holds
	// for tenantID. Bindings that disappear before they are marked are
	// expected, not an error.
	listTenantBindingKeys(ctx context.Context, tenantID uint64) ([]SessionSandboxKey, error)

	// markBindingStale sets StaleAt only while the binding still names
	// expected's provider and sandbox ID, and reports whether it wrote.
	markBindingStale(
		ctx context.Context,
		key SessionSandboxKey,
		expected SessionSandboxBinding,
		staleAt time.Time,
	) (bool, error)
}

// invalidateBindingsByConfig marks one config's bindings stale, one session at
// a time under that session's lifecycle lock.
//
// The lock is what makes the mark reliable: resolve reads the binding, decides
// whether to replace the sandbox, and rebinds all inside it, so marking outside
// would silently lose against a resolve running at the same moment. The mark
// itself is only ever an added field — the binding is never deleted here — so a
// concurrent resolve can lose a mark but never a sandbox.
//
// One session that cannot be marked does not stop the others: the image pointer
// has already moved by the time this runs, and an unmarked session serves the
// previous image until it ends, which is strictly better than leaving the rest
// of the workspace unmarked too. The failures are still returned so the caller
// can say which sessions are affected.
func invalidateBindingsByConfig(
	ctx context.Context,
	store tenantBindingScanner,
	tenantID uint64,
	configID string,
) (int, error) {
	if tenantID == 0 {
		return 0, errors.New("sandbox binding invalidation requires a tenant")
	}
	keys, err := store.listTenantBindingKeys(ctx, tenantID)
	if err != nil {
		return 0, err
	}

	wanted := NormalizeConfigID(configID)
	marked := 0
	var failures []error
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		// The cap applies to acquiring the lock only: the timer is stopped once
		// fn runs, and the work below gets a budget of its own. Cancelling
		// waitCtx is how the wait is abandoned, so the timer cannot simply be
		// a deadline on the context fn is given.
		waitCtx, cancel := context.WithCancel(ctx)
		acquireTimer := time.AfterFunc(bindingInvalidateLockTimeout, cancel)
		err := store.WithLifecycleLock(waitCtx, key, func(lockCtx context.Context) error {
			// A false Stop means the cap fired as the lock was granted; then
			// lockCtx is already cancelled and the calls below fail, which is
			// the same outcome as losing the wait.
			acquireTimer.Stop()
			markCtx, cancelMark := context.WithTimeout(lockCtx, bindingInvalidateMarkTimeout)
			defer cancelMark()

			binding, err := store.Get(markCtx, key)
			if err != nil || binding == nil {
				return err
			}
			if binding.ConfigID != wanted || binding.StaleAt != nil {
				return nil
			}
			wrote, err := store.markBindingStale(
				markCtx, key, *binding, time.Now().UTC(),
			)
			if err != nil {
				return err
			}
			if wrote {
				marked++
			}
			return nil
		})
		acquireTimer.Stop()
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("mark session %s stale: %w", key.SessionID, err))
		}
	}
	return marked, errors.Join(failures...)
}

type lifecycleOwnershipContextKey struct{}

func withLifecycleOwnershipContext(
	ctx context.Context,
	ownershipCtx context.Context,
) context.Context {
	return context.WithValue(ctx, lifecycleOwnershipContextKey{}, ownershipCtx)
}

func lifecycleOwnershipContext(ctx context.Context) context.Context {
	if ownershipCtx, ok := ctx.Value(lifecycleOwnershipContextKey{}).(context.Context); ok {
		return ownershipCtx
	}
	// Unknown store implementations fail safe by retaining request/lock
	// cancellation rather than detaching destructive cleanup.
	return ctx
}

type memoryLifecycleLock struct {
	semaphore chan struct{}
	users     int
}

// MemorySessionSandboxBindingStore is a process-local implementation intended
// for tests and explicitly configured single-process deployments.
type MemorySessionSandboxBindingStore struct {
	mu       sync.Mutex
	bindings map[SessionSandboxKey]SessionSandboxBinding
	locks    map[SessionSandboxKey]*memoryLifecycleLock
}

// NewMemorySessionSandboxBindingStore creates an empty in-memory store.
func NewMemorySessionSandboxBindingStore() *MemorySessionSandboxBindingStore {
	return &MemorySessionSandboxBindingStore{
		bindings: make(map[SessionSandboxKey]SessionSandboxBinding),
		locks:    make(map[SessionSandboxKey]*memoryLifecycleLock),
	}
}

// Get returns the current binding, or nil when the session is unbound.
func (s *MemorySessionSandboxBindingStore) Get(
	ctx context.Context,
	key SessionSandboxKey,
) (*SessionSandboxBinding, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[key]
	if !ok {
		return nil, nil
	}
	result := binding
	return &result, nil
}

// Create stores a validated current-schema binding only if key is unbound.
func (s *MemorySessionSandboxBindingStore) Create(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) (bool, error) {
	if err := binding.Validate(key); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.bindings[key]; exists {
		return false, nil
	}
	s.bindings[key] = binding
	return true, nil
}

// DeleteIfMatch deletes only the expected provider and sandbox ID.
func (s *MemorySessionSandboxBindingStore) DeleteIfMatch(
	ctx context.Context,
	key SessionSandboxKey,
	provider RemoteProvider,
	sandboxID string,
) (bool, error) {
	if err := validateBindingMatch(key, provider, sandboxID); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	binding, exists := s.bindings[key]
	if !exists || binding.Provider != provider || binding.SandboxID != sandboxID {
		return false, nil
	}
	delete(s.bindings, key)
	return true, nil
}

// InvalidateByConfig marks this process's bindings of one config stale.
func (s *MemorySessionSandboxBindingStore) InvalidateByConfig(
	ctx context.Context,
	tenantID uint64,
	configID string,
) (int, error) {
	return invalidateBindingsByConfig(ctx, s, tenantID, configID)
}

func (s *MemorySessionSandboxBindingStore) listTenantBindingKeys(
	ctx context.Context,
	tenantID uint64,
) ([]SessionSandboxKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []SessionSandboxKey
	for key := range s.bindings {
		if key.TenantID == tenantID {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (s *MemorySessionSandboxBindingStore) markBindingStale(
	ctx context.Context,
	key SessionSandboxKey,
	expected SessionSandboxBinding,
	staleAt time.Time,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, exists := s.bindings[key]
	if !exists ||
		binding.Provider != expected.Provider ||
		binding.SandboxID != expected.SandboxID {
		return false, nil
	}
	marked := staleAt
	binding.StaleAt = &marked
	s.bindings[key] = binding
	return true, nil
}

// WithLifecycleLock runs fn while holding the process-local lock for key.
func (s *MemorySessionSandboxBindingStore) WithLifecycleLock(
	ctx context.Context,
	key SessionSandboxKey,
	fn func(context.Context) error,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("sandbox lifecycle lock callback is required")
	}

	s.mu.Lock()
	lock := s.locks[key]
	if lock == nil {
		lock = &memoryLifecycleLock{semaphore: make(chan struct{}, 1)}
		s.locks[key] = lock
	}
	lock.users++
	s.mu.Unlock()

	select {
	case lock.semaphore <- struct{}{}:
	case <-ctx.Done():
		s.dropMemoryLifecycleLockUser(key, lock)
		return ctx.Err()
	}

	defer func() {
		<-lock.semaphore
		s.dropMemoryLifecycleLockUser(key, lock)
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(withLifecycleOwnershipContext(ctx, context.WithoutCancel(ctx)))
}

func (s *MemorySessionSandboxBindingStore) dropMemoryLifecycleLockUser(
	key SessionSandboxKey,
	lock *memoryLifecycleLock,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock.users--
	if lock.users == 0 {
		delete(s.locks, key)
	}
}

func validateBindingMatch(
	key SessionSandboxKey,
	provider RemoteProvider,
	sandboxID string,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if !isRemoteProvider(provider) {
		return fmt.Errorf("unsupported sandbox binding provider %q", provider)
	}
	if strings.TrimSpace(sandboxID) == "" {
		return errors.New("sandbox binding match requires sandbox ID")
	}
	return nil
}

// Both stores must keep answering "which bindings belong to this config", or
// an image switch stops reaching the sessions already running.
var _ tenantBindingScanner = (*MemorySessionSandboxBindingStore)(nil)

func isRemoteProvider(provider RemoteProvider) bool {
	switch provider {
	case SandboxTypeCube, SandboxTypeE2B, SandboxTypeDocker:
		return true
	default:
		return false
	}
}
