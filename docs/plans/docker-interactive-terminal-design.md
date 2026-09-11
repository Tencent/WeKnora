# Docker Interactive Terminal Design

## Status and baseline

- Status: implemented and validated in PR #3175; retained as the design record.
- Source of truth: `Tencent/WeKnora` `upstream/main`.
- Baseline SHA: `5db13a131e10e8ee2105211f665412ebc13bd98e`.
- Baseline date: 2026-09-10 (Asia/Shanghai).
- Scope: add Docker as the third implementation of the existing interactive session-terminal abstraction.
- Out of scope: Files, terminal Audit, resource-limit redesign, ArtifactKind, Presentation Skill, a second terminal abstraction, a second WebSocket/ticket flow, and Docker-specific frontend terminal UI.

The implementation follows the existing E2B/Cube adapter family:

```text
RemoteTerminalManager
  -> DockerRemoteClient.OpenTerminal
  -> Docker Engine TTY exec
  -> dockerTerminalSession
       -> pump / Output / PID / Write / Resize / Close
```

## A. Provider capability comparison

| Concern | E2B (`e2b_terminal.go`) | Cube (`cube_terminal.go`) | Docker design |
|---|---|---|---|
| PTY create | `sbx.Pty.Create` through envd | `sb.Pty().Create` through envd | `ExecCreate` with `TTY=true`, all three attach flags enabled, provider-neutral cwd/user/env translated to Engine options; `ExecAttach` starts and attaches the shell |
| Shell/prompt | Provider PTY shell; image prompt bootstrap | Provider PTY shell; image prompt bootstrap | Interactive `/bin/bash` in the existing sandbox image; reuse `/etc/weknora/pty-prompt.sh`, `/root/.bashrc`, `/home/user/.bashrc`; no prompt logic in Go |
| Reconnect | `Pty.Connect(PID)`; fallback to create | `Pty.Connect(PID)`; fallback to create | Real Engine spike is a hard gate. Do not claim reattach unless the same exec/PTY can be attached again with cwd/env preserved. If unsupported, expose the provider difference explicitly instead of opening a fresh shell and calling it reattach |
| stdin | envd unary `SendInput`, coalesced for latency | `PtyHandle.SendStdin` | Raw writes to the hijacked connection. Empty writes are no-ops. Writes remain safe against concurrent resize/close |
| output | Wait callback emits raw chunks | Wait callback emits raw chunks | Read `ExecAttachResult.Reader` directly as raw PTY bytes. Never call `stdcopy.StdCopy` for `TTY=true` |
| resize | envd `Pty.Resize` | `PtyHandle.Resize` | `ExecResize(execID, Height=rows, Width=cols)` through the narrow Engine interface and bounded RPC wrapper |
| exit | Wait result / `CommandExitError` | Wait exit code | On stream EOF, inspect the exec. Emit `Exited=true` only when `ExecInspect.Running=false`; use its exit code. EOF while still running is a transport loss, not a shell exit |
| `Close` | cancel stream + `Disconnect`; shell remains | `Disconnect`; shell remains | Idempotently close local transport and stop adapter goroutines. Whether the shell survives and is reattachable is determined by the real spike, not assumed |
| context cancellation | PTY context ends stream | provider handle/context ends stream | A cancellation watcher invokes the same idempotent local teardown; cancellation must unblock `pump` without leaking the exec stream |
| short RPC timeout | envd transport bounds unary calls and exempts PTY routes | transport bounds unary calls and exempts process stream | `ExecCreate`, `ExecInspect`, and `ExecResize` use `withDockerRPCTimeout`; `ExecAttach` remains on the caller/session context and must not receive `DockerHTTPTimeout` |
| sandbox TTL/activity | `SetTimeoutWithContext` via `startTerminalTTLRefresh` | `SetTimeout` via `startTerminalTTLRefresh` | Touch the existing Docker activity marker when the shell starts, then use a low-frequency heartbeat while the terminal is open. Reuse `startTerminalTTLRefresh` timing and perform one bounded marker refresh per interval, never one Engine RPC per keystroke |
| errors | `normalizeE2BError` | `normalizeCubeError` | Every Engine failure is wrapped with `dockerError` and a terminal-specific operation name; invalid/mismatched handles use `dockerHandleID`/`dockerInvalidRequest` |
| output shutdown | pump alone closes `out` | pump alone closes `out` | pump is the sole closer of `out`; all sends use `emitTerminalEvent`; `Close` only signals teardown and closes transport |

