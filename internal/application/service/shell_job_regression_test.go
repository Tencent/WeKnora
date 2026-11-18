package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestShellJobReadFailureMustNotHideCompletion(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	id := "sj_0123"
	reader.errs = map[string]error{id: errors.New("sandbox exec timed out")}
	marks := background.NewRunNotices(ref, bg.Registry, bg.Inbox)
	wait := tools.NewWorkWaitTool(svc, ref, "m1", marks, nil, []string{"shell_exec_async"})
	result, err := wait.Execute(ctx, json.RawMessage("{\"work_ids\":[\"sj_0123\"],\"wait_sec\":0}"))
	require.ErrorContains(t, err, "sandbox exec timed out")
	require.Nil(t, result)
	require.Empty(t, marks.TakeSeen())
	end := time.Now().UTC()
	reader.errs = nil
	reader.snapshots[id] = sandbox.ShellJobSnapshot{
		Meta:   sandbox.ShellJobMeta{ID: id, TenantID: 7, SessionID: "s", SandboxID: "sbx1", Title: "build"},
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateSucceeded, EndedAt: &end},
	}
	require.NoError(
		t,
		index.Add(
			ctx,
			ShellJobIndexEntry{SessionRef: ref, WorkID: id, SandboxID: "sbx1", YieldUntil: end.Add(-time.Minute)},
		),
	)
	require.NoError(t, svc.SweepOnce(ctx))
	delivery, err := marks.RoundStart(ctx)
	require.NoError(t, err)
	require.Contains(t, delivery.Text, id, "a transient read must not suppress the real completion")
}

func TestShellJobForeignStatusMustNotReturnMetadata(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta: sandbox.ShellJobMeta{
			ID:        "sj_0123",
			TenantID:  7,
			SessionID: "other-session",
			SandboxID: "sbx1",
			Command:   "private-command",
			WorkDir:   "/workspace/private",
			Title:     "private-title",
		},
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
	}
	got, err := svc.Status(
		context.Background(),
		interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		[]string{"sj_0123"},
		interfaces.StatusOptions{Detailed: true},
	)
	require.NoError(t, err)
	p := got[0].Payload.(ShellJobPayload)
	t.Logf("foreign response: title=%s payload=%+v", got[0].Title, p)
	require.Empty(t, p.Command)
	require.Empty(t, p.SessionID)
	require.Empty(t, p.WorkDir)
	require.NotContains(t, got[0].Title, "private")
}

func TestShellJobForeignCancelMustCheckOwnership(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	reader.snapshots["sj_0123"] = sandbox.ShellJobSnapshot{
		Meta:   sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "other-session", SandboxID: "sbx1"},
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
	}
	signalled := false
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		cmd string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		if strings.Contains(cmd, "kill -TERM") {
			signalled = true
		}
		return &sandbox.ExecuteResult{Stdout: "{\"state\":\"cancelled\"}"}, nil
	}
	_, err := svc.Cancel(context.Background(), interfaces.SessionRef{TenantID: 7, SessionID: "s"}, []string{"sj_0123"})
	require.ErrorIs(t, err, interfaces.ErrWorkNotFound)
	require.False(t, signalled, "another session's id reached the signal command")
}

