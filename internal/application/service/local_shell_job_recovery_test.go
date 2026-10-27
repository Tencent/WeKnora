//go:build desktop

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func writeLocalJob(t *testing.T, root, id string, meta sandbox.ShellJobMeta,
	status sandbox.ShellJobStatus,
) string {
	t.Helper()
	dir := filepath.Join(root, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, sandbox.WriteShellJobJSON(filepath.Join(dir, "meta.json"), meta))
	require.NoError(t, sandbox.WriteShellJobJSON(filepath.Join(dir, "status.json"), status))
	return dir
}

func TestRecoveryMarksLostRepushesNoticePrunesOld(t *testing.T) {
	root := t.TempDir()
	recent := time.Now().UTC().Add(-time.Hour)
	old := time.Now().UTC().Add(-25 * time.Hour)
	meta := sandbox.ShellJobMeta{TenantID: 7, SessionID: "s", Title: "build"}
	running := meta
	running.ID = "sj_0123"
	writeLocalJob(t, root, "sj_0123", running, sandbox.ShellJobStatus{
		State: interfaces.WorkStateRunning, PGID: 999999, ProcStartedAt: 1,
		StartedAt: recent,
	})
	succeeded := meta
	succeeded.ID = "sj_ab01"
	ended := recent
	writeLocalJob(t, root, "sj_ab01", succeeded, sandbox.ShellJobStatus{
		State: interfaces.WorkStateSucceeded, StartedAt: recent, EndedAt: &ended,
	})
	ancient := meta
	ancient.ID = "sj_fedc"
	writeLocalJob(t, root, "sj_fedc", ancient, sandbox.ShellJobStatus{
		State: interfaces.WorkStateFailed, StartedAt: old, EndedAt: &old,
	})

	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := &LocalShellJobService{
		bg: bg, root: root, jobs: make(map[string]*localJob),
		cursors: make(map[string]localCursor), blocked: make(map[interfaces.SessionRef]bool),
	}
	require.NoError(t, svc.recoverJobs(context.Background()))

	ref := interfaces.SessionRef{TenantID: 7, SessionID: "s"}
	notices, err := bg.Inbox.Pending(context.Background(), ref)
	require.NoError(t, err)
	require.Len(t, notices, 2, "running→lost 与 24h 内的 succeeded 各补推一条；25h 前的直接清理")
	require.Contains(t, notices[0].Text, "lost")
	require.True(t, interfaces.IsTerminalWorkState(svc.jobs["sj_0123"].status.State))
	_, err = os.Stat(filepath.Join(root, "sj_fedc"))
	require.True(t, os.IsNotExist(err), "ended_at 超过 24h 的终态目录被删除（D5）")
}

func TestRecoverySkipsCorruptMetaAndContinues(t *testing.T) {
	root := t.TempDir()
	recent := time.Now().UTC().Add(-time.Hour)
	meta := sandbox.ShellJobMeta{ID: "sj_ab01", TenantID: 7, SessionID: "s", Title: "build"}
	ended := recent
	writeLocalJob(t, root, "sj_ab01", meta, sandbox.ShellJobStatus{
		State: interfaces.WorkStateSucceeded, StartedAt: recent, EndedAt: &ended,
	})
	bad := filepath.Join(root, "sj_bad1")
	require.NoError(t, os.MkdirAll(bad, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(bad, "meta.json"), []byte("{"), 0o600))

	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := &LocalShellJobService{
		bg: bg, root: root, jobs: make(map[string]*localJob),
	}
	require.NoError(t, svc.recoverJobs(context.Background()))
	require.Contains(t, svc.jobs, "sj_ab01")
	require.NotContains(t, svc.jobs, "sj_bad1")
	notices, err := bg.Inbox.Pending(context.Background(), interfaces.SessionRef{TenantID: 7, SessionID: "s"})
	require.NoError(t, err)
	require.Len(t, notices, 1)
}

func TestRecoveryContinuesWhenProcessSignalFails(t *testing.T) {
	root := t.TempDir()
	recent := time.Now().UTC()
	meta := sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s", Title: "build"}
	writeLocalJob(t, root, "sj_0123", meta, sandbox.ShellJobStatus{
		State: interfaces.WorkStateRunning, PGID: 4321, ProcStartedAt: recent.UnixNano(),
		StartedAt: recent,
	})
	orig := signalRecoveredProcess
	signalRecoveredProcess = func(context.Context, int, int64) error {
		return errors.New("process table busy")
	}
	t.Cleanup(func() { signalRecoveredProcess = orig })

	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := &LocalShellJobService{
		bg: bg, root: root, jobs: make(map[string]*localJob),
	}
	require.NoError(t, svc.recoverJobs(context.Background()))
	require.Equal(t, interfaces.WorkStateLost, svc.jobs["sj_0123"].status.State)
}

func TestRecoveryDoesNotTouchRunningWithoutPGID(t *testing.T) {
	root := t.TempDir()
	meta := sandbox.ShellJobMeta{ID: "sj_0123", TenantID: 7, SessionID: "s"}
	writeLocalJob(t, root, "sj_0123", meta, sandbox.ShellJobStatus{
		State: interfaces.WorkStateRunning, StartedAt: time.Now().UTC(),
	})
	bg := NewBackgroundWork(background.NewMemoryActivityRegistry(nil), background.NewMemoryNoticeInbox(nil))
	svc := &LocalShellJobService{bg: bg, root: root, jobs: make(map[string]*localJob)}
	require.NoError(t, svc.recoverJobs(context.Background()))
	require.Equal(t, interfaces.WorkStateLost, svc.jobs["sj_0123"].status.State)
}
