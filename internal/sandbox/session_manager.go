package sandbox

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// SessionBoundManager is a sandbox.Manager implementation that keeps one
// persistent CubeSandbox instance per SessionID. It gives the caller
// state-preserving script execution semantics: pip installs, files, and
// long-running services set up during one Execute call are still visible
// in the next call from the same session.
//
// Design notes:
//
//   - Sandboxes are created lazily on the first Execute for a given
//     SessionID, so purely-conversational sessions never allocate a MicroVM.
//   - Executions from a request without a SessionID (e.g. an ephemeral tool
//     invocation outside the chat pipeline) fall back to an ephemeral
//     CubeSandbox, giving stateless-per-call semantics for backwards
//     compatibility with DockerSandbox / LocalSandbox callers.
//   - The reaper goroutine periodically walks the map and kills sandboxes
//     that have been idle for longer than CubeIdleTTL * idleReapMultiplier.
//   - When the Cube API is unreachable at construction time, we transparently
//     fall back to LocalSandbox if config.FallbackEnabled is true; otherwise
//     the constructor returns an error so callers can decide how to react.
//
// SessionBoundManager satisfies the Manager interface, so it can be dropped
// into any place that previously held a *DefaultManager. Callers that want to
// tear down a specific session's sandbox (e.g. sessionService.DeleteSession)
// can type-assert to the exported DestroySession method.
type SessionBoundManager struct {
	config    *Config
	validator *ScriptValidator
	client    *cubeClient

	// sandboxes maps SessionID -> *CubeSandbox.
	sandboxes sync.Map

	// fallback is used when the Cube API is unreachable. Nil when disabled.
	fallback Sandbox

	// mu guards state that has to change atomically (fallback + closed).
	mu     sync.RWMutex
	closed bool

	// reaper lifecycle
	reaperCancel context.CancelFunc
	reaperWG     sync.WaitGroup
}

// idleReapMultiplier controls when idle sandboxes transition from "idle" to
// "kill". A sandbox idle for CubeIdleTTL * idleReapMultiplier is torn down.
const idleReapMultiplier = 3

// NewSessionBoundManager wires up a SessionBoundManager from Config.
// It performs a health probe against CubeAPI so callers can detect fatal
// mis-configuration up-front instead of at first Execute.
func NewSessionBoundManager(config *Config) (*SessionBoundManager, error) {
	if config == nil {
		config = DefaultConfig()
	}
	config.Type = SandboxTypeCube
	if err := ValidateConfig(config); err != nil {
		return nil, fmt.Errorf("invalid sandbox config: %w", err)
	}
	applyCubeDefaults(config)

	m := &SessionBoundManager{
		config:    config,
		validator: NewScriptValidator(),
		client:    newCubeClient(config),
	}

	// Probe once so the caller can log a friendly diagnostic. We swallow the
	// error and let the fallback path handle it — the Cube deployment might
	// come online after WeKnora starts.
	probeCtx, cancel := context.WithTimeout(context.Background(), config.CubeHTTPTimeout)
	defer cancel()
	if err := m.client.Health(probeCtx); err != nil {
		if !config.FallbackEnabled {
			return nil, fmt.Errorf("cube sandbox unavailable: %w", err)
		}
		log.Printf("[sandbox] cube api unreachable at %s (%v); falling back to local sandbox", config.CubeAPIURL, err)
		m.fallback = NewLocalSandbox(config)
	}

	m.startReaper()
	return m, nil
}

// Type reports the current effective sandbox type. When the Cube API is
// unreachable and the fallback engaged, this returns SandboxTypeLocal.
func (m *SessionBoundManager) GetType() SandboxType {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fallback != nil {
		return m.fallback.Type()
	}
	return SandboxTypeCube
}

// GetSandbox returns a representative sandbox for callers that need to
// inspect availability. It intentionally does not create one on demand —
// SessionBoundManager binds sandboxes to session IDs, not to the manager
// itself.
func (m *SessionBoundManager) GetSandbox() Sandbox {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fallback != nil {
		return m.fallback
	}
	// Return the first live sandbox we can find. Callers only use this for
	// diagnostics (Type / IsAvailable).
	var picked Sandbox
	m.sandboxes.Range(func(_, v any) bool {
		picked = v.(*CubeSandbox)
		return false
	})
	return picked
}

