# Sandbox hard-limit gap report

## Baseline and scope

- Source of truth: `upstream/main` at `462999ec3f5c1467ef0ccf5cf8c422393f40a0e6`.
- Scope: A5 CPU, memory, and elapsed-time over-limit termination for the current session-persistent sandbox architecture. Terminal command audit is isolated in PR #3190 and is not duplicated here.
- Open PR #3146 remains an unmerged, broad Workbench/`CommandTerminal` implementation. Its limit tests cover socket authentication, semaphore/backpressure, and output cancellation; they do not establish CPU, memory, or hard wall-time termination. No code is copied from it.
- “Terminated” below means the offending process or the sandbox is actually gone. A WebSocket close, provider pause, client context cancellation, or CPU throttling alone is not counted as hard termination.

## Current semantic matrix

| Semantic | Docker | E2B | Cube | Hard termination today? | Remaining gap |
|---|---|---|---|---|---|
| Memory | Container create applies cgroup `Memory` and `MemorySwap=Memory`; the kernel reports OOM and kills the allocating process. | Memory size is a property of the selected template/instance and is observable in `SandboxInfo`; WeKnora has no per-workspace memory-limit field or OOM signal. | Memory is fixed while building a template and is observable in `SandboxInfo`; create has no per-sandbox memory override or OOM signal. | Docker: process yes, session no. E2B/Cube: not proven by current code. | Docker must classify OOM separately and destroy the session container; provider APIs do not currently expose equivalent portable evidence for E2B/Cube. |
| CPU capacity | `NanoCPUs` limits the container to a CPU share/maximum rate. | CPU count comes from the selected template/instance. | CPU count comes from the selected template. | No. All three are allocation/throttle semantics, not accumulated CPU-time termination. | Docker needs a measured cumulative CPU-time budget and an automatic destructive action. E2B/Cube expose no equivalent cumulative CPU counter through the adapters. |
| Per-command time | Ordinary Docker exec uses in-container `timeout -s KILL` and reports exit 124/137 as `Killed=true`. | Ordinary command run has a request/provider timeout and timeout normalization. | Ordinary command run has a request/provider timeout and timeout normalization. | Yes for an ordinary exec process. | It does not cover an interactive PTY or the lifetime of the persistent sandbox. |
| Hard wall time | None. | None. | None. | No. | A continuously active session can live indefinitely. A hard deadline must be independent of activity and must destroy, not pause, the sandbox. |
| Idle timeout / TTL | A request-triggered sweeper reads an activity marker and force-removes an idle container. | Provider timeout is configured as auto-pause/auto-resume and terminal activity refreshes it. | Provider timeout is configured as auto-pause/auto-resume and terminal activity refreshes it. | Docker idle: sandbox deletion after a later sweep trigger. E2B/Cube: pause, not termination. | Idle is intentionally distinct from hard lifetime. It cannot satisfy the wall-time requirement. |
| Terminal idle | The shared WebSocket bridge disconnects after `TerminalIdleDisconnect`; PR #3175 also refreshes Docker activity while a terminal is active. | The bridge disconnects and PTY `Close` preserves the remote process. | Same. | No. | A socket disconnect must never be reported as process or sandbox termination. |
| Process cleanup | Ordinary exec timeout kills that exec. Interactive `RemoteTerminalSession.Close` intentionally only detaches. | Same contract: PTY close detaches; provider PTY has a separate kill operation not used by `Close`. | Same. | Only ordinary timed exec. | Limit enforcement must not change `RemoteTerminalSession.Close`; session-wide breaches should use destructive lifecycle deletion. |
| Sandbox/session destroy | `SessionBoundManager.DestroySession` force-removes the container and deletes the matching binding. | The same path calls E2B delete/kill and removes the binding. | The same path calls Cube kill and removes the binding. | Yes when explicitly invoked. | No current CPU/memory/hard-wall controller invokes this path automatically. |

## Real Docker baseline evidence

Run on Docker Engine `29.7.2`, API `1.55`, Linux/amd64, using `wechatopenai/weknora-sandbox:dev`.

The probe created one disposable container with `--memory 64m --memory-swap 64m --cpus 0.25`, ran a 256 MiB Python allocation, then a three-second CPU burner, inspected the container after each workload, and force-removed the exact probe container.

Observed:

- Engine inspection confirmed `memory=67108864`, `memorySwap=67108864`, and `nanoCPUs=250000000`.
- The memory allocation exited `137`; `State.OOMKilled=true`, but `State.Running=true` and PID 1 remained alive. Therefore the current memory cap kills the allocation but does not terminate the session sandbox.
- The CPU burner was stopped only by the probe's explicit `timeout` with exit `124`; the container remained running. `NanoCPUs` is throttling, not a CPU-time budget.
- After more than eleven elapsed seconds and ongoing work, the container remained running. There is no hard wall deadline.

This evidence rules out claiming that the existing Docker settings already satisfy A5.

## Required semantic separation

