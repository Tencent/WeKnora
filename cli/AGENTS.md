# Agent Integration Guide for `weknora` CLI

> **Scope.** This file is an **operational reference** for LLM agents
> (Claude Code, Cursor, Codex, Aider, Gemini Coder, etc.) that **invoke
> `weknora` on a user's behalf**. It documents the wire shape, exit code,
> and behavioral conventions an agent integration relies on.
>
> This is **not** a contributor guide. If you are an AI coding agent
> editing weknora's source, see the repo root `README.md` (and, if added
> later, a separate contributor `AGENTS.md` at the repo root).

`weknora` is designed to be agent-friendly: error messages, output format,
and flag design follow conventions agents can rely on. Wire-contract
breaking changes are flagged in their PR description and the corresponding
`weknora --version` bump — agents should pin a known-good version and
re-validate against `--help` output on upgrade.

The model: **gh CLI** as the human-side north star, **lark-cli (larksuite)**
as the agent-affordance reference. The "Output contract" and "Behavioral
rules" sections below are the self-contained specification of that
decision; everything an integrator needs is in this document.

---

## Output contract

### Streams

- **stdout** is the data channel: JSON envelope (with `--json`) or
  human-formatted output.
- **stderr** is logs / progress / warnings / agent guidance footnotes.
  Never parse stderr for data.

A non-empty stderr does **not** mean failure — read the exit code instead.

### JSON envelope

When `--json` is set, stdout contains exactly one envelope:

```jsonc
{
  "ok": true,                 // false on failure; check this first
  "data": { /* command-specific payload */ },
  "error": { "code": "...", "message": "...", "hint": "..." },  // iff ok=false
  "_meta": { "request_id": "...", "kb_id": "..." },             // optional
  "risk": { "level": "high-risk-write", "action": "..." },      // write commands
  "dry_run": false                                              // true on --dry-run
}
```

This snippet is illustrative. Fields are added (never renamed or repurposed)
within a minor version, and agents must not error on unknown keys. The
authoritative envelope shape lives in `cli/internal/format/envelope.go`.

### Error codes (closed registry)

`error.code` is a `namespace.snake_case` string from a closed registry in
`cli/internal/cmdutil/errors.go` `AllCodes()`. An acceptance test enforces
that every code referenced in `cli/cmd/` is registered.

Categories: `auth.*` / `resource.*` / `input.*` / `server.*` / `network.*` /
`local.*` / `mcp.*`.

`error.hint` provides a deterministic next-step hint agents can follow
without natural-language parsing.

### Exit codes

| Code | Meaning | Agent action |
|---|---|---|
| `0` | Success | Continue |
| `1` | Typed error (see envelope.error.code) | Read code, decide retry/abort |
| `2` | Flag/argument validation error | Re-check `weknora <command> --help` |
| `10` | **Confirmation required** for high-risk write | Ask the human, retry with `-y` only after explicit approval |
| `130` | Cancelled (SIGINT / Ctrl-C) | Stop, do not retry |