func TestShellJobRemoteOutputBudget(t *testing.T) {
	svc, _, _, bg := newRemoteServiceFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	root := t.TempDir()
	ids := make([]string, 16)
	for i := range ids {
		ids[i] = fmt.Sprintf("sj_%04x", i)
		dir := filepath.Join(root, ids[i])
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(
			t,
			sandbox.WriteShellJobJSON(
				filepath.Join(dir, "meta.json"),
				sandbox.ShellJobMeta{ID: ids[i], TenantID: 7, SessionID: "s", SandboxID: "sbx1"},
			),
		)
		require.NoError(
			t,
			sandbox.WriteShellJobJSON(
				filepath.Join(dir, "status.json"),
				sandbox.ShellJobStatus{State: interfaces.WorkStateSucceeded},
			),
		)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout"), []byte(strings.Repeat("\x01", 16*1024)), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "stderr"), []byte(strings.Repeat("\x02", 8*1024)), 0o600))
	}
	svc.remoteRead = svc.batchReadViaPinned
	svc.remoteExec = func(
		ctx context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		cmd := exec.CommandContext(ctx, "sh")
		cmd.Stdin = strings.NewReader(strings.ReplaceAll(script, sandbox.ShellJobsRoot, root))
		out, err := cmd.Output()
		return &sandbox.ExecuteResult{Stdout: string(out)}, err
	}
	results, err := svc.Status(ctx, ref, ids, interfaces.StatusOptions{Detailed: true, OutputBudget: 32 * 1024 / 16})
	require.NoError(t, err)
	total := 0
	for _, r := range results {
		p := r.Payload.(ShellJobPayload)
		total += len(p.Stdout) + len(p.Stderr)
	}
	t.Logf("requested total budget 32768; actual body bytes=%d", total)
	if total > 32*1024 {
		t.Errorf("remote results ignore the caller's output budget: %d", total)
	}
	registry := tools.NewToolRegistry()
	marks := background.NewRunNotices(ref, bg.Registry, bg.Inbox)
	registry.RegisterTool(tools.NewWorkWaitTool(svc, ref, "m2", marks, nil, []string{"shell_exec_async"}))
	args, _ := json.Marshal(map[string]any{"work_ids": ids, "wait_sec": 0})
	rendered, err := registry.ExecuteTool(ctx, "work_wait", args)
	require.NoError(t, err)
	t.Logf(
		"rendered bytes=%d validJSON=%v acknowledged=%d",
		len(rendered.Output),
		json.Valid([]byte(rendered.Output)),
		len(marks.TakeSeen()),
	)
	require.True(t, json.Valid([]byte(rendered.Output)), "the actual tool registry cuts the batched JSON")
}

func TestShellJobInlineResultMustPreserveTail(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		marker := shellJobMarkerInScript.FindString(script)
		text := strings.Repeat("x", 20000) + "FINAL-DIAGNOSTIC\n"
		out := text + marker + "\n" + "{\"state\":\"failed\",\"exit_code\":1}" + fmt.Sprintf("\n%d 0\n", len(text))
		return &sandbox.ExecuteResult{Stdout: out}, nil
	}
	result, err := svc.SubmitShellJob(
		context.Background(),
		interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		tools.ShellJobSubmitRequest{Command: "build", YieldTime: time.Second},
	)
	require.NoError(t, err)
	p := result.Result.Payload.(ShellJobPayload)
	t.Logf(
		"stdout=%d total=%d truncated=%v omitted=%d",
		len(p.Stdout),
		p.TotalBytes.Stdout,
		p.Truncated,
		p.OmittedBytes.Stdout,
	)
	require.Contains(t, p.Stdout, "FINAL-DIAGNOSTIC")
	require.True(t, p.Truncated)
	require.Equal(t, p.TotalBytes.Stdout-int64(len(p.Stdout)), p.OmittedBytes.Stdout)
}

func TestShellJobDeleteMustStopRemoteWork(t *testing.T) {
	svc, reader, index, bg := newRemoteServiceFixture(t)
	reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", "s", "sbx1")
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(
		t,
		index.Add(
			ctx,
			ShellJobIndexEntry{SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: time.Now()},
		),
	)
	require.NoError(
		t,
		bg.Registry.Add(ctx, ref, interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell"}, time.Minute, 0),
	)
	cancelled := false
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		if strings.Contains(script, "kill") {
			cancelled = true
		}
		return &sandbox.ExecuteResult{Stdout: `{"state":"cancelled"}`}, nil
	}
	require.NoError(t, svc.OnDelete(ctx, ref))
	due, err := index.Due(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Empty(t, due)
	require.True(t, cancelled, "removing an index entry does not kill a detached command on a retained shared machine")
}

func TestShellJobReplacedSandboxMustStopRenewing(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ctx := context.Background()
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	require.NoError(
		t,
		index.Add(
			ctx,
			ShellJobIndexEntry{
				SessionRef: ref,
				WorkID:     "sj_0123",
				SandboxID:  "old",
				YieldUntil: time.Now().Add(-time.Minute),
			},
		),
	)
	svc.remoteRead = svc.batchReadViaPinned
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		_ string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		require.Equal(t, "old", opts.ExpectedSandboxID)
		return nil, fmt.Errorf("maintenance: %w", sandbox.ErrBoundSandboxChanged)
	}
	require.NoError(t, svc.SweepOnce(ctx))
	require.NoError(t, svc.SweepOnce(ctx))
	pending, err := bg.Inbox.Pending(ctx, ref)
	require.NoError(t, err)
	live, err := bg.Registry.List(ctx, ref)
	require.NoError(t, err)
	t.Logf("permanently replaced sandbox: live=%d notices=%d", len(live), len(pending))
	require.Len(t, pending, 1, "a confirmed binding change cannot recover on a subsequent retry")
}

