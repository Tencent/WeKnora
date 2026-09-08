package sandbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	workbenchRuntimeContract     = "weknora-workbench-runtime/v1"
	workbenchProbeTimeout        = 10 * time.Second
	workbenchRuntimeCacheTTL     = 30 * time.Minute
	workbenchRuntimeConfigLimit  = 1024
	workbenchRuntimeSandboxLimit = 4096
)

var ErrWorkbenchRuntimeIncompatible = errors.New("sandbox: workbench runtime contract unavailable")

//go:embed terminal_runtime_probe.py
var terminalRuntimeProbe string

type workbenchRuntimeSupport struct {
	terminal bool
	files    bool
}

type workbenchRuntimeRequirement uint8

const (
	workbenchRuntimeAny workbenchRuntimeRequirement = iota
	workbenchRuntimeTerminal
	workbenchRuntimeFiles
)

func (s workbenchRuntimeSupport) meets(requirement workbenchRuntimeRequirement) bool {
	switch requirement {
	case workbenchRuntimeTerminal:
		return s.terminal
	case workbenchRuntimeFiles:
		return s.files
	default:
		return s.terminal || s.files
	}
}

type workbenchRuntimeKey struct {
	store      uintptr
	provider   SandboxType
	configID   string
	templateID string
	dataPlane  string
}

type workbenchSandboxRuntimeKey struct {
	runtime workbenchRuntimeKey
	id      string
}

type workbenchRuntimeCacheEntry struct {
	support   workbenchRuntimeSupport
	checkedAt time.Time
}

type workbenchRuntimeStateCache struct {
	mu           sync.Mutex
	runtimes     map[workbenchRuntimeKey]workbenchRuntimeCacheEntry
	sandboxes    map[workbenchSandboxRuntimeKey]workbenchRuntimeCacheEntry
	runtimeLimit int
	sandboxLimit int
	ttl          time.Duration
	now          func() time.Time
}

func newWorkbenchRuntimeStateCache(
	runtimeLimit, sandboxLimit int,
	ttl time.Duration,
) *workbenchRuntimeStateCache {
	return &workbenchRuntimeStateCache{
		runtimes:     make(map[workbenchRuntimeKey]workbenchRuntimeCacheEntry),
		sandboxes:    make(map[workbenchSandboxRuntimeKey]workbenchRuntimeCacheEntry),
		runtimeLimit: runtimeLimit,
		sandboxLimit: sandboxLimit,
		ttl:          ttl,
		now:          time.Now,
	}
}

func (c *workbenchRuntimeStateCache) expired(
	entry workbenchRuntimeCacheEntry,
	now time.Time,
) bool {
	return c.ttl > 0 && now.Sub(entry.checkedAt) > c.ttl
}

func (c *workbenchRuntimeStateCache) loadRuntime(
	key workbenchRuntimeKey,
) (workbenchRuntimeSupport, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.runtimes[key]
	if !found {
		return workbenchRuntimeSupport{}, false
	}
	if c.expired(entry, c.now()) {
		delete(c.runtimes, key)
		return workbenchRuntimeSupport{}, false
	}
	return entry.support, true
}

func (c *workbenchRuntimeStateCache) loadSandbox(
	key workbenchSandboxRuntimeKey,
) (workbenchRuntimeSupport, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, found := c.sandboxes[key]
	if !found {
		return workbenchRuntimeSupport{}, false
	}
	if c.expired(entry, c.now()) {
		delete(c.sandboxes, key)
		return workbenchRuntimeSupport{}, false
	}
	return entry.support, true
}

func (c *workbenchRuntimeStateCache) store(
	runtimeKey workbenchRuntimeKey,
	sandboxKey workbenchSandboxRuntimeKey,
	support workbenchRuntimeSupport,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	entry := workbenchRuntimeCacheEntry{support: support, checkedAt: now}
	c.runtimes[runtimeKey] = entry
	c.sandboxes[sandboxKey] = entry
	c.pruneLocked(now)
}

