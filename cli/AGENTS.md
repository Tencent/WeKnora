# Agent Integration Guide for `weknora` CLI

> **Scope.** This file is an **operational reference** for LLM agents
> (Claude Code, Cursor, Codex, Aider, Gemini Coder, etc.) that **invoke
> `weknora` on a user's behalf**. It documents the wire shape, exit code,
> and behavioral conventions an agent integration relies on.
>
> This is **not** a contributor guide. If you are an AI coding agent
> editing weknora's source, see the repo root `README.md` (and, if added
> later, a separate contributor `AGENTS.md` at the repo root).
>
> **Naming note.** "Agent" appears in two distinct WeKnora contexts:
>
> - **This file (`AGENTS.md`) + the `agent_help` annotation on each
>   command's `--help`**: documents the contract for AI coding agents
>   (you, the LLM-driven CLI consumer).
> - **The `weknora agent` subtree** (`agent list / view / invoke`):
>   manages WeKnora's first-class *Custom Agent* resources — server-side
>   records (system prompt + model + allowed tools + KB scope) that the
>   user authored in the web UI. `agent invoke` calls the agent's
>   configured workflow against a query; it is **not** how you, the AI
>   coding agent, drive WeKnora — that's `kb` / `doc` / `search` / `chat`
>   / `mcp serve`.

`weknora` is designed to be agent-friendly: error messages, output
format, and flag design follow conventions agents can rely on.
Wire-contract breaking changes are flagged in their PR description and
the corresponding `weknora --version` bump — agents should pin a
known-good version and re-validate against `--help` output on upgrade.

The "Output contract" and "Behavioral rules" sections below are the
self-contained specification of the wire format; everything an integrator
needs is in this document.

---

## Output contract

### Streams

- **stdout** is the data channel: bare JSON (with `--json`) or
  human-formatted output (without `--json`). Never carries error text.
- **stderr** is logs / progress / warnings / errors / agent guidance
  footnotes. On failure, the error message + actionable hint go here.

A non-empty stderr does **not** mean failure — read the exit code instead.

### JSON output (bare data)

When `--json` is set on a successful command, stdout contains exactly
one JSON value matching the resource the command produces:

| Command shape | stdout JSON |
|---|---|
| `list` / `search` | `[ { …resource… }, … ]` (bare array; `[]` when empty) |
| `view` / `create` / `edit` | `{ …resource… }` (bare object) |
| `delete` / `pin` / write ops | `{ id, …action-result fields… }` (bare object) |
| `doctor` | `{ summary: { all_passed, passed, warned, failed, skipped }, checks: [ … ] }` |

There is no `ok` / `data` / `error` wrapper. Agents read the resource
shape directly. Successful runs always return exit 0; failures never
emit data JSON on stdout.

#### Field selection: `--json=field,field,…`

`--json` accepts a comma-separated field list (passed with `=`) to restrict
each top-level object (or each element of a top-level array) to the named
keys:

```bash
weknora kb list --json=id,name        # [{ "id": "kb_x", "name": "Eng" }, …]
weknora kb view kb_x --json=id        # { "id": "kb_x" }
```

Note the `=` form: pflag's optional-value parser treats space-separated
arguments after a bare `--json` as positionals, so `--json id,name` would
be interpreted as bare `--json` plus the positional `id,name`. Always use
`--json=field,...` for projection.

Unknown field names are silently dropped so you can pass an aspirational
field set across heterogenous outputs.

#### jq pipeline: `--jq <expr>`

`--jq` applies a jq expression to the JSON before printing. The
expression sees the same bare shape the command produces (no envelope
indirection). String results render without quotes for shell-friendly
substitution; non-string results render as JSON.

```bash
weknora kb list --jq '.[].id'                       # one id per line
weknora kb view kb_x --jq .name                     # bare name
weknora search chunks "x" --kb e --jq '.[].score'   # scores per line
```

`--jq` requires `--json`. Combining with `--json=id,name` is fine — the
filter runs after the field projection.

### Errors (stderr, exit code carries the class)

On failure, stdout is empty (or holds the partial-success output the
command already wrote before the failure). The error message goes to
stderr in this format:

```
<error.code>: <message>[: <wrapped cause>]
hint: <actionable next-step>
```

- `<error.code>` is a `namespace.snake_case` string from a closed
  registry in `cli/internal/cmdutil/errors.go` (`AllCodes()`).
  An acceptance test enforces that every code referenced in `cli/cmd/`
  is registered.
