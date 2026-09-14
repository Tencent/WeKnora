//go:build sandbox_terminal_integration

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Opt in with -tags=sandbox_terminal_integration and WORKBENCH_TEST_* settings.
// Each backend without dedicated configuration is skipped independently.
func TestTerminalRealBackends(t *testing.T) {
	for _, backend := range []SandboxType{SandboxTypeDocker, SandboxTypeE2B} {
		t.Run(string(backend), func(t *testing.T) {
			cfg := workbenchRealBackendConfig(t, backend)
			client := newWorkbenchIntegrationClient(t, cfg)
			store := NewMemorySessionSandboxBindingStore()
			mgr, err := NewSessionBoundManager(SessionBoundManagerConfig{
				Config: cfg, Client: client, Store: store, Checker: PermissiveSessionExistenceChecker{},
				ConfigID: "weknora-wb-terminal", SkipHealthProbe: true,
			})
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(types.WithSandboxTenantID(context.Background(), 918271), 8*time.Minute)
			defer cancel()
			session := fmt.Sprintf("weknora-wb-terminal-%s-%d", backend, time.Now().UnixNano())
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(
					types.WithSandboxTenantID(context.Background(), 918271), time.Minute,
				)
				defer cancel()
				require.NoError(t, mgr.DestroySession(cleanup, session))
			})
			provider, ok := CommandTerminalProviderFrom(mgr)
			require.True(t, ok)
			open := func(t *testing.T, parent context.Context, req CommandTerminalRequest) CommandTerminal {
				t.Helper()
				if req.Timeout == 0 {
					req.Timeout = 10 * time.Second
				}
				term, err := provider.OpenSessionCommandTerminal(parent, session, req)
				require.NoError(t, err)
				t.Cleanup(func() { _ = term.Close() })
				return term
			}
			exec := func(t *testing.T, command string) string {
				t.Helper()
				result, err := mgr.ExecShellCommand(ctx, session, command, SessionWorkspaceRoot, 10*time.Second, nil)
				require.NoError(t, err)
				require.True(t, result.IsSuccess(), "%+v", result)
				return result.Stdout
			}

			t.Run("ExplicitInitialization", func(t *testing.T) {
				require.NoError(t, mgr.EnsureWorkbenchSession(ctx, session))
				handle, ok, err := mgr.lookupSessionHandle(ctx, session)
				require.NoError(t, err)
				require.True(t, ok)
				for _, path := range []string{SessionInputRoot, SessionOutputRoot} {
					entry, err := client.Stat(ctx, handle, path)
					require.NoError(t, err)
					require.Equal(t, RemoteEntryDir, entry.Type)
				}
			})

			t.Run("StreamingStdinResize", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{
					Cols: 93, Rows: 37,
					Command: `stty -echo; stty size; printf 'READY\n'; read -r value; ` +
						`printf 'input:%s\n' "$value"; stty size; read -r finish; printf DONE`,
				})
				before := terminalReadUntil(t, term, "READY")
				require.Contains(t, before, "37 93")
				waitCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				_, err := term.Wait(waitCtx)
				cancel()
				require.ErrorIs(t, err, context.DeadlineExceeded, "stdout must arrive before completion")
				require.NoError(t, term.Resize(ctx, 111, 42))
				require.NoError(t, term.Input(ctx, []byte("hello\n")))
				output := terminalReadUntil(t, term, "42 111")
				require.Contains(t, output, "input:hello")
				require.NoError(t, term.Input(ctx, []byte("finish\n")))
				terminalReadUntil(t, term, "DONE")
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 0, exit.ExitCode)
			})

			t.Run("BinaryInputOutput", func(t *testing.T) {
				command := `stty raw -echo; python3 -c 'import os; os.write(1,b"READY"); data=b""` +
					"\n" + `while len(data)<256: data+=os.read(0,256-len(data))` + "\n" + `os.write(1,data)'`
				term := open(t, ctx, CommandTerminalRequest{Command: command})
				terminalReadUntil(t, term, "READY")
				payload := make([]byte, 256)
				for i := range payload {
					payload[i] = byte(i)
				}
				require.NoError(t, term.Input(ctx, payload))
				output, err := io.ReadAll(term)
				require.NoError(t, err)
				require.Equal(t, payload, output)
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 0, exit.ExitCode)
			})

			t.Run("Interrupt", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{Command: `printf READY; sleep 30`})
				terminalReadUntil(t, term, "READY")
				require.NoError(t, term.Interrupt(ctx))
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, "interrupted", exit.Reason)
			})

			descendant := `import os,time
if os.fork() == 0:
 os.setsid()
 if os.fork() == 0:
  open("/workspace/output/terminal-child.pid","w").write(str(os.getpid()))
  print("READY",flush=True)
  time.sleep(3)
  open("/workspace/output/terminal-late","w").write("leaked")
 os._exit(0)
time.sleep(30)`
			for _, mode := range []string{"timeout", "cancel", "close", "disconnect"} {
				t.Run("DescendantCleanup/"+mode, func(t *testing.T) {
					exec(t, "rm -f /workspace/output/terminal-child.pid /workspace/output/terminal-late")
					parent, cancel := context.WithCancel(ctx)
					defer cancel()
					timeout := 10 * time.Second
					if mode == "timeout" {
						timeout = 1200 * time.Millisecond
					}
					term := open(t, parent, CommandTerminalRequest{
						Command: "python3 -c " + ShellQuote(descendant), Timeout: timeout,
					})
					terminalReadUntil(t, term, "READY")
					if mode == "cancel" {
						cancel()
					}
					if mode == "close" {
						require.NoError(t, term.Close())
					}
					if mode == "disconnect" {
						term.(*sessionTerminal).CommandTerminal.(*commandTerminal).process.close()
					}
					exit, err := term.Wait(ctx)
					if mode == "disconnect" {
						require.Error(t, err)
					} else {
						require.NoError(t, err)
						reasons := map[string]string{"timeout": "timeout", "cancel": "canceled", "close": "closed"}
						require.Equal(t, reasons[mode], exit.Reason)
					}
					time.Sleep(3200 * time.Millisecond)
					exec(t, `test ! -e /workspace/output/terminal-late; `+
						`pid=$(cat /workspace/output/terminal-child.pid); test ! -e /proc/$pid`)
				})
			}

			t.Run("NormalExitCleansBackgroundChild", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{
					Command: `sleep 30 & echo $! > /workspace/output/terminal-background.pid; exit 7`,
				})
				_, err := io.ReadAll(term)
				require.NoError(t, err)
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 7, exit.ExitCode)
				exec(t, `pid=$(cat /workspace/output/terminal-background.pid); test ! -e /proc/$pid`)
			})

			t.Run("CPU", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{Command: `python3 -c 'while True: pass'`, CPUSeconds: 1})
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 152, exit.ExitCode)
				require.Equal(t, "cpu_limit", exit.Reason)
			})

			t.Run("AggregateDescendantCPU", func(t *testing.T) {
				code := "import os\nos.fork()\nwhile True: pass"
				term := open(t, ctx, CommandTerminalRequest{Command: "python3 -c " + ShellQuote(code), CPUSeconds: 1})
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, "cpu_limit", exit.Reason)
			})

			t.Run("AddressSpace", func(t *testing.T) {
				code := "import sys,resource\nprint(resource.getrlimit(resource.RLIMIT_AS),flush=True)\n" +
					"try: bytearray(128*1024*1024)\nexcept MemoryError: print('MEMORY_LIMIT',flush=True); sys.exit(73)"
				term := open(t, ctx, CommandTerminalRequest{
					Command: "python3 -c " + ShellQuote(code), MemoryBytes: 64 * 1024 * 1024,
				})
				output, err := io.ReadAll(term)
				require.NoError(t, err)
				require.Contains(t, string(output), "MEMORY_LIMIT")
				require.Contains(t, string(output), "(67108864, 67108864)")
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 73, exit.ExitCode)
			})

			t.Run("AggregateDescendantMemory", func(t *testing.T) {
				exec(t, "rm -f /workspace/output/terminal-memory-*.pid")
				code := `import os,resource,time
root = "/workspace/output/terminal-memory-"
limit = 96*1024*1024
assert resource.getrlimit(resource.RLIMIT_AS) == (limit, limit)
read_fd, write_fd = os.pipe()
for index in range(3):
 if os.fork() == 0:
  os.close(write_fd)
  if index == 2:
   os.setsid()
   if os.fork() != 0:
    os._exit(0)
  with open(root+str(index)+".pid", "w") as target:
   target.write(str(os.getpid()))
  os.read(read_fd, 1)
  data = bytearray(32*1024*1024)
  data[::4096] = b"x"*(len(data)//4096)
  with open("/proc/self/stat") as src:
   assert int(src.read().rsplit(")", 1)[1].split()[20]) < limit
  time.sleep(30)
  os._exit(0)
os.close(read_fd)
with open(root+"parent.pid", "w") as target:
 target.write(str(os.getpid()))
while not all(os.path.exists(root+str(i)+".pid") for i in range(3)):
 time.sleep(0.01)
print("READY", flush=True)
input()
os.write(write_fd, b"xxx")
time.sleep(30)`
				term := open(t, ctx, CommandTerminalRequest{
					Command:     "stty -echo; python3 -c " + ShellQuote(code),
					MemoryBytes: 96 * 1024 * 1024, Timeout: 6 * time.Second,
				})
				terminalReadUntil(t, term, "READY")
				require.NoError(t, term.Input(ctx, []byte("allocate\n")))
				output, err := io.ReadAll(term)
				require.NoError(t, err)
				require.NotContains(t, string(output), "MemoryError")
				require.NotContains(t, string(output), "AssertionError")
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				// Check adopted descendants even if a regression returns timeout.
				exec(t, `for file in /workspace/output/terminal-memory-*.pid; do `+
					`pid=$(cat "$file"); test ! -e /proc/$pid || exit 1; done`)
				require.Equal(t, "memory_limit", exit.Reason)
				require.Equal(t, 200, exit.ExitCode)
			})

			t.Run("SIGKILLIsNotMemoryLimit", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{Command: "kill -KILL $$"})
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 137, exit.ExitCode)
				require.Equal(t, "exited", exit.Reason)
			})

			t.Run("ReservedMemoryExitIsNotMemoryLimit", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{Command: "exit 200"})
				exit, err := term.Wait(ctx)
				require.NoError(t, err)
				require.Equal(t, 1, exit.ExitCode)
				require.Equal(t, "exited", exit.Reason)
			})

			t.Run("UnreadOutputBounded", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{
					Command: `python3 -c 'import os; os.write(1,b"x"*1048576)'`,
				})
				exit, err := term.Wait(ctx)
				require.ErrorIs(t, err, ErrTerminalOutputLimit)
				require.Equal(t, "output_limit", exit.Reason)
			})

			t.Run("RepeatedCloseDoesNotLeak", func(t *testing.T) {
				baseline := runtime.NumGoroutine()
				for i := 0; i < 6; i++ {
					term := open(t, ctx, CommandTerminalRequest{Command: "printf READY; sleep 30"})
					terminalReadUntil(t, term, "READY")
					require.NoError(t, term.Close())
					require.NoError(t, term.Close())
				}
				require.Eventually(t, func() bool {
					return runtime.NumGoroutine() <= baseline+4
				}, 3*time.Second, 50*time.Millisecond)
			})

			t.Run("ImmediateClosePreventsLateCommand", func(t *testing.T) {
				exec(t, "rm -f /workspace/output/terminal-early-*")
				for i := 0; i < 4; i++ {
					term := open(t, ctx, CommandTerminalRequest{
						Command: fmt.Sprintf("sleep 1; touch /workspace/output/terminal-early-%d", i),
					})
					// Do not wait for the first byte or for the runner's child PID.
					require.NoError(t, term.Close())
				}
				time.Sleep(1200 * time.Millisecond)
				exec(t, `test -z "$(find /workspace/output -name 'terminal-early-*' -print -quit)"`)
			})

			t.Run("NoShellRemainsAfterCommand", func(t *testing.T) {
				term := open(t, ctx, CommandTerminalRequest{Command: "printf AUDITED"})
				output, err := io.ReadAll(term)
				require.NoError(t, err)
				require.Equal(t, "AUDITED", string(output))
				_, err = term.Wait(ctx)
				require.NoError(t, err)
				err = term.Input(ctx, []byte("touch /workspace/output/terminal-injected\n"))
				require.ErrorIs(t, err, io.ErrClosedPipe)
				exec(t, "test ! -e /workspace/output/terminal-injected")
			})

			t.Run("ActiveTurnDefersStaleReplacement", func(t *testing.T) {
				require.NoError(t, mgr.BeginSessionTurn(ctx, session))
				term := open(t, ctx, CommandTerminalRequest{Command: "stty -echo; printf READY; read -r done"})
				terminalReadUntil(t, term, "READY")
				key, err := mgr.sessionKey(ctx, session)
				require.NoError(t, err)
				before, err := store.Get(ctx, key)
				require.NoError(t, err)
				_, err = mgr.InvalidateConfigSandboxes(ctx, key.TenantID, "weknora-wb-terminal")
				require.NoError(t, err)
				require.NoError(t, mgr.EndSessionTurn(ctx, session))
				exec(t, "printf STILL_LIVE")
				after, err := store.Get(ctx, key)
				require.NoError(t, err)
				require.Equal(t, before.SandboxID, after.SandboxID)
				require.NoError(t, term.Input(ctx, []byte("done\n")))
				_, err = term.Wait(ctx)
				require.NoError(t, err)
				active, _, err := store.TurnState(ctx, key)
				require.NoError(t, err)
				require.False(t, active)
				next, err := mgr.resolveSession(ctx, session)
				require.NoError(t, err)
				require.NotEqual(t, before.SandboxID, next.ID(),
					"stale replacement resumes after terminal completion")
			})
		})
	}
}

func terminalReadUntil(t *testing.T, terminal CommandTerminal, marker string) string {
	t.Helper()
	var output bytes.Buffer
	buffer := make([]byte, 4096)
	for !strings.Contains(output.String(), marker) {
		n, err := terminal.Read(buffer)
		_, _ = output.Write(buffer[:n])
		if err != nil && !strings.Contains(output.String(), marker) {
			t.Fatalf("read %q before %q: %v", output.String(), marker, err)
		}
		if output.Len() > terminalBufferLimit {
			t.Fatal("unexpected output while waiting for marker")
		}
	}
	return output.String()
}
