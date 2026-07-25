---
name: weknora-shared
description: Use when driving a WeKnora RAG server through the `weknora` CLI as an agent — authenticating, managing knowledge bases / documents / sessions / agents, running search or chat, or interpreting the CLI's JSON envelopes and exit codes. Read this before any other weknora-* skill.
metadata:
  tested_against: v0.10
---

# WeKnora CLI — shared base

`weknora` is the agent-first CLI for a WeKnora RAG server. Every command prints
a JSON envelope and uses a typed exit code, so you branch on machine-readable
output, not prose. Read this skill before any task-specific `weknora-*` skill.

## 1. Authenticate (do this first — order matters)

**Agents usually skip profiles entirely:** set `WEKNORA_API_KEY` (or
`WEKNORA_TOKEN`) + `WEKNORA_HOST` and every command authenticates statelessly and
zero-disk — no `profile add` / `auth login` needed. `auth token` echoes that env
credential; `auth status` / `doctor` confirm it. The steps below set up a
**persistent named profile** instead (interactive / multi-environment use).

Authentication is a **two-step sequence**. `weknora auth login` operates on the
*active profile*, so the profile must exist first:

```bash
# 1. register a connection target and make it active
weknora profile add prod --host https://kb.example.com --use
# 2a. API key (agent default — pipe the key on stdin, non-interactive):
echo "$WEKNORA_API_KEY" | weknora auth login --with-token
# 2b. OR email+password (interactive prompt only — no flags; not for agents)
weknora auth login

weknora auth status          # verify: who am I, which tenant
```

- Target a *non-active* profile for one command with the global `--profile NAME`
  (e.g. `weknora --profile staging auth refresh`). There is no per-command
  `--name`/`--host` on auth commands.
- Get the raw token for scripting with `WEKNORA_TOKEN=$(weknora auth token)`
  (raw token by default; works with an env credential too; `--format json` gives
  the `{token, mode, profile}` envelope).
- `weknora auth logout` clears a profile's stored credentials but **keeps the
  profile registered** (re-auth later with `auth login`); use `profile remove`
  to delete the profile entirely.
- `weknora doctor` runs 4 health checks (reachability, credential, version, storage).

## 2. Selecting a knowledge base (`--kb`)

`--kb` accepts a **name or a UUID** (resolved server-side). Resolution order:
`--kb` flag → `WEKNORA_KB_ID` env → directory link (`weknora link --kb X` binds
the cwd) → error. Read/create commands that operate "inside a project" inherit
the link; **`search *` and destructive `--all` operations always require an
explicit `--kb`** (so an agent never silently hits the wrong corpus).

**A KB must have an embedding model bound to be searchable.** A freshly created
KB is `retrieval_ready:false` — uploaded docs stay unindexed and `search`/`chat`
return nothing until you bind models. Create it ready in one step
(`kb create --embedding-model <m> --chat-model <m>`, discover ids with
`weknora model list`) or bind after the fact
(`kb config set <kb> --embedding-model <m> --chat-model <m>`). `kb status` /
`kb check` report `retrieval_ready`, and `kb create` hints the fix when it is
false — so an unconfigured KB is never silently "healthy".

## 3. Output contract — every command

Default output is `--format json`: a single envelope.

| Field | Meaning |
|---|---|
| `ok` | `true`/`false` — branch on this first (see batch/wait caveat below) |
| `data` | success payload (object or array; absent on mutation-only success) |
| `meta` | `count` / `has_more` / `dry_run` / `plan` … |
| `error` | on failure: `{type, message, hint?, retry_argv?, retryable?, risk?}` |
| `profile` | active profile name |

- `error.type` is a **stable typed code** (e.g. `local.kb_not_found`,
  `input.invalid_argument`, `input.confirmation_required`, `server.error`).
  Branch on it; `error.hint` usually tells you the next action.
- `--format text` = a live human-readable projection. `chat` and `session ask`
  buffer a bounded answer-event projection into one JSON envelope by default;
  pass `--reference` for indexed citations, `--verbose` for execution detail,
  or `--format ndjson` for raw event lines. `session resume` remains
  an NDJSON streaming command.
- `--jq '<expr>'` filters the envelope (e.g. `weknora kb list --jq '.data[].id'`).
- Exception: `weknora auth token` emits the **raw token** by default (it's a
  scripting helper); pass `--format json` for the `{token, mode, profile}` envelope.
- **Batch / wait caveat:** multi-item commands come in two shapes, but for
  **both you branch on the exit code, not `ok`**:
  - `doc/chunk/session delete` with several ids → a **batch** envelope:
    `status` is `success`/`partial`/`error`, `ok` is `true` *only* when every
    item succeeded (**`ok:false` on any failure**), `data` is a per-item array
    `[{id, ok, result|error}]`, and `meta.successes`/`failures` count the split.
    Exit 1 if any item failed.
  - `doc wait` → a normal `ok:true` envelope whose `data` partitions the ids
    into `{completed, failed, timeout}`; `ok` stays `true` even with failures
    (to avoid a contradictory envelope). Exit 1 if any doc failed, 124 on timeout.
  Either way, read the **exit code** first, then `data` for which items failed.

## 4. Exit codes (branch on these)

| Code | Meaning (typed code class) | Agent action |
|---|---|---|
| 0 | success (incl. `--dry-run`) | proceed |
| 1 | `local.*` / unclassified (incl. `local.kb_not_found`) | read `error`, decide retry/abort |
| 2 | flag / argument validation (bad/unknown/missing-required flag) | re-check `weknora <cmd> --help` |
| 3 | `auth.*` (missing / expired / forbidden) | re-auth, then retry |
| 4 | `resource.not_found` (a server resource id) | verify the id |
| 5 | `input.*` (other than confirmation_required) | adjust args, retry |
| 6 | `server.rate_limited` | back off, retry |
| 7 | `server.*` / `network.*` | transient — retry with backoff |
| **10** | **`input.confirmation_required` (destructive)** | **see §5 — never auto-bypass** |
| 124 | `operation.timeout` | raise `--timeout` or check the job |
| 130 | `operation.cancelled` (SIGINT/SIGTERM) | stop, don't retry |