// Execute runs config against the per-SessionID sandbox (or an ephemeral one
// if no SessionID is provided). All the shared security validation runs
// first, matching DefaultManager's behaviour.
func (m *SessionBoundManager) Execute(ctx context.Context, config *ExecuteConfig) (*ExecuteResult, error) {
	if m == nil {
		return nil, ErrSandboxDisabled
	}
	m.mu.RLock()
	if m.closed {
		m.mu.RUnlock()
		return nil, ErrSandboxDisabled
	}
	fallback := m.fallback
	m.mu.RUnlock()

	// Security validation is applied regardless of backend.
	if !config.SkipValidation {
		if err := runScriptValidation(m.validator, config); err != nil {
			log.Printf("[sandbox] Security validation failed: %v", err)
			return &ExecuteResult{
				ExitCode: -1,
				Error:    err.Error(),
				Stderr:   fmt.Sprintf("Security validation failed: %v", err),
			}, ErrSecurityViolation
		}
	}

	// If the Cube API was unavailable at construction time, honour the
	// configured fallback so scripting keeps working under partial outages.
	if fallback != nil {
		return fallback.Execute(ctx, config)
	}

	sandbox, err := m.resolveSandbox(ctx, config.SessionID)
	if err != nil {
		return nil, err
	}
	return sandbox.Execute(ctx, config)
}

// resolveSandbox returns the CubeSandbox bound to sessionID, creating one
// lazily. When sessionID is empty, a fresh ephemeral sandbox is returned so
// each call is independent.
func (m *SessionBoundManager) resolveSandbox(_ context.Context, sessionID string) (*CubeSandbox, error) {
	if sessionID == "" {
		return newPersistentCubeSandbox(m.config, m.client).withEphemeral(true), nil
	}
	if existing, ok := m.sandboxes.Load(sessionID); ok {
		return existing.(*CubeSandbox), nil
	}
	sb := newPersistentCubeSandbox(m.config, m.client)
	actual, loaded := m.sandboxes.LoadOrStore(sessionID, sb)
	if loaded {
		return actual.(*CubeSandbox), nil
	}
	return sb, nil
}

// DestroySession tears down the sandbox bound to sessionID, if any.
// The sessionService.DeleteSession hook calls this so orphaned MicroVMs get
// cleaned up promptly on user-initiated session deletion.
func (m *SessionBoundManager) DestroySession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	v, ok := m.sandboxes.LoadAndDelete(sessionID)
	if !ok {
		return nil
	}
	return v.(*CubeSandbox).Cleanup(ctx)
}

// Cleanup destroys every remaining sandbox and stops the reaper. It is safe
// to call multiple times.
func (m *SessionBoundManager) Cleanup(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	cancel := m.reaperCancel
	m.reaperCancel = nil
	fallback := m.fallback
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.reaperWG.Wait()

	var firstErr error
	m.sandboxes.Range(func(k, v any) bool {
		sb := v.(*CubeSandbox)
		m.sandboxes.Delete(k)
		if err := sb.Cleanup(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		return true
	})
	if fallback != nil {
		if err := fallback.Cleanup(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// startReaper launches the background goroutine that kills idle sandboxes.
// The tick interval is CubeIdleTTL / 4 so idle sandboxes are reaped
// promptly even for very short TTLs; a lower bound of 15s prevents
// pathological configurations from hammering the API.
func (m *SessionBoundManager) startReaper() {
	if m.config.CubeIdleTTL <= 0 {
		return
	}
	interval := m.config.CubeIdleTTL / 4
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.reaperCancel = cancel
	m.reaperWG.Add(1)
	go func() {
		defer m.reaperWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reapOnce(ctx)
			}
		}
	}()
}

// reapOnce walks the sandbox map once and kills anything that has been idle
// for longer than CubeIdleTTL * idleReapMultiplier.
func (m *SessionBoundManager) reapOnce(ctx context.Context) {
	idleThreshold := m.config.CubeIdleTTL * idleReapMultiplier
	cutoff := time.Now().Add(-idleThreshold)

	m.sandboxes.Range(func(k, v any) bool {
		sb := v.(*CubeSandbox)
		last := sb.LastUsed()
		if last.IsZero() {
			// Never used since being registered — skip on this pass to give
			// the caller a chance to actually run something.
			return true
		}
		if last.Before(cutoff) {
			m.sandboxes.Delete(k)
			if err := sb.Cleanup(ctx); err != nil {
				log.Printf("[sandbox] reaper: cleanup session %v: %v", k, err)
			} else {
				log.Printf("[sandbox] reaper: reclaimed idle sandbox for session %v", k)
			}
		}
		return true
	})
}

// withEphemeral toggles the ephemeral flag on a CubeSandbox and returns the
// same pointer for fluent style. Kept private because it's only useful to
// the manager's dispatch logic.
func (s *CubeSandbox) withEphemeral(v bool) *CubeSandbox {
	s.ephemeral = v
	return s
}
