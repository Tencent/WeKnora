//go:build desktop

package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Layout falls back to the remote layout; the explicit working directory below
// selects the test workspace. The async tool never executes through this stub.
type shellJobToolExecutor struct{ tools.SandboxCommandExecutor }

func TestLocalShellJobLargeStdinPreservesResultJSON(t *testing.T) {
	for _, command := range []string{"cat", "tee /dev/stderr #" + strings.Repeat("\x01", 8192-17)} {
		t.Run(map[bool]string{false: "stdin", true: "escaped-command"}[len(command) > 3], func(t *testing.T) {
			root := t.TempDir()
			bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
			svc, err := newLocalShellJobService(drainingShellStarter{root: root}, bg, nil,
				filepath.Join(root, "jobs"), func(context.Context, int) (int64, error) { return 1, nil })
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, svc.Shutdown(context.Background())) })
			registry := tools.NewToolRegistry()
			registry.RegisterTool(tools.NewShellExecAsyncTool(svc, shellJobToolExecutor{}, nil))
			ctx := tools.WithToolExecContext(types.WithSandboxTenantID(t.Context(), 7),
				&tools.ToolExecContext{SessionID: "s"})
			input, err := json.Marshal(tools.ShellExecAsyncInput{
				Command: command, Stdin: strings.Repeat("\x01", 65536), WorkDir: root, Description: "copy input",
			})
			require.NoError(t, err)
			result, err := registry.ExecuteTool(ctx, tools.ToolShellExecAsync, input)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			require.True(t, json.Valid([]byte(result.Output)), "the registry truncated a valid large-stdin result")
			var payload ShellJobPayload
			require.NoError(t, json.Unmarshal([]byte(result.Output), &payload))
			require.Equal(t, interfaces.WorkStateSucceeded, payload.State)
			require.Equal(t, strings.Repeat("\x01", 16*1024), payload.Stdout)
			require.Equal(t, int64(65536), payload.TotalBytes.Stdout)
			require.Equal(t, command, payload.Command, "display the user's command, not the stdin transport")
			var meta sandbox.ShellJobMeta
			require.NoError(t, readLocalJobJSON(filepath.Join(svc.root, payload.WorkID, "meta.json"), &meta))
			require.Equal(t, command, meta.Command, "the stdin transport must not be persisted either")
			if command != "cat" {
				require.Equal(t, strings.Repeat("\x01", 8*1024), payload.Stderr)
			}
		})
	}
}

func TestLocalShellJobRecoveryReadsLargeLegacyMetadata(t *testing.T) {
	root := t.TempDir()
	ended := time.Now().UTC()
	command := "printf %s " + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 65536))) +
		" | base64 -d | cat"
	var signalled []int
	original := signalRecoveredProcess
	signalRecoveredProcess = func(_ context.Context, pid int, _ int64) error {
		signalled = append(signalled, pid)
		return nil
	}
	t.Cleanup(func() { signalRecoveredProcess = original })
	for i, state := range []string{interfaces.WorkStateRunning, interfaces.WorkStateSucceeded} {
		id := []string{"sj_b001", "sj_b002"}[i]
		dir := writeLocalJob(t, root, id, sandbox.ShellJobMeta{
			ID: id, TenantID: 7, SessionID: "s", Command: command,
		}, sandbox.ShellJobStatus{
			State: state, PGID: 4321, ProcStartedAt: 1, StartedAt: ended, EndedAt: &ended,
		})
		info, err := os.Stat(filepath.Join(dir, "meta.json"))
		require.NoError(t, err)
		require.Greater(t, info.Size(), int64(65536))
	}
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := &LocalShellJobService{root: root, bg: bg, jobs: map[string]*localJob{}}
	require.NoError(t, svc.recoverJobs(t.Context()))
	require.Len(t, svc.jobs, 2)
	require.Equal(t, []int{4321}, signalled, "the orphan must still pass through identity-checked cleanup")
	require.Equal(t, interfaces.WorkStateLost, svc.jobs["sj_b001"].status.State)
	notices, err := bg.Inbox.Pending(t.Context(), interfaces.SessionRef{TenantID: 7, SessionID: "s"})
	require.NoError(t, err)
	require.Len(t, notices, 2)
}

func TestLocalShellJobCaptureRegisteredAfterReportSettles(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(map[int]string{0: "success", 1: "failure"}[code], func(t *testing.T) {
			svc, proc, _ := localFixture(t)
			proc.exit.Code = code
			ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
			sub, err := svc.SubmitShellJob(t.Context(), ref, tools.ShellJobSubmitRequest{
				Command: "true", YieldTime: time.Millisecond,
			})
			require.NoError(t, err)
			require.True(t, sub.Background)
			job, err := svc.ownedJob(ref, sub.Result.ID)
			require.NoError(t, err)
			proc.finish()
			select {
			case <-job.reported:
			case <-time.After(time.Second):
				t.Fatal("watcher did not finish reporting")
			}
			var calls atomic.Int32
			svc.HoldSkillEnvCapture(ref, sub.Result.ID, func(context.Context) { calls.Add(1) })
			want := int32(0)
			if code == 0 {
				want = 1
			}
			require.Equal(t, want, calls.Load(), "a completed success must not lose the late write-back")
			require.Empty(t, svc.skillCapture.pending(), "terminal failures must drop late credentials too")
		})
	}
}