Note: a server resource id that doesn't exist → exit 4 (`resource.not_found`);
a `--kb` *name* that doesn't resolve client-side → exit 1 (`local.kb_not_found`).
`input.invalid_argument` spans two exit codes — a malformed *invocation*
(unknown/missing flag, wrong arg count) exits **2** (fix the command, re-check
`--help`), while a value rejected *after* parsing (e.g. a bad `--jq` expression)
exits **5** (adjust the value, retry). Branch on the exit code to tell them apart.

## 5. Destructive writes (exit 10) — hard rule

Destructive commands (`kb/doc/chunk/session/agent delete`, `kb/agent update`,
`auth logout`, `doc delete --all`, …) without `-y` exit **10** with
`error.type = input.confirmation_required` and `error.risk = {level, action}`:

```jsonc
{"ok":false,"error":{"type":"input.confirmation_required",
  "message":"delete knowledge base X requires explicit confirmation: re-run with -y",
  "retry_argv":["weknora","kb","delete","X","-y"],"risk":{"level":"destructive","action":"kb.delete"}}}
```

**Surface this to the user and get explicit approval. Re-run with `-y` ONLY
after they approve. Never add `-y` on your own initiative.**

## 6. Preview before acting — `--dry-run`

Any mutation accepts `--dry-run`: it resolves the request, prints
`meta.plan = {action, args}`, makes **zero** server/side-effect changes, exits 0.
Use it to confirm a command is well-formed (and what it would do) before running
it for real — especially before destructive or bulk operations.

## 7. Chaining (mutation → id)

Mutations return the new resource id in `data.id`. Chain with it:

```bash
KB=$(weknora kb create "Docs" --jq '.data.id' --format json | tr -d '"')
weknora doc upload ./manual.pdf --kb "$KB"
```

## 7a. Reliability patterns for long-running agent runs

### Inspecting prior messages
Use `weknora message list --session <sess-id>` to review the message history of a session (e.g., after a stream drops) before deciding whether to re-ask or continue. Use `weknora message search "<query>"` to locate a prior Q&A exchange across all sessions — prefer this over re-running an expensive query when the answer may already exist.

### Tool-approval unlock
An agent run pauses mid-stream on a tool-approval event when the server requires human sign-off before executing a tool call. The pattern:

1. The stream emits a tool-approval event; capture the `pending_id`.
2. **Surface the pending tool call to the user** (show tool name + proposed args). Do not auto-approve.
3. After explicit user go-ahead: `weknora session tool-approval resolve <pending-id> -y` to approve, or add `--reject --reason "..."` to reject.
4. Resume the answer: `weknora session resume <sess-id> --message <msg-id>`.

`--modified-args '{"key":"val"}'` replaces the tool arguments on approve (non-empty JSON object required). This is an exit-10 interaction — see §5.

## 8. Resource model & command map

```
kb        knowledge bases   list/view/create/update/delete/pin/unpin/status/check
doc       documents in a KB list/view/create/upload/fetch/download/reparse/update/delete/wait
chunk     retrieval units   list/view/delete   (RAG debug; not search)
session   conversations     list/view/delete/ask/stop/resume/tool-approval resolve
message   session messages  list/search/delete
agent     custom agents     list/view/create/update/delete/status/check
model     configured models list/view/create/update/delete   (update rotates key / base-url in place, id preserved)
search    retrieval         chunks / docs / kb / sessions
chat      one-shot KB RAG Q&A (streaming)
api       raw HTTP passthrough to any server endpoint (escape hatch)
link/unlink  bind cwd to a KB        mcp serve  expose weknora as MCP tools
```

**chat vs session ask vs search** — the most common confusion: see the
`weknora-rag-search` skill for the decision table. Briefly: `chat` = one-shot
KB Q&A with an LLM; `session ask --agent <id>` = invoke a *custom agent*;
`search chunks` = raw hybrid retrieval (no LLM).

## 9. CLI vs MCP

For your own scripted control, use the CLI (richer: dry-run, exit-10, all verbs).
For an IDE/host agent that speaks MCP, `weknora mcp serve` exposes a curated
read+chat tool set: `kb_list`, `kb_view`, `doc_list`, `doc_view`, `doc_download`,
`search_chunks`, `chunk_list`, `agent_list`, `chat`, `session_ask`. MCP tools
take raw ids (no name resolution); resolve names via `kb_list` first.

## 10. Agent self-help

Run `weknora <command> --help`. With `WEKNORA_AGENT_HELP=1` set, `--help` emits a
JSON blob (`used_for` / `required_flags` / `examples`) instead of prose — parse
that to learn a command without scraping the human help table.

## Common mistakes

| Mistake | Fix |
|---|---|
| `weknora auth login` before any profile exists | `profile add <n> --host <url> --use` first (§1) |
| Passing `--host` or `--name` to `auth login` | `auth login` takes neither — host comes from the profile; use `profile add <n> --host <url> --use`, then `auth login` |
| Auto-adding `-y` to clear an exit-10 | Never; get user approval first (§5) |
| Need the raw `chat` event stream | pass `--format ndjson`; use `--format text` for a live projected transcript |
| `search chunks "q"` → exit 1 `local.kb_id_required` | Pass `--kb <name-or-id>`, set `WEKNORA_KB_ID`, or `weknora link` the dir |
