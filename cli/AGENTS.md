# AGENTS.md

This is the WeKnora CLI (`weknora`), a command-line client for the WeKnora RAG server. The module path is `github.com/Tencent/WeKnora/cli`.

The wire contract for AI agents *consuming* `weknora` output (JSON shape, exit codes, error format) is documented below and in [README.md](README.md). Read this file if you're integrating with the CLI binary — build / test / architecture details follow the wire contract sections.

## Wire contract for AI agents

This CLI's primary consumers include AI agents (Claude Code, Cursor, Gemini CLI,
etc.). Output format is the agent-facing API. **Every error message and every
JSON field you write becomes part of an agent's decision-making input.**

### Stdout (success path)

All `--format json` (default) commands emit a symmetric envelope. Optional
fields are `omitempty` — they only appear when populated:

```json
// list (kb list, doc list, ...) — data is an array, meta carries count
{
  "ok": true,
  "data": [ {"id": "kb_abc", "name": "prod"} ],
  "meta": {"count": 1},
  "profile": "prod"
}

// single resource (kb view, doc view, ...) — data is an object
{
  "ok": true,
  "data": {"id": "kb_abc", "name": "prod", "description": "..."},
  "profile": "prod"
}

// mutation success with no payload (some delete / edit paths)
{"ok": true, "profile": "prod"}
```

`data` is omitted on mutation-only success (no payload). `meta` carries list
counters (`count`, `has_more`) and batch successes/failures, and is omitted
when empty. `meta.next_cursor`, `meta.total_count`, and `meta.request_id` are
reserved — not currently populated; planned for v0.8 when the SDK exposes
pagination cursors and response headers. `_notice` is reserved — open-map
infrastructure is in place for deprecation / version_skew / security notices;
the field is omitted until a producer is wired in v0.8. `profile` echoes the
resolved profile name and is omitted when no profile is configured.

### Stderr (error path)

Errors emit an error envelope on stderr (`--format json`) or prose
`code: message\nhint: ...\nretry: ...` (`--format text`):

```json
{
  "ok": false,
  "error": {
    "type": "auth.unauthenticated",
    "message": "fetch current user: HTTP error 401",
    "hint": "run `weknora auth login`",
    "retry_command": "weknora auth login --host https://kb.example.com",
    "retry_after_seconds": 0,
    "risk": {"level": "destructive", "action": "noun.verb"},
    "detail": {}
  },
  "_notice": {}
}
```

