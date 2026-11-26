package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Exercise the tool boundary with a real command and file; only the job
// scheduler is replaced so completion inside the yield window is deterministic.
type inlineCacheShell struct {
	fakeShellExecutor
	root  string
	reads int
}

func (s *inlineCacheShell) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	return sandbox.WorkspaceLayout{
		Origin: sandbox.WorkspaceOriginHost, Root: s.root,
		ReadRoots: []string{s.root}, WriteRoots: []string{s.root},
	}, nil
}

func (s *inlineCacheShell) ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error) {
	return nil, nil
}

func (s *inlineCacheShell) StatSessionFile(_ context.Context, _, filePath string) (*sandbox.RemoteStatEntry, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}
	return &sandbox.RemoteStatEntry{
		Path: filePath, Type: sandbox.RemoteEntryFile, Size: info.Size(), ModTime: info.ModTime(),
	}, nil
}

func (s *inlineCacheShell) ReadSessionFile(_ context.Context, _, filePath string) ([]byte, error) {
	s.reads++
	return os.ReadFile(filePath)
}

func (s *inlineCacheShell) SubmitShellJob(
	ctx context.Context, _ interfaces.SessionRef, req ShellJobSubmitRequest,
) (ShellJobSubmission, error) {
	cmd := exec.CommandContext(ctx, "/bin/bash", "-c", req.Command)
	cmd.Dir = req.WorkDir
	state := interfaces.WorkStateSucceeded
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return ShellJobSubmission{}, err
		}
		state = interfaces.WorkStateFailed
	}
	return ShellJobSubmission{Result: interfaces.WorkResult{ID: "sj_inline", State: state, Terminal: true}}, nil
}

func TestShellExecAsyncInlineResultInvalidatesReadCache(t *testing.T) {
	for _, exitCode := range []string{"0", "3"} {
		t.Run("exit_"+exitCode, func(t *testing.T) {
			root := t.TempDir()
			filePath := filepath.Join(root, "run.py")
			require.NoError(t, os.WriteFile(filePath, []byte("DEBUG = 1"), 0o600))
			mtime := time.Unix(1700000000, 0)
			require.NoError(t, os.Chtimes(filePath, mtime, mtime))
			shell := &inlineCacheShell{root: root}
			reader := NewReadFileTool(shell)
			args := json.RawMessage(`{"path":"run.py"}`)
			ctx := asyncTestCtx()
			first, err := reader.Execute(ctx, args)
			require.NoError(t, err)
			require.True(t, first.Success, first.Error)
			require.Contains(t, first.Output, "DEBUG = 1")
			_, err = reader.Execute(ctx, args)
			require.NoError(t, err)
			require.Equal(t, 1, shell.reads, "unchanged pages should use the cache")

			command, err := json.Marshal(ShellExecAsyncInput{
				Command: "printf 'DEBUG = 0' > run.py; exit " + exitCode,
			})
			require.NoError(t, err)
			result, err := NewShellExecAsyncTool(shell, shell, nil).Execute(ctx, command)
			require.NoError(t, err)
			require.True(t, result.Success, result.Error)
			// Reproduce a same-size edit within a backend's coarse mtime tick.
			require.NoError(t, os.Chtimes(filePath, mtime, mtime))
			actual, err := os.ReadFile(filePath)
			require.NoError(t, err)
			require.Equal(t, "DEBUG = 0", string(actual), "the real command must have changed the file")

			after, err := reader.Execute(ctx, args)
			require.NoError(t, err)
			require.True(t, after.Success, after.Error)
			require.Contains(t, after.Output, "DEBUG = 0")
			require.Equal(t, 2, shell.reads)
		})
	}
}
