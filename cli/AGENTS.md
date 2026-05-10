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
| `view` | Single-resource read (alias `get` for v0.0/v0.1 callers) | `kb view <id>` |
| `create` | Create resource | `kb create --name X` |
| `delete` | Destructive remove | `kb delete <id> -y` |
| `upload` | Bulk write content | `doc upload <file>` |
| `use` | Switch active selection | `context use <name>` |

Top-level RAG / connectivity verbs: `chat`, `search`, `api`, `init`, `link`,
`auth`, `whoami`, `doctor`, `version`.

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
5. **`init` writes to the user's working directory** — only run it when
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

## Reporting issues

If the CLI's behavior contradicts this document, that is a bug. File at
https://github.com/Tencent/WeKnora/issues with:

- The exact command line
- `weknora --version` output
- The envelope you got vs the envelope this document promises