- `<message>` is the human-readable description.
- `<hint>` is the deterministic next-step hint (omitted when no hint
  applies).

Code namespaces: `auth.*` / `resource.*` / `input.*` / `server.*` /
`network.*` / `local.*` / `mcp.*`.

To pattern-match programmatically: split on the first `:` to extract
the code, or branch on the exit code (see below).

### Exit codes

| Code | Meaning | Agent action |
|---|---|---|
| `0` | Success | Continue |
| `1` | Typed `local.*` error or unclassified | Read stderr, decide retry/abort |
| `2` | Flag/argument validation error | Re-check `weknora <command> --help` |
| `3` | `auth.*` (token missing / expired / forbidden) | Re-auth then retry |
| `4` | `resource.not_found` | Verify the resource id |
| `5` | `input.*` (other than confirmation_required) | Adjust args and retry |
| `6` | `server.rate_limited` | Back off, then retry |
| `7` | `server.*` / `network.*` | Transient; retry with backoff |
| `10` | **`input.confirmation_required`** — high-risk write needs `-y` | Ask the human, retry with `-y` only after explicit approval |
| `130` | Cancelled (SIGINT / Ctrl-C) | Stop, do not retry |

Exit 10 is the wire-level signal for "high-risk write needs explicit
confirmation". **Never bypass exit 10 by auto-passing `-y` without
explicit user permission.**

---

## Command surface

Discover the command tree the same way human users do:

```bash
weknora --help                       # top-level
weknora kb --help                    # subtree
weknora kb delete --help             # single command flags
```

The command tree follows `<noun> <verb>`. Verbs are:

| Verb | Semantics | Example |
|---|---|---|
| `list` | Multi-resource read | `kb list` |
| `view` | Single-resource read | `kb view <id>` |
| `create` | Create resource | `kb create --name X` |
| `edit` | Partial update (only sent fields change) | `kb edit <id> --description X` |
| `delete` | Destructive remove (KB itself) | `kb delete <id> -y` |
| `empty` | Bulk-delete contents, preserve container | `kb empty <id> -y` |
| `upload` | Bulk write content | `doc upload <file>` |
| `download` | Stream resource to disk | `doc download <id> -O file` |
| `pin` / `unpin` | Toggle "pinned" state (idempotent) | `kb pin <id>` |
| `use` | Switch active selection | `context use <name>` |
| `add` / `remove` | Manage local config entries | `context add staging --host ...` |

`auth` subtree: `login` / `logout` / `list` / `status` / `refresh` /
`token`. Context-switching uses `context use <name>` (WeKnora contexts
bundle host + tenant + credentials, so they need a richer abstraction
than a single per-host token slot). `auth refresh` exchanges the stored
refresh token for a new access + refresh pair (OAuth refresh-token
grant); it errors with `input.invalid_argument` on API-key contexts
which have no refresh semantic. Transparent 401 → refresh → retry is
wired into the SDK transport (`cli/internal/cmdutil/authretry.go`)
with singleflight de-dup, so most callers never need to invoke `auth
refresh` explicitly.

`search` subtree: `search chunks "<q>" --kb X` for hybrid retrieval;
`search kb "<q>"` / `search docs "<q>" --kb X` / `search sessions "<q>"`
for client-side substring filtering on the listing endpoints.

`session` subtree: `list` / `view` / `delete` for chat session
management. Sessions are the durable wrapper around `chat` invocations.

Top-level RAG / connectivity verbs: `chat`, `search`, `api`, `link`,
`auth`, `context`, `session`, `doctor`, `version`.

`doctor` is a deliberate WeKnora addition: RAG deployments routinely
break on misconfigured embeddings, storage backends, and credentials,
and the structured `{summary: {all_passed, passed, warned, failed,
skipped}, checks: [...]}` JSON shape is the cleanest agent-readable
surface for that.

---

## Behavioral rules

Per-command guidance also appears in each command's `--help` output
(under "AI Agents:").

1. **Pass `-y/--yes`** on destructive writes (`kb delete` / `kb empty` /
   `doc delete` / `session delete` / `context remove` when targeting
   the current context) when running headless. Without it, you will
   get exit 10. **Never auto-add `-y`** without the user's explicit
   go-ahead — the exit-10 protocol is the one explicit guard against
   unintended writes.
2. **Prefer typed commands over `weknora api`** for known endpoints.
   Fallback to `weknora api` only when no typed command covers the
   call.