Docker interactive terminal support requires `/bin/bash` in the configured image. The standard WeKnora sandbox image satisfies this contract. A custom image without `/bin/bash` may still support ordinary sandbox `Exec` operations, but interactive terminal availability is not guaranteed.

### Docker create and stream details

The new adapter will validate the opaque handle with `dockerHandleID`; browser/API callers never supply a container ID. `SessionBoundManager.lookupSessionHandle` remains the only source of the handle, so the authoritative tenant/session binding remains the sandbox identity boundary.

Planned `ExecCreateOptions`:

```text
TTY=true
AttachStdin=true
AttachStdout=true
AttachStderr=true
ConsoleSize=[rows, cols]
WorkingDir=terminalCwd(opts)
User=terminalUser(opts)
Env=dockerEnvSlice(terminalEnvs(opts)), with TERM=xterm-256color guaranteed
Cmd=interactive bash using the image's existing prompt bootstrap
```

`ExecAttachOptions` will also set `TTY=true` and the initial console size. The attached reader is a raw byte stream because TTY mode combines stdout/stderr without Docker's eight-byte multiplexing headers.

The stable internal control key is the Engine exec ID, not a browser-supplied container ID or OS PID. Docker returns `PID() == 0`: its inspected PID is a daemon-host process identifier and must not be exposed as a reusable PTY token.

### Concurrency and teardown invariants

- `Write` and `Resize` may run concurrently.
- Writes are serialized so input chunks cannot interleave.
- State/close coordination uses `sync.Once` plus `closedCh`; local close is idempotent under concurrent calls.
- The hijacked transport is closed exactly once and is the mechanism that unblocks a blocked read.
- `pump` is the only goroutine allowed to close `Output()`.
- `pump` checks local teardown before interpreting EOF, so client disconnect/cancellation is silent.
- Every output send uses `emitTerminalEvent` to avoid blocking after the WebSocket consumer leaves.
- A natural stream end is followed by bounded `ExecInspect`; only a confirmed stopped exec produces an exit event.
- Heartbeat and cancellation goroutines both select on `closedCh`/context and cannot outlive session teardown.

### Activity/idle lifecycle

Docker's idle sweeper reads `/var/lib/weknora-sandbox-activity`. Ordinary non-TTY execs touch it inside `dockerExecCommand`; an interactive terminal does not use that wrapper continuously.

The terminal path will:

1. Touch the marker as part of the trusted shell bootstrap before `exec bash`, avoiding a separate open-time Engine round trip.
2. Start `startTerminalTTLRefreshAfterInitialTouch` with the configured/effective Docker idle TTL, so the first provider refresh waits for the normal interval.
3. On each low-frequency refresh, execute one bounded provider-local marker refresh (expected to reuse the existing non-TTY `Exec(..., command=true)` path).
4. Stop refresh immediately on `Close`, context cancellation, or shell exit.

This keeps an active terminal ahead of the sweeper without making keystrokes trigger Docker API traffic. The minimum non-zero Docker idle TTL is 60 seconds, while the production refresh floor is 15 seconds; unit tests pin that every valid Docker TTL exceeds its refresh interval. Real integration observes the activity marker advance on the production interval and asks the real sweeper decision path to preserve the active terminal container.

### Reconnect decision

The [Docker reattach spike](../poc/docker-terminal-reattach-spike.md) records the real-daemon result: after the initial hijacked transport closes, the exec remains `Running=true`, but a second `ExecAttach` stream says `exec command ... is already running` and immediately reaches EOF. No command can be sent to the original PTY. Docker Engine therefore does not provide the `Connect(PID)` behavior exposed by E2B/Cube.