- **CPU throttle:** maximum instantaneous CPU capacity (`NanoCPUs`, template vCPU count). It may slow work forever.
- **CPU hard termination:** destructive action after accumulated CPU consumption exceeds a configured budget.
- **Memory cgroup/OOM:** kernel enforcement of resident memory. The offending allocation can die while the session container survives.
- **Idle timeout:** based on absence of activity; activity refreshes it.
- **Hard wall time:** elapsed from sandbox creation regardless of activity, reconnects, or WebSocket state.
- **WebSocket disconnect:** transport/liveness action only; it must retain the current PTY detach contract.
- **Process kill:** removes one exec or PTY process but can leave sibling/background processes and the sandbox alive.
- **Sandbox/session destroy:** provider/container deletion plus binding cleanup; this is the strongest existing lifecycle primitive.

## Minimal accurate PR-04 scope

The currently implementable, verifiable hard-limit backend is Docker because its Engine API exposes cgroup configuration, OOM state, cumulative CPU accounting, container creation time, and force removal. E2B/Cube expose allocated CPU/memory but not equivalent per-session usage/OOM evidence through the current SDK contracts. PR-04 must not pretend those provider gaps are solved.

Planned focused scope:

1. Keep the existing `cpu_limit` as a throttle and `memory_limit_mb` as the cgroup ceiling.
2. Add separately named Docker policies for cumulative CPU-time budget and hard sandbox lifetime; validate and label them at container creation so each sandbox keeps the policy it was created with.
3. Start one deduplicated watchdog per live Docker session container from the normal create/connect path. It must survive the initiating request context, stop when the container disappears, and never multiply across manager reconstructions.
4. Poll bounded Engine inspect/stats calls at a low frequency. Breach reasons are distinct: `memory_oom`, `cpu_time`, and `wall_time`.
5. On any breach, force-remove the container. Do not implement the limit by closing the browser socket or by changing terminal `Close` semantics. The lifecycle already treats a provider-missing sandbox as replaceable; where the session manager owns the watcher, prefer `DestroySession` so binding cleanup is immediate.
6. Keep the implementation Docker-specific behind the existing session sandbox and Engine abstractions. Do not add fake CPU/memory controls to E2B/Cube or a second terminal/control WebSocket.
7. Document the provider capability gap explicitly in configuration help and PR limitations.

The design checkpoint before production edits is whether the watchdog can call the current `SessionDestroyer` without creating a dependency cycle. If it cannot, Engine force-removal is acceptable only with tests proving the existing binding is replaced on the next session resolution and no container/process is orphaned.

## Test plan

- Unit: policy validation/default/serialization; labels are immutable per container; CPU nanoseconds and wall deadline calculations; OOM/CPU/wall reason classification; watchdog deduplication, cancellation, bounded RPCs, idempotent deletion, and not-found cleanup.
- Lifecycle: a limit-deleted container leaves no live process; the stale binding is safely replaced or explicitly removed; a later request cannot reconnect to the destroyed sandbox.
- Concurrency: repeated create/connect calls produce one watcher; simultaneous breach/delete does not panic or leak a goroutine.
- Real Docker memory workload: exceed a small cgroup limit, observe OOM, then verify the entire managed container is absent rather than merely observing exec exit 137.
- Real Docker CPU workload: run a burner under a small cumulative CPU-time budget and verify container removal; do not use an external command timeout as the terminating mechanism.
- Real Docker wall workload: keep a session active past a small hard lifetime and verify container removal independent of activity.
- Evidence: record Engine version, image ID, configured limits, observed reason, container inspection/removal result, and absence of orphaned processes.
- Quality gates: gofmt, focused tests, real Docker integration, `git diff --check`, diff-scoped golangci-lint, self-review, latest-main rebase, and affected-test rerun.

## Known capability boundary

Completing the Docker path provides truthful A5 termination evidence on one formal session-persistent backend. It does not make E2B/Cube resource accounting portable. Extending CPU/memory termination to those providers requires provider-native usage/OOM APIs or a trusted in-guest supervisor with stronger semantics than a user-controlled shell; neither exists in current `upstream/main`, so PR-04 will not fabricate it.

## Implementation verification

The completed Docker implementation was exercised against Docker Engine `29.7.2` / API `1.55` on Linux/amd64 with image `wechatopenai/weknora-sandbox:dev` (`sha256:a28200c75e4229f1c397155fbb0aaf5540051258761408d4998d505b3b462de8`). The tagged integration test used the production Engine adapter and real session lifecycle:

```text
go test -tags=docker_integration ./internal/sandbox \
  -run '^TestDockerHardLimitsIntegration$' -count=1 -v -timeout=5m
```

Observed results:

- Memory: a 512 MiB Python allocation under a 64 MiB cgroup limit produced `State.OOMKilled=true`; the watcher force-removed the entire container with reason `memory_oom`.
- CPU: a Python busy loop crossed a one-second cumulative CPU budget at `1.1757176s`; the watcher force-removed the container with reason `cpu_time`.
- Wall: an actively running Python loop crossed a five-second hard lifetime at `5.174899146s`; the watcher force-removed the container with reason `wall_time`.
- For all three cases, Engine inspect returned not-found for the old container, the attached exec unblocked, the stale session binding produced one fresh replacement container on the next execution, and cleanup left no matching managed container behind.

Result: `PASS` (`TestDockerHardLimitsIntegration`, three subtests, 14.90s; package 16.008s). This is evidence of Docker hard termination only; the E2B/Cube boundary above remains unchanged.
