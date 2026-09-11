# Interactive Terminal Audit design

## Baseline and scope

- Source of truth after latest-main sync: `upstream/main` at `7e53c3167164d68cfb6c2e897ddd3e755b4b5a45`.
- Main already has the provider-neutral `RemoteTerminalManager` / `RemoteTerminalSession`, current terminal ticket, authenticated WebSocket bridge, xterm UI, pinned session binding, periodic auth recheck, idle disconnect, and native `AuditLog` service/repository/UI.
- PR #3146 is open, merge-conflicting, and builds a separate `CommandTerminal`/Workbench audit stack. It remains analysis only and is not an architecture or code source for this PR.
- PR-03 audits commands entered in the current interactive PTY only. It does not create a second terminal, ticket, socket, audit store, or frontend terminal.

## Why raw keystroke reconstruction is rejected

Browser binary frames are terminal input, not shell commands. They include cursor movement, completion, paste-mode delimiters, full-screen application input, control keys, and edits that may never execute. Reconstructing a command from those bytes would produce false records and can miss shell-side expansion/editing.

The shell is the only component that knows both the command committed to history and its exit status. PR-03 therefore uses Bash prompt integration.

## Shell integration protocol

For a newly created PTY, the current `RemoteTerminalOptions.Envs` carries:

- a server-derived, session-stable marker token;
- `HISTCONTROL=` and `HISTIGNORE=` so ordinary leading-space commands remain visible to history;
- a bounded `PROMPT_COMMAND` hook.

The hook captures `$?` before running anything else. On every prompt after the initial prompt, if `HISTCMD` advanced, it obtains `builtin history 1`, base64-encodes it, and emits one private OSC frame:

```text
ESC ] 6973 ; token ; exit_status ; base64(history line) BEL
```

The token is HMAC-derived from the server JWT secret plus tenant and session IDs. It is stable across reconnects and server processes that share the configured secret, so reconnecting to an existing PTY does not leak old markers or stop auditing. It is not supplied by the browser.

The WebSocket output bridge incrementally parses markers across arbitrary provider chunks, strips them before writing PTY bytes to xterm, and forwards only authenticated, well-formed records to the audit recorder. Ordinary ANSI and Unicode bytes remain byte-for-byte unchanged. Partial frames are bounded so a malformed stream cannot grow memory without limit.

Real Docker PTY spike (Engine 29.7.2, standard sandbox image) confirmed:

- `printf '你好 audit\n'` emitted history command with status `0`;
- `false` emitted status `1`;
- `export API_TOKEN=topsecret` emitted the committed history command;
- marker output can be interleaved with bracketed-paste/prompt ANSI and split independently, so stream parsing must not assume line boundaries;
- `exit` does not reach another prompt and therefore has no prompt-completion marker.

The first login-shell run also exposed a real integration bug: the standard image prepended `weknora_set_pty_prompt` to `PROMPT_COMMAND`, and that cosmetic function returned 0, overwriting a failing command's status before the audit hook ran. The shared prompt helper now captures its incoming `$?` and returns it after setting `PS1`. A second real `bash -il` PTY run with the updated script observed `true -> 0`, `false -> 1`, preserved Unicode/ANSI output, and emitted the secret assignment for server-side redaction. The tagged Docker integration test repeats this path through Engine `ExecCreate(TTY=true)` and `ExecAttach`.

## Audit persistence

Each accepted marker becomes one existing `types.AuditLog` row:

- action: `sandbox.terminal_command`;
- tenant and actor: from the validated terminal ticket/auth context;
- target: `sandbox_session:<session id>`;
- outcome: `success` for status 0, `failed` otherwise;
- details: sanitized `command`, `exit_code`, `backend`, and `pty_id`.

The row stays unscoped so the current tenant audit feed includes it; `TargetType`/`TargetID` carry the sandbox-session identity. The command remains a structured JSON value visible/searchable in the native audit details rather than being placed in logs or a new table.

Audit writes use a small bounded worker so database latency never stalls raw PTY output. Teardown drains briefly, then cancels; overload or storage errors are logged without breaking the terminal.

## Secret redaction and bounds

The raw history value exists only long enough to decode and sanitize. Before enqueueing:

- the history sequence prefix is removed;
- control characters are normalized;
- the command is capped to a bounded UTF-8 length;
- sensitive environment assignments are replaced with `[REDACTED]`. The name run around the keyword may be empty, so bare `PASSWORD=`, `TOKEN=`, and `SECRET=` redact exactly like `API_TOKEN=`; matching a name that merely *contains* a keyword is the safe direction for a scrubber;
- sensitive CLI flag values redact when the keyword appears anywhere in the flag name, covering both `--password=` and prefixed forms such as `--http-password=` or `--db-token=`;
- `Authorization:` values redact for **any** auth scheme, not just `bearer`/`basic`, plus `X-Api-Key`/`X-Auth-Token`-style headers and URL user-info passwords. Quoted values in those headers redact too.

The raw command and marker token are never persisted or logged. Tests assert representative secret forms are absent from the serialized audit details.

This is pattern-based scrubbing, not a shell parser, and it is deliberately documented rather than over-claimed. The shape it does not cover is a keyword assignment that appears *inside* a quoted payload whose value contains whitespace: in `curl --data "my_password=my secret value"` only the first word of the value redacts and the rest of the quoted text survives. Redaction also over-matches by design: a name that merely contains a keyword, and a quoted value that reads like one, are both scrubbed.

## Reconnect and provider behavior

| Path | E2B | Cube | Docker after PR-01 |
|---|---|---|---|
| New PTY | receives audit env/hook | receives audit env/hook | receives audit env/hook through the same terminal options |
| Native reconnect | existing shell retains hook; stable token parses it | existing shell retains hook; stable token parses it | provider reports reconnect unsupported and creates a new audited shell, matching its real capability |
| Output | existing provider stream, marker filtered in shared WS bridge | same | same |
| Exit status | command status comes from Bash prompt hook; final shell exit remains current terminal exit event | same | same |

No adapter-specific audit implementation is required.

## Expected production changes

- `internal/types/audit_log.go`: register the native audit action.
- `internal/application/service/sandbox_terminal_ticket.go`: derive the stable private marker token with existing server auth secret.
- `internal/handler/session/handler.go`: inject the existing `AuditLogService` into the session handler.
- `internal/handler/session/sandbox_terminal_audit.go`: prompt environment, incremental marker filter, command sanitization, bounded audit writer.
- `internal/handler/session/sandbox_terminal_ws.go`: attach the audit env and recorder to the current terminal open.
- `internal/handler/session/sandbox_terminal_bridge.go`: filter provider output and close the recorder with the bridge lifecycle.
- audit action registry/locales and the current tenant audit summary renderer: display the new action and sanitized command.

## Explicit non-changes

- no `RemoteTerminalManager` or provider adapter redesign;
- no new WebSocket, ticket type, terminal UI, or session binding;
- no raw-xterm input parser;
- no `CommandTerminal`;
- no schema migration or second audit repository;
- no Files, limits, ArtifactKind, or Presentation Skill work.

## Test plan

- marker extraction across every chunk boundary, multiple markers, malformed/base64-invalid/oversized frames, wrong token, ANSI and Unicode preservation, and final flush;
- initial prompt suppression, command history prefix removal, exit-code outcome mapping, reconnect-stable token;
- secret redaction for assignments, flags, Authorization/Bearer values, URL user-info, control characters, and length bounds;
- audit queue bounds, immutable native row fields, writer cancellation/close, and storage-error isolation;
- bridge tests prove markers never reach WebSocket output and valid commands reach the audit sink;
- real Docker PTY integration verifies a successful command, failing command, Unicode/ANSI output, secret redaction, exit status, and clean terminal lifecycle;
- gofmt, focused Go tests, frontend locale tests/type-check, real Docker integration, diff-scoped lint, `git diff --check`, self-review, latest-main sync, and affected-test rerun.

## Known limitation expressed honestly

This is Bash prompt integration, not kernel process accounting. A user can deliberately disable or replace `PROMPT_COMMAND`/history, inspect the marker token available to their own shell, or forge a marker; the token prevents accidental marker interpretation and browser-selected tokens, not a hostile same-user shell. A command that terminates the shell (`exit`, `exec`, crash) cannot emit a subsequent prompt marker. The audit row is evidence emitted by the interactive shell, not a tamper-proof record of every process spawned inside the sandbox. That limitation is preferable to falsely claiming raw keystrokes are executed commands.