The exit-10 protocol mirrors `lark-cli`'s
([source](https://github.com/larksuite/cli/blob/main/skills/lark-shared/SKILL.md))
"high-risk write requires confirmation" model. **Never bypass exit 10 by
auto-passing `-y` without explicit user permission.**

---

## Command surface

Discover the command tree the same way human users do:

```bash
weknora --help                       # top-level
weknora kb --help                    # subtree
weknora kb delete --help             # single command flags
```

The command tree follows `<noun> <verb>` (gh style). Verbs are:

| Verb | Semantics | Example |
|---|---|---|
| `list` | Multi-resource read | `kb list` |
| `view` | Single-resource read | `kb view <id>` |
| `create` | Create resource | `kb create --name X` |
| `delete` | Destructive remove | `kb delete <id> -y` |
| `upload` | Bulk write content | `doc upload <file>` |
| `use` | Switch active selection | `context use <name>` |

`auth` subtree: `login` / `logout` / `list` / `status`. Mirrors gh's
`auth login / logout / status / switch / list`-style surface; weknora uses
`context use` instead of `auth switch` because contexts carry host + tenant
on top of credentials. Token refresh is deferred to v0.3 (will land as
transparent 401 retry in the SDK plus an explicit `auth refresh` command).

Top-level RAG / connectivity verbs: `chat`, `search`, `api`, `link`,
`auth`, `doctor`, `version`.

`doctor` is a deliberate divergence from gh / lark (neither ships a
health-check command); the precedent is `flutter doctor` / `brew doctor`.
Kept because RAG deployments routinely break on misconfigured embeddings,
storage backends, and credentials, and a structured 4-status envelope
(ok/warn/fail/skip) is the cleanest agent-readable surface for that.

---

## Behavioral rules

These mirror lark-cli's per-command `Tips`. Per-command guidance also
appears in each command's `--help` output (under "AI agents:").

1. **Pass `-y/--yes`** on `kb delete` / `doc delete` / `auth logout` when
   running headless. Without it, you will get exit 10. **Never auto-add
   `-y`** without the user's explicit go-ahead — the exit-10 protocol is
   the one explicit guard against unintended writes.
2. **Prefer typed commands over `weknora api`** for known endpoints.
   Fallback to `weknora api` only when no typed command covers the call.
3. **For chat, prefer `--no-stream --json`** in agent contexts. Streaming
   tokens to stdout makes JSON envelope parsing impossible.
4. **Honor `--dry-run`** — when the user passes it, don't follow up with
   the real command unless explicitly asked. The dry-run envelope is the
   answer.
5. **`link` writes to the user's working directory** — only run it when
   the user invoked it, not as a side effect of unrelated automation.

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

The omnibus `--agent` mode-switch flag that briefly existed in early v0.2
was removed: gh / kubectl / aws / docker / flyctl all decline this kind
of flag, since per-command `--json` + TTY auto-detect cover the same
ground without an extra global switch. Stripe's `DetectAIAgent` (the
inspiration) only tags User-Agent for telemetry, never flips behavior;
`weknora` now follows that narrower scope.

---

## Architecture decisions

A handful of decisions are referenced inline in the source as `ADR-N`. They
live here, alongside the contract they shape.

**ADR-3 — `gh` CLI as the primary mainstream north star.** When weknora's
v0.0/v0.1 surface was audited against gh / kubectl / cargo / npm / git /
docker / flyctl / vercel / supabase / brew, gh emerged as the closest fit
for an opinionated noun-verb tool with a stable JSON envelope and an
agent-aware error model. Documented deviations:

- `link` (project-binding) borrows from `vercel link` / `netlify link`
  rather than gh's per-host config model — a `<cwd>/.weknora/project.yaml`
  walk-up matches how RAG users scope work to a specific knowledge base.
- `chat` / `search` are domain-specific verbs gh has no analog for.
- `context use` (kubectl idiom) instead of gh's `auth switch` — weknora's
  context bundles host + tenant + credential, which is more than gh's
  per-host account model.
- `doctor` (flutter / brew idiom) instead of gh's `status` (which is an
  activity feed, different concept) — RAG deployments routinely break on
  misconfigured embeddings / storage / credentials, so a structured
  4-status diagnostic is the agent-readable surface for that.

Verb canon: `list / view / create / delete / upload / use` (all gh-canonical).

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

The following classes of failure currently surface as `error.code = "network.error"`
with `context deadline exceeded` rather than a precise typed code. A future
release will introduce a `precondition.*` namespace (server returns HTTP 412
with a typed remediation body before opening the SSE / streaming response):

- `weknora chat` when no chat model is configured for the active tenant
- `weknora search` when no retriever / vector store is configured
- `weknora doc upload` when no storage engine is selected for the KB

Workaround until then: if a chat / search / upload call times out without
producing a first-byte response, check the server's tenant configuration
(LLM / vector store / storage engine) before retrying. A planned
`weknora doctor --server-config` will probe these directly.

---

## Reporting issues

If the CLI's behavior contradicts this document, that is a bug. File at
https://github.com/Tencent/WeKnora/issues with:

- The exact command line
- `weknora --version` output
- The envelope you got vs the envelope this document promises
