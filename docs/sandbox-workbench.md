# Sandbox Workbench

The workbench adds a command console and file manager to a chat session's existing sandbox. It reuses the session config pin, Docker/E2B/Cube lifecycle and artifact previews. The ReAct loop and buffered `shell_exec` contract stay unchanged.

Baseline: official main `647848f3`, after v0.8.0 (`1edcd54b`). Local host execution is excluded.

## Scope

- Submit a command, receive live PTY output, send stdin, resize, interrupt and close.
- Browse `/workspace/output`, upload/download, create directories, rename, delete files or empty directories.
- Reuse PPTX/HTML/spreadsheet preview and durable message artifacts.
- Ship an original `presentation-builder` Skill using python-pptx.
- Verify Docker Engine and a real E2B-compatible Agent-Sandbox backend on Kind. Kind provides container isolation, not MicroVM isolation.

The surface is opt-in (`WEKNORA_SANDBOX_WORKBENCH_ENABLED=true`). It requires an active web user, active workspace membership, session ownership, a named sandbox config and workspace policy permitting scripts. Clients never choose provider sandbox/process IDs. Files, secrets and evidence from local runs stay under ignored `.runtime/`.

## Deployment

Set the application origin explicitly when using a reverse proxy:

```dotenv
WEKNORA_SANDBOX_WORKBENCH_ENABLED=true
WEKNORA_SANDBOX_WORKBENCH_ORIGINS=https://weknora.example.com
```

Redis is required for multi-instance ticket consumption and console leases. The in-memory alternative requires `WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE=true`. Missing shared state fails closed and is not advertised as terminal capability. Vite and the supplied nginx proxy pass WebSocket upgrades; production ingress must do the same. Authentication remains in the first socket frame, so proxies must not log frame payloads. Authenticated consoles are capped at 256 per deployment and four per user; each session still permits one console.

| Backend | Terminal | File API | Verification |
| --- | --- | --- | --- |
| Docker Engine | Native TTY exec | Descriptor-based helper | Real local runtime |
| E2B protocol | envd PTY | Descriptor-based helper | Agent-Sandbox 0.8.3 on Kind |
| Cube | envd adapter | Disabled | Protocol tests only; native deployment not tested |

Cube file access remains disabled because its existing command logger does not provide the helper-specific redaction boundary. The local E2B implementation supports create/execute with an explicit template ID but returns 404 for the E2B template catalog. That provider's Settings template picker is not covered by this delivery. No E2B Cloud or MicroVM claim follows from the Kind tests.

## Terminal Contract

Each submitted command launches a separate shell process with a PTY. Stdin is accepted only during execution. Audit records cover server-accepted launches, not arbitrary nested shell commands or terminal keystrokes. No process starts until its accepted audit record is durable.

An optional terminal interface provides byte-stream reads, input, resize, interrupt, wait and close. Missing capability fails closed. Docker uses native Engine TTY exec; remote adapters use envd's documented Connect process protocol. Existing buffered Exec behavior is preserved. Explicit initialization probes a versioned runtime contract: terminal needs Python 3, Bash, Linux `/proc` and `prctl`; the file helper additionally requires descriptor APIs and `renameat2(RENAME_NOREPLACE)`. A failed probe suppresses only the unsupported capability without allocating from status reads. Probe results expire after 30 minutes and are capped at 1,024 config states and 4,096 sandbox states so retired configs and reaped sandbox IDs cannot accumulate in process memory.

Docker reconnect refreshes the sandbox activity marker before starting an idle sweep. Initialization also holds the session lifecycle lock and a temporary turn lease across workspace preparation and probing; a provider-confirmed reclaim or removal conflict is re-resolved once. This prevents an expired container from being deleted between reconnect and its first command without retrying ambiguous launches.

There is one command per console and a bounded connection lifetime. Frame size, dimensions, output queue, total output, CPU time, address space and wall duration are capped. UTF-8 and binary terminal input are converted to explicit byte frames and split below the negotiated JSON frame limit. Cancellation performs explicit cleanup with a fresh deadline; closing a transport alone does not establish process termination. An ambiguous launch is never automatically retried. Session ownership and workspace policy are rechecked every five seconds and before each command, while high-frequency stdin/resize/ping frames only renew the lease. Process shutdown cancels and waits for hijacked WebSocket consoles before resource cleanup.

Defaults are 120 seconds per command, 30 minutes per console, 60 CPU seconds and 512 MiB address space per process. The supervisor accounts for descendant CPU use and cleans remaining descendants after exit or cancellation. Address space is not a container-wide memory reservation: use the backend's memory/PID limits for aggregate containment. Resource exhaustion terminates the command; it does not automatically destroy the session sandbox or its files.

The distinction between container CPU shares and CPU-time exhaustion is retained. Resource checks must report what was actually enforced. Arbitrary shell commands can access files inside their own sandbox; the file API's path boundary does not confine shell execution. Host and cross-session isolation depend on provider configuration.

## HTTP And Socket

Authenticated API base: `/api/v1/sessions/{id}/sandbox`.