func TestShellJobRotatedIncrementalReadMustNotSkipUnreadTail(t *testing.T) {
	svc, _, index, _ := newRemoteServiceFixture(t)
	ctx := context.WithValue(context.Background(), shellReadRunKey{}, "m1")
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	root := t.TempDir()
	dir := filepath.Join(root, "sj_0123")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(
		t,
		sandbox.WriteShellJobJSON(
			filepath.Join(dir, "meta.json"),
			sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", SandboxID: "sbx1"},
		),
	)
	require.NoError(
		t,
		sandbox.WriteShellJobJSON(
			filepath.Join(dir, "status.json"),
			sandbox.ShellJobStatus{State: interfaces.WorkStateRunning},
		),
	)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.1"), []byte(strings.Repeat("x", 20000)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout"), []byte("FINAL-DIAGNOSTIC"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "stdout.rotated"), []byte("65536"), 0o600))
	require.NoError(t, index.PutCursor(ctx, ref, "m1", "sj_0123", sandbox.ShellJobOffsets{Stdout: 1}, time.Hour))
	svc.remoteRead = svc.batchReadViaPinned
	svc.remoteExec = func(
		ctx context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		cmd := exec.CommandContext(ctx, "sh")
		cmd.Stdin = strings.NewReader(strings.ReplaceAll(script, sandbox.ShellJobsRoot, root))
		out, err := cmd.Output()
		return &sandbox.ExecuteResult{Stdout: string(out)}, err
	}
	first, err := svc.Status(ctx, ref, []string{"sj_0123"}, interfaces.StatusOptions{Detailed: true, RunID: "m1"})
	require.NoError(t, err)
	p := first[0].Payload.(ShellJobPayload)
	cursor, _, err := index.GetCursor(ctx, ref, "m1", "sj_0123")
	require.NoError(t, err)
	t.Logf(
		"window from=%d len=%d omitted=%d total=%d nextCursor=%d",
		p.FromBytes.Stdout,
		len(p.Stdout),
		p.OmittedBytes.Stdout,
		p.TotalBytes.Stdout,
		cursor.Stdout,
	)
	require.Equal(
		t,
		p.FromBytes.Stdout+int64(len(p.Stdout)),
		cursor.Stdout,
		"the cursor skips remaining bytes still present on disk",
	)
	second, err := svc.Status(ctx, ref, []string{"sj_0123"}, interfaces.StatusOptions{Detailed: true, RunID: "m1"})
	require.NoError(t, err)
	require.Contains(t, second[0].Payload.(ShellJobPayload).Stdout, "FINAL-DIAGNOSTIC")
}

func ownedRunningShellSnapshot(id, session, box string) sandbox.ShellJobSnapshot {
	return sandbox.ShellJobSnapshot{
		Meta:   sandbox.ShellJobMeta{ID: id, TenantID: 7, SessionID: session, SandboxID: box, Title: "original job"},
		Status: sandbox.ShellJobStatus{State: interfaces.WorkStateRunning, StartedAt: time.Now()}, WatcherAlive: true,
	}
}

type shellLeaseClock struct{ nanos atomic.Int64 }

func (c *shellLeaseClock) Now() time.Time { return time.Unix(0, c.nanos.Load()) }

type observedShellRegistry struct {
	interfaces.ActivityRegistry
	renewed chan string
}

func (r observedShellRegistry) Renew(
	ctx context.Context,
	ref interfaces.SessionRef,
	id string,
	lease time.Duration,
) (bool, error) {
	found, err := r.ActivityRegistry.Renew(ctx, ref, id, lease)
	select {
	case r.renewed <- id:
	default:
	}
	return found, err
}