func (c *workbenchRuntimeStateCache) pruneLocked(now time.Time) {
	for key, entry := range c.runtimes {
		if c.expired(entry, now) {
			delete(c.runtimes, key)
		}
	}
	for key, entry := range c.sandboxes {
		if c.expired(entry, now) {
			delete(c.sandboxes, key)
		}
	}
	for len(c.runtimes) > c.runtimeLimit {
		var oldestKey workbenchRuntimeKey
		var oldest time.Time
		for key, entry := range c.runtimes {
			if oldest.IsZero() || entry.checkedAt.Before(oldest) {
				oldestKey, oldest = key, entry.checkedAt
			}
		}
		delete(c.runtimes, oldestKey)
	}
	for len(c.sandboxes) > c.sandboxLimit {
		var oldestKey workbenchSandboxRuntimeKey
		var oldest time.Time
		for key, entry := range c.sandboxes {
			if oldest.IsZero() || entry.checkedAt.Before(oldest) {
				oldestKey, oldest = key, entry.checkedAt
			}
		}
		delete(c.sandboxes, oldestKey)
	}
}

// Tenant managers are rebuilt per request. The binding store and config
// identity remain stable, so this cache lets a later lookup-only Status report
// an incompatibility learned by an explicit initialization without probing or
// allocating a sandbox itself. TTL and count caps prevent deleted sandbox IDs
// and retired configs from accumulating for the lifetime of the process.
var workbenchRuntimeCache = newWorkbenchRuntimeStateCache(
	workbenchRuntimeConfigLimit,
	workbenchRuntimeSandboxLimit,
	workbenchRuntimeCacheTTL,
)

// SessionWorkbenchInitializer provisions the bound session only on an explicit
// workbench initialization action. File reads remain lookup-only operations.
type SessionWorkbenchInitializer interface {
	EnsureWorkbenchSession(context.Context, string) error
}

func (m *SessionBoundManager) EnsureWorkbenchSession(
	ctx context.Context,
	sessionID string,
) (retErr error) {
	if err := m.requireRemoteBackend(); err != nil {
		return err
	}
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return err
	}
	leaser, holdsTurn := m.bindings.(sessionTurnLeaseStore)
	turnStarted := false
	defer func() {
		if !turnStarted {
			return
		}
		releaseCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			terminalCleanupTimeout,
		)
		defer cancel()
		retErr = errors.Join(retErr, leaser.EndTurn(releaseCtx, key))
	}()

	err = m.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		if holdsTurn {
			if err := leaser.BeginTurn(lockCtx, key); err != nil {
				return err
			}
			turnStarted = true
		}

		// An idle sweep can remove an expired sandbox after Resolve but before
		// the first workspace exec refreshes its activity marker. Re-resolve
		// once only when the provider proves that sandbox is gone.
		for attempt := 0; attempt < 2; attempt++ {
			handle, err := m.lifecycle.resolveLocked(lockCtx, key)
			if err != nil {
				return err
			}
			if err = m.ensureSessionWorkspaceDirs(
				lockCtx,
				handle,
				SessionOutputRoot,
			); err != nil {
				if attempt == 0 && retryWorkbenchInitialization(err) {
					continue
				}
				return err
			}
			// Explicit initialization always retries the contract. This lets
			// an operator repair a mutable image after an earlier failed probe.
			err = m.ensureWorkbenchRuntime(
				lockCtx,
				handle,
				true,
				workbenchRuntimeAny,
			)
			if err != nil && attempt == 0 &&
				retryWorkbenchInitialization(err) {
				continue
			}
			return err
		}
		return ErrWorkbenchRuntimeIncompatible
	})
	if err != nil {
		return err
	}
	return nil
}

func retryWorkbenchInitialization(err error) bool {
	return CanReplaceRemoteBinding(err) || IsRemoteConflict(err)
}