3. **For chat, prefer `--no-stream --json`** in agent contexts.
   Streaming tokens to stdout makes JSON parsing impossible.
4. **`link` writes to the user's working directory** — only run it
   when the user invoked it, not as a side effect of unrelated
   automation.

(Additional safety guidance — e.g. "do not switch context unless the
user asked" — is documented in the affected command's own `--help`.)

---

## Auto-detection of agent environments

`weknora` checks these environment variables (case-sensitive):

| Env var | Detected agent name |
|---|---|
| `CLAUDECODE` | `claude-code` |
| `CURSOR_AGENT` | `cursor` |

When any is set, `weknora --help` appends the command's `agent_help`
annotation. **No behavior change** — this is help-text rendering only.

To suppress detection (e.g. running `weknora` interactively from inside
Claude Code without the agent footer): `WEKNORA_NO_AGENT_AUTODETECT=1`.

Agent detection (`CLAUDECODE` / `CURSOR_AGENT` env) also tags the
User-Agent header for server-side telemetry — it never changes CLI
behavior.

---

## Architecture decisions

A handful of decisions are referenced inline in the source as `ADR-N`. They
live here, alongside the contract they shape.

**ADR-3 — bare-data JSON on stdout, errors on stderr.** Successful
commands emit the raw resource shape (`[]Item` for lists, `T` for views,
`{id, deleted: true}` for deletes) — no `ok` / `data` / `error`
wrapper. Errors are not data; they go to stderr in `code: message\nhint:
…` form, and the typed exit code carries the failure class for
programmatic branching. This separates "what the command produces" from
"how the run went", so `--json | jq` pipelines never have to filter
error shapes out of the success stream, and matches the contract of
gh / aws / stripe.

WeKnora-specific shape choices:

- `link` (project-binding) — `<cwd>/.weknora/project.yaml` walk-up
  matches how RAG users scope work to a specific knowledge base. There
  is no per-host config model competing with it; `context use` is the
  separate mechanism for switching the credential set.
- `chat` / `search` are domain-specific verbs (LLM streaming +
  retrieval) with no equivalent in pure-API CLIs.
- `context use` switches the active credential set; contexts bundle
  host + tenant + credential, so a richer abstraction than a single
  per-host token slot is required.
- `doctor` (4-status: ok / warn / fail / skip per check, plus a
  summary object) is the agent-readable surface for RAG-deployment
  misconfiguration (embeddings, storage, credentials) — failure modes
  that the underlying SDK can't classify on its own. Agents short-
  circuit on `summary.all_passed`; exit code is non-zero iff any
  check is `fail`.

Verb canon: `list / view / create / edit / delete / upload / download
/ pin / unpin / use`. WeKnora-specific verbs for resource semantics
the common set lacks: `empty` (bulk-delete contents preserving the
container), `refresh` (token), `add` / `remove` (context CRUD),
`link` / `unlink` (project bind / unbind), `invoke` (run a custom
agent), `serve` (long-lived MCP transport).

**ADR-4 — Factory closures + narrow Service interfaces.** `cmdutil.Factory`
exposes four lazy closures (Config / Client / Prompter / Secrets) that
commands may invoke, but each subcommand declares its own narrow `Service`
interface for the SDK calls it actually makes. The production `*sdk.Client`
satisfies these implicitly via duck typing; tests inject fakes. Splitting
the boundaries this way means a subcommand's test can stand up an
`httptest.Server` (or a hand-rolled struct) without standing up the full
SDK, and the dependency graph of any one command is visible in one file.

---

## Known limitations

The following classes of failure currently surface as `error.code =
"network.error"` with `context deadline exceeded` rather than a precise
typed code. A future release will introduce a `precondition.*`
namespace (server returns HTTP 412 with a typed remediation body before
opening the SSE / streaming response):

- `weknora chat` when no chat model is configured for the active tenant
- `weknora search chunks` when no retriever / vector store is configured
- `weknora doc upload` when no storage engine is selected for the KB

Workaround until then: if a chat / search / upload call times out
without producing a first-byte response, check the server's tenant
configuration (LLM / vector store / storage engine) before retrying. A
planned `weknora doctor --server-config` will probe these directly.

---

## Reporting issues

If the CLI's behavior contradicts this document, that is a bug. File at
https://github.com/Tencent/WeKnora/issues with:

- The exact command line
- `weknora --version` output
- The output (stdout + stderr) you got vs the output this document
  promises
