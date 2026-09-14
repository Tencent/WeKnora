# Docker Engine TTY Exec Reattach Spike

## Result

**Docker Engine native same-exec PTY reattach is not supported in the tested environment.**

After the first hijacked transport was closed, `ExecInspect` still reported the same exec as running with the same Engine PID. A second `ExecAttach` call returned a hijacked response at the client API layer, but that stream contained only:

```text
exec command <exec-id> is already running
```

and then EOF. Commands written to that connection did not reach the original PTY. Therefore a new bash process must not be presented as a reattach to the old terminal.

## Environment

| Item | Observed |
|---|---|
| Date | 2026-09-10 (Asia/Shanghai) |
| Repository baseline | `5db13a131e10e8ee2105211f665412ebc13bd98e` |
| Docker client | 29.7.2, API 1.55, Windows/amd64 |
| Docker server | Docker Desktop 4.88.1 (237512) |
| Docker Engine | 29.7.2, API 1.55 (minimum 1.40) |
| Engine OS/arch | Linux/amd64 |
| Kernel | `5.15.167.4-microsoft-standard-WSL2` |
| Runtime image | local `golang:1.26-bookworm` |
| Shell | `/bin/bash --noprofile --norc -i` |
| PTY | Docker exec with `TTY=true`, 80x24 |

The image was chosen because it was already present locally and contains bash. The reattach decision is an Engine exec lifecycle/API property, not a WeKnora image or prompt property. PR-01 integration must still run against the actual WeKnora sandbox image.

## Method

A temporary Go spike used the same Moby client module already pinned by WeKnora. It connected to the active Docker CLI context (`desktop-linux`) and performed the following Engine API sequence against an isolated temporary container:

1. `ServerVersion`
2. `ContainerCreate` using `golang:1.26-bookworm`
3. `ContainerStart`
4. `ExecCreate` with:
   - `TTY=true`
   - `AttachStdin=true`
   - `AttachStdout=true`
   - `AttachStderr=true`
   - `ConsoleSize={Height:24, Width:80}`
   - `WorkingDir=/`
   - `User=root`
   - `Env=[TERM=xterm-256color]`
   - `Cmd=[/bin/bash --noprofile --norc -i]`
5. First `ExecAttach` with `TTY=true`
6. Write:

   ```bash
   cd /tmp
   export WEKNORA_TEST=123
   printf '__WEKNORA_SPIKE_READY__%s|%s__END__\n' "$PWD" "$WEKNORA_TEST"
   ```

7. Wait for the marker proving the live shell state.
8. Close only the first `HijackedResponse`; do not exit bash and do not stop the container.
9. `ExecInspect` the same exec ID.
10. Call `ExecAttach` again for the same exec ID and attempt to write a state-check command.
11. `ExecInspect` again.
12. Force-remove the isolated container in cleanup.

No new bash was created for the reconnect attempt.

## Observations

### Initial shell

The first raw TTY stream contained ANSI/bracketed-paste control sequences and the exact marker:

```text
__WEKNORA_SPIKE_READY__/tmp|123__END__
```

This proves the initial interactive shell accepted stdin and had:

- `pwd == /tmp`
- `WEKNORA_TEST == 123`

### After first transport disconnect

`ExecInspect` returned:

```text
inspect_error=""
running=true
pid=27973
exit_code=0
```

The shell/exec therefore remained alive after the client transport was closed. The PID reported by Docker's exec inspect is an Engine/daemon host PID, not a portable provider PTY identifier suitable for a browser reattach token.

### Second attach attempt

The second Moby `ExecAttach` call itself returned no immediate Go error because the daemon had already hijacked the HTTP connection. The raw response stream then produced:

```text
exec command bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d is already running
```

and EOF. Writing the state-check command returned no local write error, but no state marker appeared because the connection was not attached to the original PTY.

Final `ExecInspect` still reported:

```text
inspect_error=""
running=true
pid=27973
exit_code=0
```

Thus the process survived, but the Engine exec API provided no usable second stdin/output transport to it.

## Requirement verdict