func TestShellJobLeaseSurvivesBlockedReadAndQueuedSessions(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	clock := &shellLeaseClock{}
	clock.nanos.Store(time.Now().UnixNano())
	observed := observedShellRegistry{
		ActivityRegistry: background.NewMemoryActivityRegistry(clock),
		renewed:          make(chan string, 100),
	}
	bg.Registry = observed
	svc.lifecycle.Registry = observed
	svc.heartbeatInterval = 5 * time.Millisecond
	refs := []interfaces.SessionRef{{TenantID: 7, SessionID: "first"}, {TenantID: 7, SessionID: "queued"}}
	for i, ref := range refs {
		id := fmt.Sprintf("sj_%04x", i)
		require.NoError(
			t,
			observed.Add(t.Context(), ref, interfaces.BackgroundActivity{ID: id, Kind: "shell"}, shellJobRenewLease, 0),
		)
		require.NoError(
			t,
			index.Add(
				t.Context(),
				ShellJobIndexEntry{
					SessionRef: ref,
					WorkID:     id,
					SandboxID:  "sbx1",
					YieldUntil: time.Now().Add(-time.Minute),
				},
			),
		)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	svc.remoteRead = func(
		_ context.Context,
		ref interfaces.SessionRef,
		ids []string,
		_ map[string]string,
	) ([]sandbox.ShellJobSnapshot, map[string]error) {
		entered <- struct{}{}
		<-release
		return []sandbox.ShellJobSnapshot{ownedRunningShellSnapshot(ids[0], ref.SessionID, "sbx1")}, nil
	}
	done := make(chan error, 1)
	go func() { done <- svc.SweepOnce(t.Context()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep never entered remote read")
	}
	for round := 0; round < 5; round++ {
		for len(observed.renewed) > 0 {
			<-observed.renewed
		}
		clock.nanos.Add(int64(10 * time.Second))
		seen := map[string]bool{}
		deadline := time.NewTimer(time.Second)
		for len(seen) < 2 {
			select {
			case id := <-observed.renewed:
				seen[id] = true
			case <-deadline.C:
				t.Fatal("blocked read starved activity renewal")
			}
		}
		deadline.Stop()
		for _, ref := range refs {
			live, err := observed.List(t.Context(), ref)
			require.NoError(t, err)
			require.Len(t, live, 1, "a queued session must remain protected too")
		}
	}
	releaseOnce.Do(func() { close(release) })
	require.NoError(t, <-done)
}

func TestShellJobIDCollisionPreservesExistingRegistrationAndIndex(t *testing.T) {
	svc, _, index, bg := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	original := ShellJobIndexEntry{
		SessionRef: ref,
		WorkID:     "sj_0123",
		SandboxID:  "old-box",
		YieldUntil: time.Now().UTC().Add(-time.Minute).Truncate(time.Second),
	}
	require.NoError(t, index.Add(t.Context(), original))
	require.NoError(
		t,
		bg.Registry.Add(
			t.Context(),
			ref,
			interfaces.BackgroundActivity{ID: original.WorkID, Kind: "shell", Title: "original"},
			time.Hour,
			0,
		),
	)
	ids := []string{original.WorkID, "sj_f00d"}
	svc.newJobID = func() (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	var executed []string
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		require.NotContains(t, script, original.WorkID)
		executed = append(executed, script)
		return &sandbox.ExecuteResult{Error: "connection interrupted after launch"}, nil
	}
	got, err := svc.SubmitShellJob(
		t.Context(),
		ref,
		tools.ShellJobSubmitRequest{Command: "sleep 60", YieldTime: time.Second},
	)
	require.NoError(t, err)
	require.Equal(t, "sj_f00d", got.Result.ID)
	require.Len(t, executed, 1)
	entries, err := index.Entries(t.Context())
	require.NoError(t, err)
	require.Contains(t, entries, original)
	live, err := bg.Registry.List(t.Context(), ref)
	require.NoError(t, err)
	require.Len(t, live, 2)
	for _, a := range live {
		if a.ID == original.WorkID {
			require.Equal(t, "original", a.Title)
		}
	}
	// A second replica must also fail to reserve the newly launched handle.
	other := NewShellJobIndex(index.client, index.namespace)
	reserved, err := other.ReserveID(t.Context(), ref, "sj_f00d")
	require.NoError(t, err)
	require.False(t, reserved)
}

func TestShellJobMalformedReadDoesNotConsumeCursors(t *testing.T) {
	svc, _, index, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	svc.remoteRead = svc.batchReadViaPinned
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		return &sandbox.ExecuteResult{Stdout: `{"meta":{"work_id":"sj_0123"},"status":`}, nil
	}
	_, err := svc.Status(t.Context(), ref, []string{"sj_0123"}, interfaces.StatusOptions{Detailed: true, RunID: "run"})
	require.ErrorContains(t, err, "unreadable")
	_, found, err := index.GetCursor(t.Context(), ref, "run", "sj_0123")
	require.NoError(t, err)
	require.False(t, found)
}

func TestShellJobInlineResultReadsFinalTailAfterForwardingCap(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	svc.newJobID = func() (string, error) { return "sj_f004", nil }
	snap := ownedRunningShellSnapshot("sj_f004", "s", "sbx1")
	snap.Status.State = interfaces.WorkStateFailed
	snap.Stdout = sandbox.ShellJobStream{
		Text:         "HEAD\nFINAL-DIAGNOSTIC",
		TotalBytes:   2 * 1024 * 1024,
		Truncated:    true,
		OmittedBytes: 2*1024*1024 - 21,
	}
	reader.snapshots[snap.Meta.ID] = snap
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		script string,
		_ sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		marker := shellJobMarkerInScript.FindString(script)
		return &sandbox.ExecuteResult{
			Stdout: strings.Repeat(
				"x",
				1024*1024,
			) + marker + "\n" + `{"state":"failed","exit_code":1}` + "\n2097152 0\n",
		}, nil
	}
	result, err := svc.SubmitShellJob(
		t.Context(),
		interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		tools.ShellJobSubmitRequest{Command: "build", YieldTime: time.Second},
	)
	require.NoError(t, err)
	require.False(t, result.Background)
	require.Contains(t, result.Result.Payload.(ShellJobPayload).Stdout, "FINAL-DIAGNOSTIC")
	require.Equal(t, 1, reader.execCalls)
}