| Method | Suffix | Purpose |
| --- | --- | --- |
| GET | `/workbench` | Inspect pinned config/capabilities without allocation |
| POST | `/workbench` | Bind `{config_id}` and initialize on first use; retain existing pin |
| POST | `/terminal-ticket` | Issue single-use ticket, TTL 30 seconds |
| GET | `/files?path=...` | List a bounded directory |
| GET | `/files/download?path=...` | Download one regular file |
| POST | `/files` | Multipart full relative destination `path` + `file`, no overwrite |
| POST | `/directories` | Create `{path}` |
| PATCH | `/files` | Rename `{path,new_path}`, no overwrite |
| DELETE | `/files?path=...` | Delete file or empty directory |
| GET | `/audit` | Session-scoped workbench audit entries |

Gin wildcard names follow each existing method tree. The socket endpoint is `/api/v1/sandbox-terminal`. The first frame is `{type:"auth",ticket:"..."}`; tickets never enter URLs. Redis stores hashes and consumes atomically. Memory storage is for single-instance deployments only. Origin must match issuance and the allowed application origin. Current user, membership and session are checked again before use.

Client frames: `command` with `command`; `stdin` with base64 `data` and `encoding:"base64"`; `resize` with `cols/rows`; `interrupt`; `ping`. Server JSON frames: `ready`, `started`, `exit` with `exit_code/reason`, `error` with `code/message`, `pong`. Binary server frames carry merged PTY bytes. A disconnected console obtains a new ticket; it never reruns the previous command.

## Files And Preview

Paths are relative POSIX names under `/workspace/output`. Reject absolute paths, `..`, backslashes, NUL and control characters. A Python isolated-mode helper walks directories using descriptors and `O_NOFOLLOW`; actual operations use those descriptors, avoiding a separate realpath-check/open race. New files are fully written, synced and verified through a random staging inode before an atomic no-replace publish; failures remove the owned staging inode. Reject symlinks, devices, sockets and multiply-linked files. Listing is capped at 500 entries; reads/uploads at 8 MiB. Upload and rename cannot overwrite. Recursive deletion is excluded.

Persisted artifacts keep message-local identity; the session listing additionally exposes `message_id` and `artifact_index` through stable `(created_at,id)` cursor pages. The Agent records an output-directory baseline before each turn and persists only files created or changed during that turn, so pre-existing Workbench uploads remain live files rather than assistant artifacts. A Workbench-specific preview adapter validates content before delegating safe blobs to the common document viewer. Workbench HTML previews deny external requests and omit same-origin, images, forms, popups and top-navigation permissions.

PPTX/XLSX archives are checked before the existing viewers consume them: at most 2,048 entries, 8 MiB per expanded entry, 32 MiB total and 100,000 spreadsheet cells. External relationships, unsafe archive paths, DTDs and entities are rejected. Spreadsheet HTML and untrusted HTML render in an opaque CSP-restricted iframe. Direct PNG/JPEG/GIF/WebP previews are header-checked before browser decoding and capped at 4,096 pixels per dimension and 12 million pixels. Workbench PDF/DOCX/audio/video previews remain download-only; ordinary chat/knowledge previews keep their existing behavior. The `kind` artifact field is a display hint, with extension-based fallback for old messages; it never grants execution permission.

The original [presentation-builder Skill](../examples/skills/presentation-builder/SKILL.md) accepts bounded JSON and produces local PPTX files without external templates or downloads. Dependencies are pinned. Text runs carry explicit styles for compatibility with the browser renderer.

## Audit

Audit intent is durable before a command or file mutation starts. Audit failure refuses the action. Completion records include tenant, actor, session, execution ID, outcome, duration and exit code. Command text is never persisted; accepted records contain only `[REDACTED]` and its byte count. Stdin, environment values, tickets and provider credentials are excluded. The existing audit retention policy applies.

## Acceptance

Run tests for early output, stdin, resize, interrupt, timeout and descendant cleanup on two real backends. Verify tenant/session isolation, expired/reused tickets, invalid origins, policy revocation, unsafe paths and symlink races, output limits, durable audit, CPU/memory/wall limits, and PPTX/HTML/table previews. Protocol mocks supplement real runtime tests. Record skipped checks and provider limitations explicitly.

```bash
go test ./...
go vet ./...
go test -race ./internal/sandbox ./internal/handler ./internal/handler/session ./internal/router ./internal/types
go test -race ./internal/application/service -run '^TestWorkbench'
```

Real-runtime terminal tests are opt-in with `-tags=sandbox_terminal_integration`; file tests require `-tags=workbench_integration` and `WORKBENCH_FILES_LOCAL_INTEGRATION=1`. Their configuration targets local test backends only. Run frontend tests, type-check and build with the scripts in `frontend/package.json`.

`npm run test:workbench-browser` checks live-file and persisted-artifact renderers, external OOXML rejection, oversized-image rejection, HTML credential/network isolation and existing Office-preview compatibility. CI generates the PPTX through the bundled `presentation-builder`, starts Vite and runs these Playwright checks. Local runs may supply `PREVIEW_ARTIFACT_DIR` containing `agent-workbench-demo.pptx`, `workbench-report.html` and `backend-summary.csv`.

The full service race suite exposes pre-existing races in three test mocks (`tenant_api_key_test.go`, `tenant_sandbox_config_test.go`, `tenant_skill_catalog_test.go`), reproduced on the unchanged baseline. Workbench-specific race checks and the ordinary complete Go suite pass. The local `.runtime/README.md` records deployment, evidence paths and exact reproduction commands.