`type` is the typed code (see [Error code reference](#error-code-reference)
below). `hint` is prose; `retry_command` is the suggested next argv (single
shell-escaped string). For non-destructive errors agents may execute it; on
exit-10 (`input.confirmation_required`) it is informational only — the human
must approve the destructive write explicitly. See "Exit-10 anti-patterns" for
details. Note: tokens in `retry_command` are built via `fmt.Sprintf` with
user-supplied IDs unquoted — callers that auto-execute must shell-quote each
token (emitting as a JSON array is planned for v0.8).
`retry_after_seconds` mirrors HTTP `Retry-After`. `risk` tags high-risk writes.
`detail` carries structured per-error context (e.g. `unknown_subcommand`'s
`available[]` list).

### NDJSON event stream (chat / session ask)

`--format json` and `--format ndjson` both produce one JSON event per line —
no envelope wrapping. The CLI injects exactly one event (`init`) at the head;
all subsequent events pass through verbatim from the SDK:

```
{"type":"init","session_id":"...","kb_id":"...","profile":"...","agent_id":"..."}
{"type":"thinking","content":"..."}
{"type":"answer","content":"Hello"}
{"type":"tool_call","name":"...","input":{}}
{"type":"complete","done":true}
```

For prose rendering, pass `--format text`.

### `_notice` evolution policy

`_notice` is an open map. New keys are **additive non-breaking**; agents MUST
ignore unknown keys. v0.7 reserves three keys: `deprecation` / `version_skew` /
`security`. New keys follow snake_case convention. The `_notice` field is
currently always empty — producer wiring is planned for v0.8 when the SDK
exposes version metadata. The wire infrastructure is in place so adding a
producer in v0.8 will not change the envelope shape.

### CLI vs server SDK contract boundary

CLI 1.0 contract covers:
- ✅ Envelope wire shape (success + error)
- ✅ CLI-injected events (`init` only)
- ✅ NDJSON line shape (bare `{type:...,...}`)
- ✅ Passthrough discipline (CLI doesn't rename SDK events)

CLI 1.0 contract does NOT cover:
- ❌ Specific SDK event names (`answer` / `tool_call` / `complete` / ...)
- ❌ SDK event field shapes (server's own version contract)

The CLI contract and the server's wire contract are versioned independently:
agents pin against the CLI surface, while server-side event shape evolves on
its own track.

### The one rule

Every error message you write will be parsed by an AI to decide its next action.
Make errors structured, actionable, and specific.

### Environment variables

| Variable | Purpose |
|---|---|
| `WEKNORA_PROFILE` | Active profile name. Equivalent to the global `--profile <name>` flag; overridden by `--profile`. Useful in CI scripts that cannot pass global flags. |
| `WEKNORA_FORMAT` | Default `--format` value (`text \| json \| ndjson`). Overridden by explicit `--format`. Invalid values ignored silently. |
| `WEKNORA_KB_ID` | Default KB ID for commands that accept `--kb`. Overridden by `--kb`. |
| `WEKNORA_LOG_LEVEL` | SDK debug log level (`error \| warn \| info \| debug`). Overridden by `--log-level`. |
| `WEKNORA_AGENT_HELP` | Set to `1` to emit structured JSON agent-help (machine-readable) instead of human help text when `--help` is invoked. |

> **Note for agents — machine-readable version and help:** The `--version` flag
> and `--help` flag on the root command bypass `--format json` and always emit
> prose (cobra built-in paths). For machine-readable output use the `version`
> subcommand (`weknora version --format json`) or `WEKNORA_AGENT_HELP=1
> weknora <cmd> --help`. Planned fix for v0.8.

## Design decisions worth flagging

Five design decisions readers may want context on: where WeKnora picks an
opinionated default, what the trade-off is, and what mainstream practice it
is or isn't aligned with.

### 1. Channel split: success → stdout, error → stderr

| | |
|---|---|
| **WeKnora** | success envelope → stdout; error envelope → stderr |
| **Rationale** | `weknora ... --format json \| jq '.data[]'` must not mix error objects into the data stream. Channel split lets pipeline consumers suppress errors with `2>/dev/null` and still get clean JSON on stdout. |

### 2. `weknora api DELETE` triggers exit-10 confirmation

| | |
|---|---|
| **WeKnora** | DELETE triggers exit-10 (`input.confirmation_required`); user bypasses with `-y/--yes` |
| **Rationale** | DELETE is irreversible. Most raw-API CLI commands rely on restricted credentials for safety, but self-hosted deployments may not have restricted-credential infrastructure available. Defensive default because agents are common consumers. |

### 3. `retry_command` distinct from `hint`

| | |
|---|---|
| **WeKnora** | two separate fields: `retry_command` (suggested next argv, directly-executable for non-destructive errors; informational only on exit-10) + `hint` (prose) |
| **Rationale** | Agents don't regex-extract argv from prose — known fragility. Trade-off: one extra envelope field. On exit-10, the user must approve the destructive write; agents surface `retry_command` for human review, not auto-execution. |

### 4. NDJSON event stream has no envelope wrapping

| | |
|---|---|
| **WeKnora** | streaming commands (`chat`, `session ask`) emit bare `{type:...}` per line; no envelope |
| **Rationale** | This matches established practice across NDJSON-emitting CLIs and webhook protocols. A streaming envelope requires unwrap before dispatch — net burden with no benefit. |

### 5. No `schema_version` field in payload

| | |
|---|---|
| **Mainstream** | some APIs (Anthropic / OpenAI) embed a `version` field in payload |
| **WeKnora** | version identity via CLI binary semver + CHANGELOG `### BREAKING` + skill `tested_against` + CI parity tests |
| **Rationale** | Mainstream CLIs don't embed version in payload. Agents have complete version awareness via `weknora --version` and skill version binding. |

## Pre-1.0 breaking policy

CLI is in `v0.x` pre-release. Breaking changes ship together in concentrated
batches (v0.5 / v0.6 / v0.7) rather than scattered across patch releases.
After 1.0, any breaking change requires a 2-version deprecation period.

`CHANGELOG.md` `### BREAKING` section is authoritative.

For agents: pin the CLI version in your skill's `tested_against` field; bump
`tested_against` only after manually validating against the new CLI.

## Build, Test, and Lint

```bash
go build -o weknora .                              # build (from cli/)
go test -count=1 ./...                             # unit + contract tests
go test -run TestFoo ./internal/format/            # single test
go test ./acceptance/contract/ -args -update       # refresh wire goldens
go test -tags acceptance_e2e ./acceptance/e2e/...  # live-server e2e (gated by env)
go vet ./...
```

Both `go test -count=1 ./...` and `go vet ./...` must pass before committing.

## Architecture

Entry point: `cmd/main.go` → `cmd.Execute()` → `cmd.NewRootCmd(cmdutil.New())`.

Key packages:

- `cmd/<name>/` — cobra command implementations, one subdir per top-level command
- `internal/cmdutil/` — `Factory`, `FormatOptions`, typed `Error`, exit-code mapping, destructive-write confirm, KB id-or-name resolve
- `internal/format/` — bare JSON emitter (`WriteJSON` / `WriteJSONFiltered`)
- `internal/iostreams/` — global IO singleton + TTY detection + `SetForTest` swap
- `internal/secrets/` — `Store` interface; `KeyringStore` primary, `FileStore` 0600 fallback, `MemStore` for tests
- `internal/prompt/` — `TTYPrompter` (password no-echo) + `AgentPrompter` (non-TTY no-prompt sentinel)
- `internal/sse/` — `Accumulator` for chat / session ask SSE streams
- `internal/mcp/` — curated 10-tool stdio MCP server (wired by `cmd/mcp/serve.go`); see [MCP tool surface](#mcp-tool-surface) for the curation rationale and inventory
- `client/` (parent module) — generated SDK

## Command Structure

A command `weknora foo bar` lives in `cmd/foo/bar.go` with `bar_test.go`.

### Canonical Examples

- **Command + tests**: `cmd/kb/list.go` and `list_test.go`
- **Destructive write + confirm protocol**: `cmd/kb/delete.go`
- **SSE streaming command**: `cmd/chat/chat.go`
- **Factory wiring**: `internal/cmdutil/factory.go`

### The Options + Narrow Service Pattern

Every command follows this structure (see `cmd/kb/list.go`):

1. `Options` struct with flag-bound fields
2. `Service` interface declaring only the SDK methods this command calls. `*sdk.Client` satisfies it implicitly via duck typing.
3. `NewCmd<Verb>(f *cmdutil.Factory) *cobra.Command` constructor — flag registration + `cmdutil.AddFormatFlag`
4. Separate `run<Verb>(ctx, opts, fopts, svc, args...)` with the business logic — the test injection point

Key rules:

- Each command owns its own `Service` interface; do NOT share interfaces across `cmd/*` packages. Per-file dependency graph is the goal.
- Lazy-init `f.Client()` / `f.Secrets()` / `f.Prompter()` inside `RunE`, not the constructor (else `--help` forces auth).
- Required flags: `_ = cmd.MarkFlagRequired("name")` — cobra returns the error only on registration-time typo.
- New subtrees register in `cmd/root.go NewRootCmd`. Verb subtrees register their leaves in the subtree's own `NewCmd`.

### Command Examples and Help Text

Use a Go raw string with `weknora` as the example prefix. Keep one-line `Short` ≤ 70 chars; `Long` may run multi-paragraph; `Example` always includes `weknora` so copy-paste works:

```go
Example: `  weknora kb view <id>
  weknora kb view kb_abc --format json
  weknora kb view kb_abc --format json --jq '{id, name}'`,
```

### JSON Output

Add `--format` / `--jq` via `cmdutil.AddFormatFlag(cmd, fieldNames...)`. In `RunE`:

```go
fopts, err := cmdutil.CheckFormatFlag(c)
if err != nil { return err }
fopts.ResolveDefault(iostreams.IO.IsStdoutTTY())
// ...
if fopts.WantsJSON() {
    return fopts.Emit(iostreams.IO.Out, result)
}
```

`Emit` is the single source for the bare-JSON contract — it honors `--format json|ndjson` and `--jq <expr>` filtering. Never call `format.WriteJSON*` directly from a command. See `cmd/kb/list.go`.

### Destructive Writes

Commands that delete / empty / overwrite call `cmdutil.ConfirmDestructive(p, opts.Yes, fopts.WantsJSON(), what, id)` before mutation. In non-TTY OR JSON-output mode without `-y`, it returns `CodeInputConfirmationRequired` → exit 10. See `internal/cmdutil/confirm.go`.

## Testing

### Narrow Service Fakes

Each command's `runX(ctx, opts, fopts, svc, ...)` takes its interface, not `*sdk.Client`. Tests inject plain-struct fakes:

```go
type fakeBarSvc struct {
    gotID string
    resp  *sdk.Bar
    err   error
}
func (f *fakeBarSvc) GetBar(_ context.Context, id string) (*sdk.Bar, error) {
    f.gotID = id
    return f.resp, f.err
}
```

No mocking library; the narrow-interface design makes fakes 5 lines each.

### IOStreams in Tests

```go
out, errBuf := iostreams.SetForTest(t)  // bytes.Buffer sinks, non-TTY
ios, _ := iostreams.SetForTestWithTTY(t) // simulate terminal
```

### Confirm Prompts

Use `testutil.ConfirmPrompter{Answer: bool, Err: error}` from `internal/testutil/`. Single source for the prompt double — do NOT re-define `confirmPrompter` per package.

### Assertions

Use `testify`. Prefer `require` (not `assert`) for error checks so the test halts immediately, and `assert` for value comparisons:

```go
require.NoError(t, err)
require.ErrorAs(t, err, &typed)
assert.Equal(t, "expected", actual)
```

### Acceptance: Wire-Shape Goldens

`acceptance/contract/wire_test.go` drives the in-process cobra tree against `httptest.Server` fixtures and compares stdout to `acceptance/testdata/wire/<case>.json`. Error-path cases also assert stderr contains the typed code substring (e.g. `auth.unauthenticated`). Update goldens with `go test ./acceptance/contract/ -args -update`.

### Table-Driven Tests

Use for flag validation, error classification, parser edge cases. See `internal/cmdutil/exit_test.go` and `cmd/kb/list_test.go`.

```go
tests := []struct{ name string; ...}{
    {name: "descriptive case", ...},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) { /* arrange, act, assert */ })
}
```

## Code Style

- Add godoc to every exported function, type, and constant. Explain *why*, not *what* — the name already says *what*.
- Don't comment to restate the code. Delete comments that narrate the next line.
- Don't reference task numbers, commit SHAs, or version tags in inline comments — they belong in CHANGELOG or git log.
- Never paste em-dashes (—) into Go source; use ASCII `-` or rewrite. (Markdown docs may use em-dashes.)
- Don't add a helper for a single caller — inline.

## Error Handling

Typed error helpers in `internal/cmdutil/errors.go`:

- `cmdutil.NewError(code, msg)` — fresh typed error
- `cmdutil.WrapHTTP(err, format, args...)` — wrap an SDK error + classify from HTTP status (404 → `resource.not_found`, 401 → `auth.unauthenticated`, …). Use at every SDK call site.
- `cmdutil.Wrapf(code, err, format, args...)` — explicit wrap with a chosen code
- `cmdutil.NewFlagError(err)` — flag / argument problem → exit 2
- `cmdutil.SilentError` — exit 1 without printing (when output already emitted)
- `cmd.MarkFlagsMutuallyExclusive("a", "b")` — cobra-level mutex

Errors print to STDERR via `cmdutil.PrintError(w, err)` as `code: msg\nhint: ...`. STDOUT stays bare JSON or empty on failure, so `--json | jq` pipelines never have to filter error shapes.

User-facing exit-code mapping lives in [README.md "Exit codes"](README.md#exit-codes). When adding a new `ErrorCode` constant, also append to `AllCodes()` so the acceptance contract picks it up.

## Error code reference

> **Audience:** AI agents and scripted callers parsing `weknora` stderr.
> Code authors writing new error sites — see [`## Error Handling`](#error-handling) above.

When `weknora` exits non-zero, stderr carries a structured triplet:

```
<code>: <message>
hint: <actionable next step>
```

Agents parse the first colon to extract the typed code. The exit code class (see [`README.md` "Exit codes"](README.md#exit-codes)) controls retry / surface decisions; the typed code disambiguates within a class.

<!-- ERROR_REFERENCE_START -->
<!-- DO NOT EDIT manually below this marker. Add new codes to
     internal/cmdutil/errors.go + register in AllCodes() + add a row here.
     The CI scan in errors_doc_test.go enforces parity. -->

| Code | Exit | Retryable | Default hint |
|---|---|---|---|
| `auth.unauthenticated` | 3 | no (run `auth login`) | run `weknora auth login` |
| `auth.token_expired` | 3 | yes (after refresh) | your session expired; run `weknora auth login` to re-authenticate |
| `auth.bad_credential` | 3 | no (re-login) | run `weknora auth login` |
| `auth.forbidden` | 3 | no | active profile lacks permission for this resource |
| `auth.cross_tenant_blocked` | 3 | no | verify tenant context with `weknora auth status` |
| `auth.tenant_mismatch` | 3 | no | verify tenant context with `weknora auth status` |
| `input.invalid_argument` | 5 | no | see `weknora <command> --help` for valid usage |
| `input.missing_flag` | 5 | no | see `weknora <command> --help` for valid usage |
| `input.confirmation_required` | 10 | **NO automatic retry** | high-risk write - re-run with `-y/--yes` after the user explicitly approves |
| `input.unknown_subcommand` | 5 | no | invocation reached a command path with no matching subcommand. Detail includes `available` list; retry with `<path> --help`. |
| `resource.not_found` | 4 | no | verify the resource ID and try again |
| `resource.already_exists` | 1 | no | use a different name or fetch the existing resource |
| `resource.locked` | 1 | maybe (transient lock) | (no canonical hint; check resource state) |
| `server.error` | 7 | yes (with backoff for 5xx) | (no canonical hint) |
| `server.timeout` | 7 | yes (with backoff) | request timed out; retry, or run `weknora doctor` to check connectivity |
| `server.rate_limited` | 6 | yes (back off, then retry) | rate-limited; retry after a few seconds |
| `server.session_create_failed` | 1 | yes (with backoff) | could not create a chat session; pass `--session` to reuse an existing session |
| `server.incompatible_version` | 7 | no (upgrade required) | run `weknora doctor` to see version skew details |
| `network.error` | 7 | yes (with backoff) | check base URL reachability with `weknora doctor` |
| `operation.timeout` | 124 | yes (raise `--timeout`) | wait timed out; raise `--timeout` or check the underlying job |
| `operation.failed` | 1 | no (target reached terminal failure) | one or more targets reached a terminal failure (e.g. doc parse_status=failed) |
| `operation.cancelled` | 1 (main overrides to 130) | no | command interrupted by SIGINT / SIGTERM. The typed code maps to exit 1, but `main` raises the exit to 130 when the root context was signal-cancelled so the user-visible exit follows Unix signal convention. |
| `local.config_corrupt` | 1 | no (manual fix) | remove `~/.config/weknora/config.yaml` and re-run `weknora auth login` |
| `local.profile_not_found` | 1 | no | (no canonical hint; check `weknora profile list`) |
| `local.file_io` | 1 | no | check file permissions under `$XDG_CONFIG_HOME/weknora/` |
| `local.kb_id_required` | 1 | no | run `weknora link` to bind this directory to a knowledge base, or pass `--kb` |
| `local.kb_not_found` | 1 | no | list available with `weknora kb list` |
| `local.keychain_denied` | 1 | no (system-level) | verify keyring access; falls back to file storage |
| `local.project_link_corrupt` | 1 | no | remove `.weknora/project.yaml` and run `weknora link` again |
| `local.sse_stream_aborted` | 1 | yes (rerun chat / session ask) | the streaming answer was cut off mid-flight; retry, or pass `--format json` to buffer the full response |
| `local.unimplemented` | 1 | no | (planned in a future release) |
| `local.upload_file_not_found` | 1 | no | verify the path is correct and readable |
| `local.user_aborted` | 1 | no (user said no) | no action taken; pass `-y/--yes` to skip the confirmation prompt |
| `mcp.readonly_mode` | 1 | no | MCP tool surface is read-only; mutations not exposed in this mode |
| `mcp.schema_unknown_command` | 1 | no | (no canonical hint) |
| `mcp.tool_not_allowed` | 1 | no | MCP tool not in the curated allowlist |

<!-- ERROR_REFERENCE_END -->

### AI agent decision shortcuts

For common retry patterns, AI agents can hardcode:

- `network.*` → retry with exponential backoff
- `auth.token_expired` → run `weknora auth refresh`, then retry once
- `server.rate_limited` → back off (Retry-After if present) then retry
- `operation.timeout` → raise `--timeout` and retry, or surface to user
- `input.confirmation_required` → **NEVER** auto-pass `-y` without explicit user authorization
- `*.invalid_argument` / `*.missing_flag` → surface to user (don't retry)

## Exit-10 anti-patterns

Exit code 10 (`input.confirmation_required`) marks a destructive write where the
CLI refused to proceed without explicit user approval. The retry envelope includes
`retry_command` showing the exact argv that would proceed. AI agents must NEVER
auto-retry this exit code — every exit 10 is a user-in-the-loop decision.

**Don't do these:**

1. **Auto-add `-y/--yes` and retry.** The flag exists for the user, not the agent.
   Surface the exit-10 envelope to the user verbatim and wait for explicit go-ahead.

2. **Parse the retry_command and run it.** The retry_command is *informational* --
   showing what *would* execute. Running it without user input collapses two steps
   the user is supposed to see.

3. **Wrap the call in a retry-with-backoff loop.** Exit 10 is not transient. It's
   a "this needs human approval" signal, not a transient transport error.

4. **Treat exit 10 as a generic error and fall back to a less-destructive verb.**
   The user asked for the destructive verb. If they want something else they'll
   say so. Don't substitute.

5. **Auto-add `-y` because the *previous* exit-10 was approved.** Each invocation
   stands on its own. The user's prior approval doesn't extend to similar calls.

6. **Skip the prompt by switching to `--format json`.** JSON mode still emits
   `input.confirmation_required` (just in envelope form). It's the same gate.

## Stream recovery

The `weknora session continue-stream <session-id> --message <msg-id>` command resumes an SSE event stream for an existing assistant message. Use cases: network-blip recovery, long-running agent invocation polling, completed-stream inspection.

### Server semantics: replay-from-0, not cursor-resume

The server **replays all stored events from the start** of the assistant message, then **tails new events**. This is NOT a cursor-from-disconnect resume. Agents reconnecting mid-stream will receive ALL previously-emitted events again.

### Agent contract

1. **Dedupe by message_id** (or maintain a per-message event hash set). Naively processing all received events causes duplicate side effects (re-running tool calls, re-rendering answers).
2. **Capture message_id from the init event** of the original `chat` or `session ask` invocation — the CLI injects `{"event":"init", "session_id":"...", "message_id":"..."}` as the first NDJSON line.
3. **Handle `local.sse_stream_aborted` typed error**: server-side buffer expired (TTL exceeded) or process restarted (memory mode). The message is no longer recoverable; restart the original query.

### Server-side buffer TTL

| Mode | TTL |
|---|---|
| `STREAM_MANAGER_TYPE=redis` | **1 hour** (server-side; not configurable from the CLI) |
| `STREAM_MANAGER_TYPE=memory` (default) | **Process lifetime** (server restart = data loss; no explicit cleanup logic) |

After TTL, `weknora session continue-stream` returns the typed error `local.sse_stream_aborted`, which maps to exit code 1 per the Error code reference.

## Dry-run contract

The `--dry-run` flag is available on every mutation cobra command (`kb create/edit/delete`, `agent create/edit/delete`, `doc create/upload/fetch/delete`, `chunk delete`, `session delete`, `auth refresh/logout`, `link/unlink`, `profile add/remove`) and on `weknora api` (POST/PUT/PATCH/DELETE only; GET rejected with FlagError exit 2).

### Envelope shape on dry-run

Success envelope (exit 0) with `data` field omitted (omitempty) and `meta.dry_run=true`:

```json
{"ok":true,"meta":{"dry_run":true,"plan":{"action":"kb.create","args":{"name":"foo","description":"bar"}}},"profile":"prod"}
```

`api` command additionally includes `method` / `path` / `body` in plan:

```json
{"ok":true,"meta":{"dry_run":true,"plan":{"action":"api.post","method":"POST","path":"/api/v1/knowledge-bases","body":{"name":"foo"}}}}
```

### Side effects suppressed

The dry-run path is **offline** — no SDK calls, no Factory.Client() init, no ResolveKB network query, no writes (keyring / `.weknora/project.yaml` / files). Works without active profile or network access.

### Interactions

| Combination | Behavior |
|---|---|
| `--dry-run` (destructive cmd, no `-y`) | NO exit-10; emits plan + exit 0 (preview implies no execution, so the confirmation gate is irrelevant) |
| `--dry-run` + `-y` | Equivalent to single `--dry-run`; `-y` is no-op (dry-run early-exits before ConfirmDestructive) |
| `--dry-run` + `api -X GET` (or default GET) | FlagError exit 2: "--dry-run requires explicit -X POST/PUT/PATCH/DELETE; default GET is read-only with no side effect to preview" |
| `--dry-run` + `--jq <expr>` | jq applied to envelope output normally |
| `--dry-run` + `kb edit my-kb` | plan.args contains user raw input (NOT ResolveKB-resolved); agent verifies kb name correctness |
| `--dry-run` + fetch-then-update (`kb edit / agent edit`) | plan.args contains user-explicit fields ONLY; agent infers server-side fetch-then-update preserves unmentioned fields |
| `--dry-run` + body containing secrets (`--input` payload) | **plan.body echoes the full body to stdout** so the agent can verify what would be sent; avoid piping secret-bearing bodies through dry-run for inspection |

### Streaming commands explicitly excluded

`session ask` and `chat` do NOT support `--dry-run` (streaming and dry-run have a semantic mismatch). For prompt-formation preview, use:

```bash
echo '{"query":"...","kb":"..."}' | weknora api -X POST /api/v1/sessions/<id>/agent-qa --input - --dry-run
```

## Risk metadata

Agents see the same `risk.action` string (in the form `noun.verb`) on three independent surfaces:

1. **Error envelope** — `envelope.error.risk.action` on an exit-10 confirmation-required error, so the agent can decide whether to escalate to the user. 11 unique values: `kb.delete`, `kb.edit`, `agent.delete`, `agent.edit`, `doc.delete`, `doc.delete_all`, `session.delete`, `chunk.delete`, `profile.remove`, `auth.logout`, `api.delete`.

2. **Help text** — a `Risk: <action> (destructive)` line prepended to the top of `--help` output on the 9 destructive cobra commands. `weknora api` is intentionally excluded: it is a generic HTTP passthrough whose risk depends on the method, so a static Risk: line would mislead for non-DELETE methods.

3. **MCP tool annotations** — `Tool.Annotations.destructiveHint` / `readOnlyHint` / `idempotentHint` / `openWorldHint` on every tool returned by `weknora mcp serve`.

The three surfaces do not auto-sync: each is wired separately so agents that only consume one surface still get the signal, but contributors adding a new destructive command must touch all three.

## MCP Tool Surface

WeKnora's MCP server exposes a curated read-only tool surface. Many MCP servers in the wild ship write / mutation operations on by default and rely on credential-scope or sandbox restrictions for safety. WeKnora opts for curation instead: the server side doesn't yet enforce per-token scope, so an agent holding a user's token has full write access. Until server-side scope ships, the CLI keeps mutation tools out of the MCP surface as a belt-and-braces second line of defense. When server scope arrives this stance can loosen.

The curated 10 tools (`cli/internal/mcp/tools.go`):

| Tool | Purpose |
| --- | --- |
| `kb_list` | list knowledge bases |
| `kb_view` | fetch a knowledge base by id |
| `doc_list` | list documents in a kb (paginated, status-filterable) |
| `doc_view` | fetch a document by id |
| `doc_download` | download raw bytes (1 MiB cap, base64 for binary) |
| `chunk_list` | list chunks of a document for RAG retrieval debug |
| `search_chunks` | hybrid (vector + keyword) retrieval |
| `chat` | stream a RAG answer; auto-creates a session if absent |
| `agent_list` | list custom agents |
| `agent_invoke` | run a query through a custom agent |

Adding a tool is a deliberate API expansion — the AI-agent-callable surface is the reason this CLI ships an MCP server, not its CLI command list, so the registration list in `registerTools` is maintained by hand.

## Command surface design SOP

Before specifying any CLI command, do this in order:

1. `grep -A 50 "type Foo struct" client/foo.go` — dump SDK request/response schemas.
2. List every field with type and source line.
3. For each field, decide: hot-path flag / config-file only / hidden / never-expose.
4. Cross-check pagination signatures: an SDK `(ctx, id, page, pageSize)` shape demands `--limit` + `--all-pages` + `--page-size` on the CLI side.
5. ONLY THEN consult mainstream CLI conventions to choose flag names, positionals, mutex, and confirm semantics.
6. Decide which fields are "top use case" (flag) / "advanced" (`--config-file` or escape hatch via `weknora api`). Don't try to flag-cover every SDK field — mature CLIs that curate ship a tighter surface; CLIs that 1:1 mirror their API pay the UX cost.

Rationale: earlier drafts produced three categories of schema errors — fields that didn't exist on the underlying SDK, wrong field counts in user-facing docs, and missing pagination flags — that all stemmed from "design from convention, not from SDK." The fix is canonical: the SDK schema is the ground truth; convention decides names and shapes around that ground truth.

## CRUD command flag conventions

CRUD commands follow the **hard-required-flags** pattern: every required input is a flag or positional, and a missing one yields an immediate `input.invalid_argument` exit. The contrast is **TTY-prompts-fill**, where missing input opens an interactive prompt; that pattern is reserved for `auth login` (the one command where a human must be at the terminal).

Required-input idioms in this codebase:

- Positional required: `cobra.ExactArgs(N)` or `cobra.MinimumNArgs(1)`
- Flag required: `cmd.MarkFlagRequired("flag")`
- Custom required (e.g., `agent edit` needs at-least-one-edit-flag): RunE-level validation that returns `input.invalid_argument`
- Mutex: `cmd.MarkFlagsMutuallyExclusive("a", "b")`

Reasons hard-required-flags is the v0.5+ default:

- Admin / debug commands have no natural human-interactive prompt to lean on.
- Agent-friendly: MCP callers do not stall waiting for stdin prompts.
- Consistent with every existing non-auth WeKnora command.

- **Agent help blob**: Commands MAY call
  `cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{...})` to expose a stable
  JSON used_for / required_flags / examples / output / warnings shape.
  Activated by `WEKNORA_AGENT_HELP=1` at `--help` time. Warnings are
  always rendered in human help (stderr, not env-gated). Applied to
  `chat`, `kb list`, `session ask`, and all destructive commands.
  Extending to another command requires touching only that command's `NewCmd`.

## Status / check verb pair pattern

When a resource has both a cheap "is it alive?" probe and a deeper
"verify its dependencies / aggregate state" probe, expose them as two
verbs so the verb itself communicates cost:

- `status <id>` — single HTTP, returns reachable + cheap fields.
- `check  <id>` — 1 + N HTTP, adds derived state that needs follow-up
  calls (e.g., aggregating `failed_count` via doc-list page-walk,
  probing every KB in an agent's scope).

Current pairs: `kb status` / `kb check`, `agent status` / `agent check`.
The deep verb's `Long` help text must enumerate the extra HTTP calls so
cost is predictable.

## Long-poll wait commands

`doc wait <doc-id> [<doc-id>...]` is the model for any future
`wait` command:

- Always wait-all on multi-target (no fail-fast flag); compose in shell
  (`wait id1 && wait id2`) when fail-fast is needed.
- Exponential backoff with jitter (initial `--interval`, cap 15s).
- Concurrency capped (5 in flight); large fan-out via `xargs -P`.
- Exit-code priority: failed (1) > timeout (124) > completed (0). The
  failed bucket is `operation.failed`, not `server.error` — a target's
  own terminal failure is not a transient transport issue.
- Validate `--format` / `--jq` before polling so an invalid flag does
  not cost the caller a multi-minute poll.