func TestShellJobSharedMachineDeleteStopsBeforeRemovingSession(t *testing.T) {
	for _, operation := range []string{"single", "batch", "all"} {
		t.Run(operation, func(t *testing.T) {
			f := newUserDeleteFixture(t, "user", nil)
			f.place(t, userDeleteSessionID)
			svc, reader, index, bg := newRemoteServiceFixture(t)
			f.svc.background = bg
			ref := interfaces.SessionRef{TenantID: 7, SessionID: userDeleteSessionID}
			reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", userDeleteSessionID, "sbx1")
			require.NoError(
				t,
				index.Add(
					t.Context(),
					ShellJobIndexEntry{SessionRef: ref, WorkID: "sj_0123", SandboxID: "sbx1", YieldUntil: time.Now()},
				),
			)
			require.NoError(
				t,
				bg.Registry.Add(
					t.Context(),
					ref,
					interfaces.BackgroundActivity{ID: "sj_0123", Kind: "shell"},
					time.Minute,
					0,
				),
			)
			unavailable := true
			calls := 0
			svc.remoteExec = func(
				ctx context.Context,
				_ string,
				script string,
				opts sandbox.ShellExecOptions,
			) (*sandbox.ExecuteResult, error) {
				calls++
				_, err := f.svc.sessionRepo.GetByID(ctx, 7, userDeleteSessionID)
				require.NoError(t, err, "the session pin must still be readable when stopping")
				require.Equal(t, "sbx1", opts.ExpectedSandboxID)
				require.Contains(t, script, "kill -TERM")
				if unavailable {
					return nil, errors.New("maintenance unavailable")
				}
				return &sandbox.ExecuteResult{Stdout: `{"state":"cancelled"}`}, nil
			}
			ctx := testSessionScopeContext(7, "u-1")
			remove := func() error {
				switch operation {
				case "batch":
					return f.svc.BatchDeleteSessions(ctx, []string{userDeleteSessionID})
				case "all":
					return f.svc.DeleteAllSessions(ctx)
				default:
					return f.svc.DeleteSession(ctx, userDeleteSessionID)
				}
			}
			require.ErrorContains(t, remove(), "maintenance unavailable")
			_, err := f.svc.sessionRepo.GetByID(ctx, 7, userDeleteSessionID)
			require.NoError(t, err)
			live, err := bg.Registry.List(ctx, ref)
			require.NoError(t, err)
			require.Len(t, live, 1)
			unavailable = false
			require.NoError(t, remove())
			require.Equal(t, 2, calls)
			require.Empty(t, f.ops.destroyedIDs(), "the shared machine must remain")
			entries, err := index.Entries(ctx)
			require.NoError(t, err)
			require.Empty(t, entries)
			_, err = svc.SubmitShellJob(ctx, ref, tools.ShellJobSubmitRequest{Command: "sleep 60"})
			require.ErrorIs(t, err, interfaces.ErrWorkNotFound)
		})
	}
}

