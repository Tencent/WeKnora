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

func isRemoteProvider(provider RemoteProvider) bool {
	return provider == SandboxTypeCube || provider == SandboxTypeE2B
}
