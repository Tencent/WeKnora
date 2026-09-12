package sandbox

import (
	"context"
	"errors"
	"sync"
	"time"
)

func remoteCommandTerminalFrom(client RemoteSandboxClient) (remoteCommandTerminalProvider, bool) {
	if client == nil || !client.Capabilities().SupportsCommandTerminals {
		return nil, false
	}
	switch wrapped := client.(type) {
	case *langfuseRemoteClient:
		return remoteCommandTerminalFrom(wrapped.inner)
	case *langfuseSnapshotClient:
		return remoteCommandTerminalFrom(wrapped.inner)
	}
	provider, ok := client.(remoteCommandTerminalProvider)
	return provider, ok
}

// SessionCommandTerminalProvider advertises bounded command PTYs independently
// of the interactive terminal's reconnect capability and sandbox-local probes.
func (m *SessionBoundManager) SessionCommandTerminalProvider() SessionCommandTerminalProvider {
	if m == nil || m.remoteDisabled() {
		return nil
	}
	if _, ok := remoteCommandTerminalFrom(m.client); !ok {
		return nil
	}
	if _, ok := m.bindings.(sessionTurnLeaseStore); !ok {
		return nil
	}
	return m
}

// OpenSessionCommandTerminal holds a reference-counted session turn until execution
// AND process cleanup finish. Begin/resolve/consume happen under the lifecycle
// lock, so a concurrent image invalidation cannot replace the active sandbox.
// Nested chat/terminal turns share the same lease. At most every 30 seconds
// the lease and remote idle TTL are renewed; a failed renewal stops execution.
// Explicit session deletion can still destroy a running sandbox.
func (m *SessionBoundManager) OpenSessionCommandTerminal(
	ctx context.Context, sessionID string, req CommandTerminalRequest,
) (CommandTerminal, error) {
	if err := m.requireRemoteBackend(); err != nil {
		return nil, err
	}
	req, err := normalizeTerminalRequest(req)
	if err != nil {
		return nil, err
	}
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	provider, ok := remoteCommandTerminalFrom(m.client)
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
		// The ordinary resolve logs consumption failures. CommandTerminal must fail
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
	inner, err := provider.OpenCommandTerminal(ctx, handle, req)
	if err != nil {
		err = errors.Join(terminalProviderError("start", err), release())
		span.Finish(nil, nil, err)
		return nil, err
	}
	t := &sessionTerminal{CommandTerminal: inner, done: make(chan struct{})}
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
				if leaseErr == nil && (refreshed == nil || refreshed.ID() != handle.ID() ||
					refreshed.Provider() != handle.Provider()) {
					leaseErr = errors.New("sandbox: terminal keepalive returned a mismatched handle")
				}
			}
			cancel()
			if leaseErr != nil || !active || rebuild {
				closeErr := inner.Close()
				t.result = terminalResult{
					exit: CommandTerminalExit{ExitCode: -1, Reason: "lease_lost"},
					err:  errors.Join(errors.New("sandbox: terminal turn lease lost"), leaseErr, closeErr, release()),
				}
				break
			}
		}
		span.Finish(t.result.exit, nil, t.result.err)
		close(t.done)
	}()
	return t, nil
}

func terminalSpanInput(req CommandTerminalRequest) map[string]interface{} {
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
	CommandTerminal
	done   chan struct{}
	result terminalResult
}

func (t *sessionTerminal) Wait(ctx context.Context) (CommandTerminalExit, error) {
	select {
	case <-t.done:
		return t.result.exit, t.result.err
	case <-ctx.Done():
		return CommandTerminalExit{}, ctx.Err()
	}
}

func (t *sessionTerminal) Close() error {
	err := t.CommandTerminal.Close()
	<-t.done
	return errors.Join(err, t.result.err)
}

var _ SessionCommandTerminalProvider = (*SessionBoundManager)(nil)
