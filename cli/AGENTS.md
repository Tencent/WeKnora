# AGENTS.md

WeKnora CLI (`weknora`) is a noun-verb wrapper around the WeKnora server API; module path `github.com/Tencent/WeKnora/cli`. This file is the developer guide for coding agents and human contributors editing the CLI. The user-facing wire contract (output shape, exit codes, error format) lives in [README.md](README.md).

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

- `cmd/<noun>/` — cobra command implementations, one subdir per noun
- `internal/cmdutil/` — `Factory`, `JSONOptions`, typed `Error`, exit-code mapping, destructive-write confirm, KB id-or-name resolve
- `internal/format/` — bare JSON emitter (`WriteJSON` / `WriteJSONFiltered`)
- `internal/iostreams/` — global IO singleton + TTY detection + `SetForTest` swap
- `internal/secrets/` — `Store` interface; `KeyringStore` primary, `FileStore` 0600 fallback, `MemStore` for tests
- `internal/prompt/` — `TTYPrompter` (huh-based, password no-echo) + `AgentPrompter` (non-TTY no-prompt sentinel)
- `internal/sse/` — `Accumulator` for chat / agent invoke SSE streams
- `internal/mcp/` — curated stdio MCP server (wired by `cmd/mcp/serve.go`)
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
3. `NewCmd<Verb>(f *cmdutil.Factory) *cobra.Command` constructor — flag registration + `cmdutil.AddJSONFlags`
4. Separate `run<Verb>(ctx, opts, jopts, svc, args...)` with the business logic — the test injection point

Key rules:

- Each command owns its own `Service` interface; do NOT share interfaces across `cmd/*` packages. Per-file dependency graph is the goal.
- Lazy-init `f.Client()` / `f.Secrets()` / `f.Prompter()` inside `RunE`, not the constructor (else `--help` forces auth).
- Required flags: `_ = cmd.MarkFlagRequired("name")` — cobra returns the error only on registration-time typo.
- New subtrees register in `cmd/root.go NewRootCmd`. Verb subtrees register their leaves in the subtree's own `NewCmd`.

### Command Examples and Help Text

Use a Go raw string with `weknora` as the example prefix. Keep one-line `Short` ≤ 70 chars; `Long` may run multi-paragraph; `Example` always includes `weknora` so copy-paste works:

```go
Example: `  weknora kb view <id>
  weknora kb view kb_abc --json
  weknora kb view kb_abc --json=id,name`,
```

### JSON Output

Add `--json` / `--jq` via `cmdutil.AddJSONFlags(cmd, fieldNames)`. In `RunE`:

```go
if jopts.Enabled() {
    return jopts.Emit(iostreams.IO.Out, result)
}
```

`Emit` is the single source for the bare-JSON contract — it honors `--json=fields,...` projection and `--jq <expr>` filtering. Never call `format.WriteJSON*` directly from a command. See `cmd/kb/list.go`.

### Destructive Writes

Commands that delete / empty / overwrite call `cmdutil.ConfirmDestructive(p, opts.Yes, jopts.Enabled(), what, id)` before mutation. In non-TTY OR `--json` mode without `-y`, it returns `CodeInputConfirmationRequired` → exit 10. See `internal/cmdutil/confirm.go`.

## Testing

### Narrow Service Fakes

Each command's `runX(ctx, opts, jopts, svc, ...)` takes its interface, not `*sdk.Client`. Tests inject plain-struct fakes:

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