PR-01 must not map a new bash process to the old PID or report it as a successful reattach. The minimal capability correction is provider-neutral:

1. Add `SupportsTerminalReconnect` alongside `SupportsTerminals`.
2. Advertise it as true for E2B/Cube and false for Docker.
3. Carry `reattachable` on the existing ready control frame.
4. When false, expose no reusable PTY ID, clear any stale stored ID, and do not submit `AttachPID` on later connections.
5. Correct `RemoteTerminalSession.Close` documentation so remote process survival/reattach is explicitly capability-dependent.

Any frontend change is limited to consuming that neutral bit in the existing composable; there is no Docker-specific component, route, ticket, or frame family. An automatic transport retry must not be described as restoring the previous Docker shell.

## B. Expected production files

Mandatory:

| File | Change |
|---|---|
| `internal/sandbox/docker_terminal.go` | New Docker `RemoteTerminalManager` adapter and `dockerTerminalSession` implementation |
| `internal/sandbox/docker_engine.go` | Add the narrow `ExecResize` Engine method used by interactive TTY sessions |
| `internal/sandbox/docker_rpc_timeout.go` | Bound `ExecResize`; keep `ExecAttach` unbounded by the fixed Docker HTTP timeout |
| `internal/sandbox/docker_remote_client.go` | Advertise `SupportsTerminals` only after the adapter exists; expose/reuse the minimal marker refresh needed by terminal activity |

Required by the negative reattach spike:

| File | Why required |
|---|---|
| `internal/sandbox/remote_client.go` | Add one provider-neutral terminal-reconnect capability bit if Docker cannot honor the existing implicit reattach contract |
| `internal/sandbox/terminal.go` | Correct the provider-neutral `AttachPID`/`Close` documentation and, if necessary, expose a neutral reattach token/capability without adding another terminal abstraction |
| `internal/application/service/sandbox_terminal_service.go` | Pass the neutral reattach capability to the existing bridge only if needed |
| `internal/handler/session/sandbox_terminal_ws.go` | Add a field to the existing ready frame only if needed; no new route or frame family |
| `frontend/src/composables/useSandboxTerminal.ts` and its focused tests | Stop persisting/sending a reattach token when the ready frame says this provider cannot reattach; no Docker-specific UI |

## C. Expected test files and why

| File | Purpose |
|---|---|
| `internal/sandbox/docker_terminal_test.go` | Unit coverage for options, raw stream, stdin, resize, Ctrl-C bytes, ANSI/Unicode passthrough, exit/error classification, cancellation, idempotent close, exact output close, and concurrent calls |
| `internal/sandbox/docker_remote_client_test.go` | Extend the existing shared fake Engine with `ExecResize`; assert capability advertisement and marker refresh plumbing |
| `internal/sandbox/docker_integration_test.go` | Real-daemon terminal conformance: echo, cwd, Unicode, ANSI, size, Ctrl-C, long-lived shell, exit code, daemon/container failure, and terminal-only activity lifetime |
| `internal/sandbox/session_manager_terminal_test.go` | Confirm Docker is now surfaced through the existing session manager and binding remains authoritative |
| Existing timeout tests or a focused new Docker timeout test | Prove create/inspect/resize are bounded while attach survives beyond `DockerHTTPTimeout` |
| Conditional WebSocket/frontend tests | Only if the spike requires an explicit reconnect capability bit |

No mock-only result can complete PR-01. Unit tests are necessary for deterministic races/errors; the tagged real Docker suite is required for the provider claims.

## D. Existing layers that do not need modification

Absent a negative-spike capability correction, the following are complete and must remain unchanged:

- Terminal ticket issuance, parsing, expiry, and token binding.
- The existing `/api/v1/sessions/:id/sandbox/terminal` WebSocket route and binary/control-frame protocol.
- The current `terminalBridge`, including ping/pong, idle disconnect, auth recheck, output/input pumps, and teardown.
- Session ownership checks, tenant context, RBAC checks, and periodic authorization revalidation.
- `SessionSandboxPinner`, `SessionBoundManager` lifecycle resolution, session binding store, and provision/lookup split.
- The existing xterm component, side panel, keyboard handling, binary framing, resize UI, and terminal concurrency limit.
- E2B and Cube terminal adapters.
- Prompt implementation in `docker/sandbox-pty-prompt.sh` and the prompt wiring already present in `docker/Dockerfile.sandbox`.
- Ordinary Docker `Exec` and its `stdcopy.StdCopy` path; it remains correct for non-TTY multiplexed output.

## E. Real Docker test plan

### Environment record

Capture before tests:

```powershell
docker version
docker info
git rev-parse HEAD
git rev-parse upstream/main
```

Record Engine API/version, OS/architecture, image digest/tag, exact command, and the full observed result. Tests that cannot run are `NOT_RUN` or `SKIPPED`, never inferred PASS.

### Reattach spike (must precede implementation)

1. Create an isolated temporary container from the standard sandbox image (or a compatible local Linux image if the standard image is unavailable).
2. `ExecCreate` an interactive TTY shell with stdin/stdout/stderr attached.
3. `ExecAttach` with `TTY=true`.
4. Send `cd /tmp`, `export WEKNORA_TEST=123`, then emit a unique readiness marker.
5. Close only the hijacked client transport; do not stop the container or intentionally exit the shell.
6. Poll `ExecInspect` and record `Running`, PID, and exit code.
7. Attempt the Engine-native start/attach API again for the same exec ID.
8. If it succeeds, send `pwd` and `printf` for `WEKNORA_TEST`; require `/tmp` and `123` from the same shell.
9. Record whether closing the second transport changes process state.
10. Force-remove the isolated temporary container in cleanup.

The outcome and raw observations are recorded in the [Docker reattach spike](../poc/docker-terminal-reattach-spike.md).

### PR-01 real integration matrix

| Case | Expected evidence |
|---|---|
| echo/stdin/output | Raw command echo and command output arrive in order without stdcopy headers |
| cwd/user/env | `pwd`, `id`, and injected env match neutral options |
| Unicode | Chinese/emoji round-trip byte-for-byte |
| ANSI | Color escape sequences remain present |
| resize | `stty size` changes after `ExecResize` |
| Ctrl-C | A foreground `sleep` is interrupted and the interactive shell remains usable |
| exit code | `exit 23` emits exactly one exited event with code 23, then closes output once |
| long stream | Terminal remains usable beyond a deliberately short `DockerHTTPTimeout`, proving attach is not fixed-timeout-bound |
| context cancel/Close | Local transport and all adapter goroutines terminate; `Close` is idempotent and produces no false exit event |
| concurrent calls | Concurrent `Write`/`Resize`/`Close` under `-race` produces no panic, data race, or hang |
| daemon/API errors | Create/attach/inspect/resize failures are normalized as Docker `RemoteError`s |
| container kill | Stream ends as a real terminal/provider event with no orphan goroutine |
| activity | A terminal-only active container survives an idle sweep; refresh stops after terminal closure |
| reconnect | Run only if the spike proves native same-exec reattach; otherwise explicitly `NOT_SUPPORTED` with the capability behavior tested |

### Validation commands after implementation

```powershell
gofmt -w <changed-go-files>
go test ./internal/sandbox -run 'TestDocker.*Terminal|Test.*Terminal.*Docker'
$env:DOCKER_INTEGRATION_IMAGE='<verified-image>'
go test -tags=docker_integration ./internal/sandbox -run 'TestDocker.*Terminal' -count=1 -v
git diff --check
golangci-lint run --new-from-rev=upstream/main ./...
```

Before publication: fetch `upstream`, rebase the PR branch onto the then-current `upstream/main`, rerun all affected unit and real Docker integration tests, self-review the complete diff, and record only actual results in the official PR template.
