# AGENTS.md

This is the WeKnora CLI (`weknora`), a command-line client for the WeKnora RAG server. The module path is `github.com/Tencent/WeKnora/cli`.

The wire contract for AI agents *consuming* `weknora` output (JSON shape, exit codes, error format) lives in [README.md](README.md) — read that if you're integrating with the CLI binary, not modifying it.

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
- `internal/sse/` — `Accumulator` for chat / agent invoke SSE streams
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
| `auth.forbidden` | 3 | no | active context lacks permission for this resource |
| `auth.cross_tenant_blocked` | 3 | no | verify tenant context with `weknora auth status` |
| `auth.tenant_mismatch` | 3 | no | verify tenant context with `weknora auth status` |
| `input.invalid_argument` | 5 | no | see `weknora <command> --help` for valid usage |
| `input.missing_flag` | 5 | no | see `weknora <command> --help` for valid usage |
| `input.confirmation_required` | 10 | **NO automatic retry** | high-risk write - re-run with `-y/--yes` after the user explicitly approves |
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
| `local.context_not_found` | 1 | no | (no canonical hint; check `weknora context list`) |
| `local.file_io` | 1 | no | check file permissions under `$XDG_CONFIG_HOME/weknora/` |
| `local.kb_id_required` | 1 | no | run `weknora link` to bind this directory to a knowledge base, or pass `--kb` |
| `local.kb_not_found` | 1 | no | list available with `weknora kb list` |
| `local.keychain_denied` | 1 | no (system-level) | verify keyring access; falls back to file storage |
| `local.project_link_corrupt` | 1 | no | remove `.weknora/project.yaml` and run `weknora link` again |
| `local.sse_stream_aborted` | 1 | yes (rerun chat / agent invoke) | the streaming answer was cut off mid-flight; retry, or pass `--format json` to buffer the full response |
| `local.unimplemented` | 1 | no | (planned in a future release) |
| `local.upload_file_not_found` | 1 | no | verify the path is correct and readable |
| `local.user_aborted` | 1 | no (user said no) | no action taken; pass `-y/--yes` to skip the confirmation prompt |
| `mcp.readonly_mode` | 1 | no | MCP tool surface is read-only; mutations not exposed in this mode |
| `mcp.schema_unknown_command` | 1 | no | (no canonical hint) |
| `mcp.tool_not_allowed` | 1 | no | MCP tool not in the curated allowlist |

<!-- ERROR_REFERENCE_END -->

### Agent decision shortcuts

For common retry patterns, agents can hardcode:

- `network.*` → retry with exponential backoff
- `auth.token_expired` → run `weknora auth refresh`, then retry once
- `server.rate_limited` → back off (Retry-After if present) then retry
- `operation.timeout` → raise `--timeout` and retry, or surface to user
- `input.confirmation_required` → **NEVER** auto-pass `-y` without explicit user authorization
- `*.invalid_argument` / `*.missing_flag` → surface to user (don't retry)

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

Adding a tool is a deliberate API expansion — the agent-callable surface is the reason this CLI ships an MCP server, not its CLI command list, so the registration list in `registerTools` is maintained by hand.

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

- **Agent help blob (v0.6, partial)**: Commands MAY call
  `cmdutil.SetAgentHelp(cmd, cmdutil.AgentHelp{...})` to expose a stable
  JSON used_for / required_flags / examples / output shape. Activated by
  `WEKNORA_AGENT_HELP=1` at `--help` time. Currently applied to `chat`
  and `kb list` only — extending to another command requires touching
  only that command's `NewCmd`.

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