| Requirement | Observed | Verdict |
|---|---|---|
| Create a TTY interactive shell | Bash started and emitted raw PTY output | PASS |
| Set cwd/env | `/tmp` and `123` observed before disconnect | PASS |
| Disconnect only the transport | First hijacked response closed; container was not stopped | PASS |
| Shell/exec remains alive | `ExecInspect.Running=true` after disconnect | PASS |
| Reattach the same TTY/exec | Second stream reported `exec command ... is already running` and EOF | FAIL / NOT SUPPORTED |
| Verify cwd/env through reattached PTY | No reattached stdin/output channel existed | NOT_VERIFIABLE |
| Cleanup | Temporary container force-removed successfully | PASS |

`NOT_VERIFIABLE` is intentional: the same bash process was still alive, so this evidence does not claim its internal cwd/env disappeared. It establishes that Docker Engine did not expose a native transport through which the required post-reconnect checks could be made.

## Repeated-run consistency

The core experiment was repeated. Both runs produced the same provider behavior:

- the first state marker was observed;
- closing the first hijacked response left `ExecInspect.Running=true`;
- the second `ExecAttach` stream said the exec was already running and closed;
- the original exec remained running until container cleanup.

## API interpretation

For Docker exec, Moby's `ExecAttach` starts the exec using `POST /exec/{id}/start` and returns its initial hijacked stream. It is not a separate “attach an already-started exec” endpoint. `ExecResize` and `ExecInspect` operate on a running exec, but neither supplies a new stdin/output transport.

This differs materially from E2B/Cube envd, whose PTY APIs expose `Connect(PID)` for an already-running PTY.

## Design consequence

PR-01 must represent two separate capabilities:

```text
SupportsTerminals          = true
SupportsTerminalReconnect = false
```

for Docker, while E2B and Cube advertise both as true.

The minimal provider-neutral correction is:

1. Add `SupportsTerminalReconnect` to `RemoteSandboxCapabilities`.
2. Carry a neutral `reattachable` value in the existing terminal ready frame.
3. For non-reattachable providers, return no reusable PTY ID, clear any stale stored PTY ID, and do not submit `AttachPID` on later connections.
4. Do not automatically describe a newly created Docker shell as the old shell being resumed.
5. Update `RemoteTerminalOptions.AttachPID` and `RemoteTerminalSession.Close` documentation so process survival and reattach are capability-dependent.

No new WebSocket, ticket, frontend terminal component, or Docker-specific UI is justified. The existing connection can consume one provider-neutral capability bit.

Remote process cleanup after a non-reattachable transport loss remains a separate correctness concern for the Docker adapter. PR-01 must either implement bounded best-effort cleanup without exposing a container/host PID to the browser, or explicitly document that the inaccessible exec is bounded by the existing container idle lifecycle. It must not solve this by claiming a fresh shell is the old PTY.

## Raw decisive output

```text
ENGINE platform="Docker Desktop 4.88.1 (237512)" version=29.7.2 api=1.55 min_api=1.40 os=linux arch=amd64
API ExecCreate tty=true attach_stdin=true attach_stdout=true attach_stderr=true cmd=/bin/bash
API ExecAttach attempt=1 exec=bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d tty=true
FIRST_STATE_MARKER observed=true
TRANSPORT disconnect=close-first-hijacked-response
API ExecInspect phase=after_disconnect exec=bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d
AFTER_DISCONNECT inspect_error="" running=true pid=27973 exit_code=0
API ExecAttach attempt=2 exec=bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d tty=true
SECOND_ATTACH success=true error=""
SECOND_STATE_MARKER observed=false write_error="" output="exec command bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d is already running\r\n"
SECOND_READER_END error="EOF"
API ExecInspect phase=final exec=bffe5ef1b3746ec6ac092a926e7ecdb96018e364b172cba6ae490ef330471c0d
FINAL inspect_error="" running=true pid=27973 exit_code=0
CONCLUSION native_same_exec_reattach_supported=false shell_alive_after_disconnect=true state_preserved=false
API ContainerRemove force=true error=""
```

## Cleanup

The temporary spike container was removed after each run. The temporary Go driver was kept outside the Git repository while evidence was collected and is not part of the intended PR diff.