func (m *SessionBoundManager) ensureWorkbenchRuntime(
	ctx context.Context, handle RemoteSandboxHandle, force bool,
	requirement workbenchRuntimeRequirement,
) error {
	key, ok := workbenchRuntimeCacheKey(m)
	if !ok || handle == nil || handle.ID() == "" {
		return ErrWorkbenchRuntimeIncompatible
	}
	sandboxKey := workbenchSandboxRuntimeKey{runtime: key, id: handle.ID()}
	if !force {
		if state, found := workbenchRuntimeCache.loadSandbox(sandboxKey); found {
			if state.meets(requirement) {
				return nil
			}
			return ErrWorkbenchRuntimeIncompatible
		}
		if workbenchRuntimeKnownIncompatible(m, requirement) {
			return ErrWorkbenchRuntimeIncompatible
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrWorkbenchRuntimeIncompatible, err)
	}
	probeCtx, cancel := context.WithTimeout(ctx, workbenchProbeTimeout)
	defer cancel()
	result, err := m.client.Exec(probeCtx, handle, RemoteExecRequest{
		Command: "python3",
		Args:    []string{"-I", "-c", terminalRuntimeProbe},
		WorkDir: "/",
		User:    DefaultSandboxExecUser,
		Env:     map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "LC_ALL": "C.UTF-8"},
		Timeout: workbenchProbeTimeout,
	})
	if err != nil || probeCtx.Err() != nil || result == nil || result.Killed {
		if probeCtx.Err() != nil {
			return errors.Join(ErrWorkbenchRuntimeIncompatible, probeCtx.Err())
		}
		if err != nil {
			return errors.Join(ErrWorkbenchRuntimeIncompatible, err)
		}
		return ErrWorkbenchRuntimeIncompatible
	}
	state := workbenchRuntimeSupport{}
	if result.ExitCode == 0 && len(result.Stdout) <= 1024 && len(result.Stderr) <= 1024 {
		state, _ = validWorkbenchRuntimeReply(result.Stdout)
	}
	workbenchRuntimeCache.store(key, sandboxKey, state)
	if !state.meets(requirement) {
		return ErrWorkbenchRuntimeIncompatible
	}
	return nil
}

func validWorkbenchRuntimeReply(raw string) (workbenchRuntimeSupport, bool) {
	var reply struct {
		Contract        string `json:"contract"`
		Terminal        bool   `json:"terminal"`
		Files           bool   `json:"files"`
		MissingTerminal string `json:"missing_terminal,omitempty"`
		MissingFiles    string `json:"missing_files,omitempty"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reply); err != nil {
		return workbenchRuntimeSupport{}, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return workbenchRuntimeSupport{}, false
	}
	if reply.Contract != workbenchRuntimeContract ||
		(reply.Terminal && reply.MissingTerminal != "") ||
		(reply.Files && reply.MissingFiles != "") {
		return workbenchRuntimeSupport{}, false
	}
	return workbenchRuntimeSupport{terminal: reply.Terminal, files: reply.Files}, true
}

func workbenchRuntimeKnownIncompatible(
	m *SessionBoundManager, requirement workbenchRuntimeRequirement,
) bool {
	key, ok := workbenchRuntimeCacheKey(m)
	if !ok {
		return false
	}
	state, found := workbenchRuntimeCache.loadRuntime(key)
	return found && !state.meets(requirement)
}

func workbenchRuntimeCacheKey(m *SessionBoundManager) (workbenchRuntimeKey, bool) {
	if m == nil || m.config == nil || m.lifecycle == nil || m.bindings == nil {
		return workbenchRuntimeKey{}, false
	}
	value := reflect.ValueOf(m.bindings)
	var store uintptr
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		if !value.IsNil() {
			store = value.Pointer()
		}
	}
	if store == 0 {
		store = reflect.ValueOf(m).Pointer()
	}
	key := workbenchRuntimeKey{
		store:      store,
		provider:   m.GetType(),
		configID:   m.lifecycle.sandboxConfigID,
		templateID: m.lifecycle.createRequest.TemplateID,
	}
	switch key.provider {
	case SandboxTypeDocker:
		key.dataPlane = m.config.DockerHost
	case SandboxTypeE2B:
		key.dataPlane = m.config.E2BAPIURL + "\x00" + m.config.E2BProxyURL + "\x00" + m.config.E2BSandboxDomain
	case SandboxTypeCube:
		key.dataPlane = m.config.CubeAPIURL + "\x00" + m.config.CubeProxyURL + "\x00" + m.config.CubeSandboxDomain
	default:
		return workbenchRuntimeKey{}, false
	}
	return key, true
}

var _ SessionWorkbenchInitializer = (*SessionBoundManager)(nil)
