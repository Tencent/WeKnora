package sandbox

import (
	"context"
	"errors"
	"sync"
	"time"
)

func remoteTerminalFrom(client RemoteSandboxClient) (remoteTerminalProvider, bool) {
	switch wrapped := client.(type) {
	case *langfuseRemoteClient:
		return remoteTerminalFrom(wrapped.inner)
	case *langfuseSnapshotClient:
		return remoteTerminalFrom(wrapped.inner)
	}
	provider, ok := client.(remoteTerminalProvider)
	return provider, ok
}

func (m *SessionBoundManager) SessionTerminalProvider() SessionTerminalProvider {
	if m == nil || m.remoteDisabled() || workbenchRuntimeKnownIncompatible(m, workbenchRuntimeTerminal) {
		return nil
	}
	if _, ok := remoteTerminalFrom(m.client); !ok {
		return nil
	}
	if _, ok := m.bindings.(sessionTurnLeaseStore); !ok {
		return nil
	}
	return m
}

// OpenSessionTerminal holds a reference-counted session turn until execution
// AND process cleanup finish. Begin/resolve/consume happen under the lifecycle
// lock, so a concurrent image invalidation cannot replace the active sandbox.
// Nested chat/terminal turns share the same lease. At most every 30 seconds
// the lease and remote idle TTL are renewed; a failed renewal stops execution.
// Explicit session deletion can still destroy a running sandbox.
func (m *SessionBoundManager) OpenSessionTerminal(ctx context.Context, sessionID string, req TerminalRequest) (Terminal, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	if workbenchRuntimeKnownIncompatible(m, workbenchRuntimeTerminal) {
		return nil, ErrWorkbenchRuntimeIncompatible
	}
	req, err := normalizeTerminalRequest(req)
	if err != nil {
		return nil, err
	}
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	provider, ok := remoteTerminalFrom(m.client)
	if !ok {
		return nil, errors.New("sandbox: native session PTY unavailable")
	}
	leaser, ok := m.bindings.(sessionTurnLeaseStore)
	if !ok {
		return nil, errors.New("sandbox: terminal requires an active-turn lease store")
	}
	var releaseOnce sync.Once
	var releaseErr error
	leased := false
	release := func() error {
		releaseOnce.Do(func() {
			if leased {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalCleanupTimeout)
				defer cancel()
				releaseErr = terminalProviderError("release lease", leaser.EndTurn(cleanupCtx, key))
			}
		})
		return releaseErr
	}
	var handle RemoteSandboxHandle
	err = m.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		if err := leaser.BeginTurn(lockCtx, key); err != nil {
			return err
		}
		leased = true
		var err error
		handle, err = m.lifecycle.resolveLocked(lockCtx, key)
		if err != nil {
			return err
		}
		// The ordinary resolve logs consumption failures. Terminal must fail
		// closed: otherwise the next resolve could replace its running image.
		return leaser.ConsumeTurnRebuild(lockCtx, key)
	})
	if err != nil {
		return nil, errors.Join(terminalProviderError("resolve", err), release())
	}
	if err := m.ensureSessionWorkspaceDirs(ctx, handle, SessionOutputRoot); err != nil {
		return nil, errors.Join(terminalProviderError("prepare workspace", err), release())
	}
	if err := m.ensureWorkbenchRuntime(ctx, handle, false, workbenchRuntimeTerminal); err != nil {
		return nil, errors.Join(err, release())
	}
	ctx, span := startSandboxSpan(ctx, "sandbox.terminal", terminalSpanInput(req), sandboxHandleMeta(handle))
	inner, err := provider.OpenTerminal(ctx, handle, req)
	if err != nil {
		err = errors.Join(terminalProviderError("start", err), release())
		span.Finish(nil, nil, err)
		return nil, err
	}
	t := &sessionTerminal{Terminal: inner, done: make(chan struct{})}
	heartbeat := terminalHeartbeatInterval(m.config, m.GetType())
	go func() {
		for {
			waitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), heartbeat)
			exit, err := inner.Wait(waitCtx)
			waitExpired := errors.Is(waitCtx.Err(), context.DeadlineExceeded)
			cancel()
			// A completed terminal can itself contain a provider deadline
			// error. Only our wait timer means the command is still running.
			if !waitExpired || !errors.Is(err, context.DeadlineExceeded) {
				t.result = terminalResult{exit: exit, err: errors.Join(err, release())}
				break
			}
			leaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), terminalOperationTimeout)
			active, rebuild, leaseErr := leaser.TurnState(leaseCtx, key)
			if leaseErr == nil && active && !rebuild {
				// Unlike TurnState's best-effort PExpire, this returns renewal
				// failures, so a terminal cannot silently outlive its lease.
				leaseErr = leaser.ConsumeTurnRebuild(leaseCtx, key)
			}
			if leaseErr == nil && active && !rebuild && m.client.Capabilities().SupportsTimeoutRefresh {
				// Connect refreshes the idle lifetime without resolving/replacing
				// the binding. Use the same backend and existing opaque handle.
				client := provider.(RemoteSandboxClient)
				refreshed, err := client.Connect(leaseCtx, RemoteConnectRequest{
					SandboxID: handle.ID(), TrafficAccessToken: InboundTokenOf(handle),
				})
				leaseErr = terminalProviderError("keepalive", err)
				if leaseErr == nil && (refreshed == nil || refreshed.ID() != handle.ID() || refreshed.Provider() != handle.Provider()) {
					leaseErr = errors.New("sandbox: terminal keepalive returned a mismatched handle")
				}
			}
			cancel()
			if leaseErr != nil || !active || rebuild {
				closeErr := inner.Close()
				t.result = terminalResult{exit: TerminalExit{ExitCode: -1, Reason: "lease_lost"},
					err: errors.Join(errors.New("sandbox: terminal turn lease lost"), leaseErr, closeErr, release())}
				break
			}
		}
		span.Finish(t.result.exit, nil, t.result.err)
		close(t.done)
	}()
	return t, nil
}

func terminalSpanInput(req TerminalRequest) map[string]interface{} {
	// Only the service audit layer owns redacted command content. Truncation
	// does not redact inline tokens, and even stdout may contain credentials.
	return map[string]interface{}{
		"command_bytes": len(req.Command), "timeout_ms": req.Timeout.Milliseconds(),
		"cpu_seconds": req.CPUSeconds, "memory_bytes": req.MemoryBytes,
	}
}

func terminalHeartbeatInterval(cfg *Config, provider SandboxType) time.Duration {
	interval := 30 * time.Second
	var ttl time.Duration
	if provider == SandboxTypeE2B {
		ttl = cfg.E2BSandboxTTL
	}
	if provider == SandboxTypeCube {
		ttl = cfg.CubeSandboxTTL
	}
	if ttl > 0 && ttl/3 < interval {
		interval = ttl / 3
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	return interval
}

type sessionTerminal struct {
	Terminal
	done   chan struct{}
	result terminalResult
}

func (t *sessionTerminal) Wait(ctx context.Context) (TerminalExit, error) {
	select {
	case <-t.done:
		return t.result.exit, t.result.err
	case <-ctx.Done():
		return TerminalExit{}, ctx.Err()
	}
}

func (t *sessionTerminal) Close() error {
	err := t.Terminal.Close()
	<-t.done
	return errors.Join(err, t.result.err)
}

var _ SessionTerminalProvider = (*SessionBoundManager)(nil)
