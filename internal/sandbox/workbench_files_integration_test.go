//go:build workbench_integration

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Opt in with -tags=workbench_integration and dedicated WORKBENCH_TEST_* settings.
// Docker and E2B are selected independently; either may run on a remote host.
func TestWorkbenchFilesIntegration(t *testing.T) {
	for _, backend := range []SandboxType{SandboxTypeDocker, SandboxTypeE2B} {
		t.Run(string(backend), func(t *testing.T) {
			cfg := workbenchRealBackendConfig(t, backend)
			client := newWorkbenchIntegrationClient(t, cfg)
			ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), 777), 5*time.Minute)
			defer cancel()
			require.NoError(t, client.Health(ctx))
			manager, err := NewSessionBoundManager(SessionBoundManagerConfig{
				Config: cfg, Client: client, Store: NewMemorySessionSandboxBindingStore(),
				Checker:  PermissiveSessionExistenceChecker{},
				ConfigID: "workbench-files-integration", SkipHealthProbe: true,
			})
			require.NoError(t, err)
			session := fmt.Sprintf("weknora-wb-files-%s-%d", backend, time.Now().UnixNano())
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(
					types.WithSandboxTenantID(context.Background(), 777), time.Minute,
				)
				defer cleanupCancel()
				require.NoError(t, manager.DestroySession(cleanupCtx, session))
			})
			_, err = manager.WorkbenchFiles(ctx, session, WorkbenchFileRequest{Operation: "list"})
			require.ErrorIs(t, err, ErrWorkbenchUnavailable)
			handle, err := manager.resolveSession(ctx, session)
			require.NoError(t, err)
			run := func(req WorkbenchFileRequest) *WorkbenchFileResult {
				t.Helper()
				result, err := manager.WorkbenchFiles(ctx, session, req)
				require.NoError(t, err, "operation=%s path=%s", req.Operation, req.Path)
				return result
			}
			fails := func(req WorkbenchFileRequest, want error) {
				t.Helper()
				_, err := manager.WorkbenchFiles(ctx, session, req)
				require.ErrorIs(t, err, want, "operation=%s path=%s", req.Operation, req.Path)
			}
			run(WorkbenchFileRequest{Operation: "list"})
			run(WorkbenchFileRequest{Operation: "mkdir", Path: "files"})
			name := "files/WEKNORA_STDIN_EOF';$(touch pwned).bin"
			run(WorkbenchFileRequest{Operation: "write", Path: name, Content: []byte{0, 1, 255}})
			require.Equal(t, []byte{0, 1, 255}, run(WorkbenchFileRequest{Operation: "read", Path: name}).Content)
			fails(WorkbenchFileRequest{Operation: "write", Path: name}, ErrWorkbenchConflict)
			run(WorkbenchFileRequest{Operation: "rename", Path: name, NewPath: "files/renamed.bin"})
			fails(WorkbenchFileRequest{Operation: "remove", Path: "files"}, ErrWorkbenchConflict)
			run(WorkbenchFileRequest{Operation: "rename", Path: "files", NewPath: "moved"})
			run(WorkbenchFileRequest{Operation: "remove", Path: "moved/renamed.bin"})
			run(WorkbenchFileRequest{Operation: "remove", Path: "moved"})
			fails(WorkbenchFileRequest{Operation: "read", Path: "missing"}, ErrWorkbenchNotFound)
			fails(WorkbenchFileRequest{Operation: "read", Path: "../input"}, ErrWorkbenchPath)

			t.Log("round-tripping an 8 MiB upload through bounded helper calls")
			content := bytes.Repeat([]byte{0, 255, 2, 3}, WorkbenchMaxFileBytes/4)
			run(WorkbenchFileRequest{Operation: "write", Path: "large.bin", Content: content})
			require.Equal(t, content, run(WorkbenchFileRequest{Operation: "read", Path: "large.bin"}).Content)
			fails(WorkbenchFileRequest{
				Operation: "write", Path: "over.bin", Content: append(content, 0),
			}, ErrWorkbenchTooLarge)
			run(WorkbenchFileRequest{Operation: "remove", Path: "large.bin"})
			t.Log("8 MiB write/read passed; exercising hostile filesystem fixtures")

			fixture, err := client.Exec(ctx, handle, RemoteExecRequest{
				Command: "python3", Args: []string{"-I", "-c", `
import os, socket
from pathlib import Path
root = Path('/workspace/output')
outside = Path('/workspace/workbench-files-outside')
outside.mkdir()
(outside / 'secret').write_bytes(b'outside-secret')
os.symlink(outside, root / 'link')
os.link(outside / 'secret', root / 'hardlink')
os.mkfifo(root / 'fifo')
sock = socket.socket(socket.AF_UNIX)
sock.bind(str(root / 'socket'))
sock.close()
with (root / 'huge').open('wb') as f:
    f.truncate(1 << 40)
(root / 'many').mkdir()
for i in range(501):
    (root / 'many' / str(i)).touch()
print('fixtures-ready')
`}, User: DefaultSandboxExecUser, WorkDir: "/", Timeout: 15 * time.Second,
			})
			require.NoError(t, err)
			require.Equal(t, 0, fixture.ExitCode, "%s", fixture.Stderr)
			for _, path := range []string{"link/secret", "hardlink", "fifo", "socket"} {
				for _, op := range []string{"read", "write", "remove"} {
					fails(WorkbenchFileRequest{Operation: op, Path: path}, ErrWorkbenchPath)
				}
			}
			fails(WorkbenchFileRequest{Operation: "read", Path: "huge"}, ErrWorkbenchTooLarge)
			fails(WorkbenchFileRequest{Operation: "list", Path: "many"}, ErrWorkbenchTooLarge)
			fails(WorkbenchFileRequest{Operation: "rename", Path: "huge", NewPath: "link/secret"}, ErrWorkbenchPath)
			check, err := client.Exec(ctx, handle, RemoteExecRequest{
				Command: "python3", Args: []string{"-I", "-c", `
from pathlib import Path
assert Path('/workspace/workbench-files-outside/secret').read_bytes() == b'outside-secret'
assert not Path('/pwned').exists()
assert not list(Path('/workspace/output').glob('.weknora-workbench-*'))
`}, Timeout: 15 * time.Second, WorkDir: "/", User: DefaultSandboxExecUser,
			})
			require.NoError(t, err)
			require.Equal(t, 0, check.ExitCode, "%s", check.Stderr)
		})
	}
}