func TestShellJobCancelUsesTheJobsIndexedSandbox(t *testing.T) {
	svc, reader, index, _ := newRemoteServiceFixture(t)
	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	reader.snapshots["sj_0123"] = ownedRunningShellSnapshot("sj_0123", "s", "original-box")
	require.NoError(
		t,
		index.Add(
			t.Context(),
			ShellJobIndexEntry{SessionRef: ref, WorkID: "sj_0123", SandboxID: "original-box", YieldUntil: time.Now()},
		),
	)
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "replacement-box", true }
	calls := 0
	svc.remoteExec = func(
		_ context.Context,
		_ string,
		_ string,
		opts sandbox.ShellExecOptions,
	) (*sandbox.ExecuteResult, error) {
		calls++
		require.Equal(t, "original-box", opts.ExpectedSandboxID)
		return nil, sandbox.ErrBoundSandboxChanged
	}
	_, err := svc.Cancel(t.Context(), ref, []string{"sj_0123"})
	require.ErrorIs(t, err, sandbox.ErrBoundSandboxChanged)
	require.Equal(t, 1, calls, "must never retry the signal on the replacement sandbox")
	require.Equal(t, "original-box", reader.calls[0].sandboxIDs["sj_0123"])
}

func TestShellJobForeignMetadataIsNotExposedOnReadFailure(t *testing.T) {
	svc, reader, _, _ := newRemoteServiceFixture(t)
	snap := ownedRunningShellSnapshot("sj_0123", "other-session", "sbx1")
	snap.Meta.Command = "private-command"
	snap.Meta.Title = "private-title"
	reader.snapshots[snap.Meta.ID] = snap
	for _, readErr := range []error{sandbox.ErrBoundSandboxChanged, errors.New("transport unavailable")} {
		reader.errs = map[string]error{snap.Meta.ID: readErr}
		results, err := svc.Status(
			t.Context(),
			interfaces.SessionRef{TenantID: 7, SessionID: "s"},
			[]string{snap.Meta.ID},
			interfaces.StatusOptions{Detailed: true},
		)
		if shellSandboxGone(readErr) {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
		encoded, err := json.Marshal(results)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "private")
		require.NotContains(t, string(encoded), "other-session")
	}
}

func TestShellJobBatchDeleteFailureReopensEarlierSessions(t *testing.T) {
	f := newUserDeleteFixture(t, "user", nil)
	f.place(t, userDeleteSessionID)
	otherID := "1f8fad5b-d9cb-469f-a165-70867728950e"
	f.place(t, otherID)
	svc, reader, index, bg := newRemoteServiceFixture(t)
	f.svc.background = bg
	for i, sid := range []string{userDeleteSessionID, otherID} {
		id := fmt.Sprintf("sj_%04x", i)
		reader.snapshots[id] = ownedRunningShellSnapshot(id, sid, "sbx1")
		require.NoError(
			t,
			index.Add(
				t.Context(),
				ShellJobIndexEntry{
					SessionRef: interfaces.SessionRef{TenantID: 7, SessionID: sid},
					WorkID:     id,
					SandboxID:  "sbx1",
					YieldUntil: time.Now(),
				},
			),
		)
	}
	calls := 0
	svc.remoteExec = func(context.Context, string, string, sandbox.ShellExecOptions) (*sandbox.ExecuteResult, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("stop failed")
		}
		return &sandbox.ExecuteResult{Stdout: `{"state":"cancelled"}`}, nil
	}
	ctx := testSessionScopeContext(7, "u-1")
	require.ErrorContains(t, f.svc.BatchDeleteSessions(ctx, []string{userDeleteSessionID, otherID}), "stop failed")
	require.Equal(t, 2, calls)
	for _, sid := range []string{userDeleteSessionID, otherID} {
		closed, err := index.SessionClosed(ctx, interfaces.SessionRef{TenantID: 7, SessionID: sid})
		require.NoError(t, err)
		require.False(t, closed)
		_, err = f.svc.sessionRepo.GetByID(ctx, 7, sid)
		require.NoError(t, err)
	}
}

func TestShellJobUnavailableBindingDoesNotConfirmLoss(t *testing.T) {
	svc, _, _, _ := newRemoteServiceFixture(t)
	svc.remoteRead = svc.batchReadViaPinned
	svc.boundSandbox = func(context.Context, string) (string, bool) { return "", false }
	results, err := svc.Status(
		t.Context(),
		interfaces.SessionRef{TenantID: 7, SessionID: "s"},
		[]string{"sj_0123"},
		interfaces.StatusOptions{Detailed: true},
	)
	require.ErrorContains(t, err, "no bound sandbox")
	require.Empty(t, results)
}
