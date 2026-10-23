package service

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestShellJobHeartbeatRefreshesProvidersWhileReadsAndAnotherProviderBlock(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	svc.heartbeatInterval = 5 * time.Millisecond
	refs := []interfaces.SessionRef{{TenantID: 7, SessionID: "blocked"}, {TenantID: 7, SessionID: "queued"}}
	for i, ref := range refs {
		id := []string{"sj_a001", "sj_a002"}[i]
		require.NoError(t, index.Add(t.Context(), ShellJobIndexEntry{
			SessionRef: ref, WorkID: id, SandboxID: "sbx1", YieldUntil: time.Now().Add(-time.Minute),
		}))
		require.NoError(t, bg.Registry.Add(t.Context(), ref,
			interfaces.BackgroundActivity{ID: id, Kind: "shell"}, time.Minute, 0))
	}
	refreshed := make(chan string, 100)
	readStarted := make(chan struct{}, len(refs))
	releaseRead := make(chan struct{})
	svc.remoteRead = func(_ context.Context, _ interfaces.SessionRef, _ []string, _ map[string]string) (
		[]sandbox.ShellJobSnapshot, map[string]error,
	) {
		readStarted <- struct{}{}
		// Maintenance reads detach from sweep cancellation. Release this fake
		// explicitly, just as the real remote exec has its own bounded lifetime.
		<-releaseRead
		return nil, nil
	}
	providerBlocked := make(chan struct{})
	var blockedOnce sync.Once
	svc.keeper = keeperFunc(func(ctx context.Context, ref interfaces.SessionRef) error {
		if ref == refs[0] {
			blockedOnce.Do(func() { close(providerBlocked) })
			<-ctx.Done()
			return ctx.Err()
		}
		select {
		case refreshed <- ref.SessionID:
		default:
		}
		return nil
	})
	// A sweep cannot reach renewRunning while waiting for this session lock.
	locked, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseLock := func() { releaseOnce.Do(func() { close(release) }) }
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- index.WithSessionLock(context.Background(), refs[0], func(context.Context) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		close(releaseRead)
		releaseLock()
		require.NoError(t, <-lockDone)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("heartbeat did not stop with the sweep")
		}
	})
	go func() { done <- svc.SweepOnce(ctx) }()
	select {
	case <-providerBlocked:
	case <-time.After(time.Second):
		t.Fatal("a blocked sweep never refreshed its sandbox provider")
	}
	for range 2 {
		select {
		case sid := <-refreshed:
			require.Equal(t, refs[1].SessionID, sid)
		case <-time.After(time.Second):
			t.Fatal("one slow provider starved another session's TTL refresh")
		}
	}
	// The map of claimed sessions has no stable order. Ensure the sweep has
	// reached a blocked read even when it selected the locked session first.
	releaseLock()
	select {
	case <-readStarted:
	case <-time.After(time.Second):
		t.Fatal("sweep never entered remote read")
	}
	for len(refreshed) > 0 {
		<-refreshed
	}
	for range 2 {
		select {
		case sid := <-refreshed:
			require.Equal(t, refs[1].SessionID, sid)
		case <-time.After(time.Second):
			t.Fatal("blocked read starved another session's TTL refresh")
		}
	}
}

func TestShellJobSubmitHonorsCancellationWhileWaitingForSessionLock(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "deadline"}[deadline], func(t *testing.T) {
			svc, _, index, bg := newRemoteServiceFixture(t)
			ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
			locked, release := make(chan struct{}), make(chan struct{})
			lockDone := make(chan error, 1)
			go func() {
				lockDone <- index.WithSessionLock(context.Background(), ref, func(context.Context) error {
					close(locked)
					<-release
					return nil
				})
			}()
			<-locked
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
				want = context.DeadlineExceeded
			}
			done := make(chan error, 1)
			t.Cleanup(func() {
				cancel()
				close(release)
				require.NoError(t, <-lockDone)
			})
			go func() {
				_, err := svc.SubmitShellJob(ctx, ref, tools.ShellJobSubmitRequest{Command: "true"})
				done <- err
			}()
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, want)
			case <-time.After(time.Second):
				t.Fatal("cancelled submission is still waiting for the session lock")
			}
			live, err := bg.Registry.List(t.Context(), ref)
			require.NoError(t, err)
			require.Empty(t, live, "a cancelled waiter must not launch a job")
		})
	}
}

func TestShellJobSubmitCancellationAfterLaunchPreservesBackgroundWork(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		cancel()
		return &sandbox.ExecuteResult{Error: "context canceled after launch"}, nil
	}
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	_, err := svc.SubmitShellJob(ctx, ref, tools.ShellJobSubmitRequest{Command: "sleep 60"})
	require.True(t, err == nil || errors.Is(err, context.Canceled))
	live, err := bg.Registry.List(t.Context(), ref)
	require.NoError(t, err)
	require.Len(t, live, 1)
	entries, err := index.Entries(t.Context())
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestShellJobRemoteKeepsStdinOutOfMetadataAndMasksDisplayCommand(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 65536)))
	svc.remoteExec = func(_ context.Context, _ string, script string, _ sandbox.ShellExecOptions) (
		*sandbox.ExecuteResult, error,
	) {
		require.Equal(t, 1, strings.Count(script, encoded), "stdin belongs only in the executable wrapper")
		require.Equal(t, 1, strings.Count(script, "private-token"), "metadata must mask the credential")
		marker := shellJobMarkerInScript.FindString(script)
		return &sandbox.ExecuteResult{Stdout: marker + "\n" + `{"state":"succeeded","exit_code":0}` + "\n0 0"}, nil
	}
	sub, err := svc.SubmitShellJob(t.Context(), interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		tools.ShellJobSubmitRequest{
			Command:        "printf %s " + encoded + " | base64 -d | TOKEN=private-token cat",
			DisplayCommand: "TOKEN=private-token cat", YieldTime: time.Second,
		})
	require.NoError(t, err)
	require.Equal(t, "TOKEN=*** cat", sub.Result.Payload.(ShellJobPayload).Command)
}
