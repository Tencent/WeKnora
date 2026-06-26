# weknora — WeKnora CLI

A command-line interface for the WeKnora RAG knowledge-base server. Lets you
authenticate, manage knowledge bases and documents, run hybrid search, and
ask streaming RAG questions from your terminal or from an AI agent.

```bash
$ weknora --help
Command-line client for the WeKnora RAG server. Manage knowledge bases
and documents, run hybrid search, chat with grounded answers, or expose
a curated read-only MCP tool surface for AI agents.

Available Commands:
  agent       Manage custom agents (CRUD + status/check)
  api         Make a raw API request to the WeKnora server
  auth        Manage authentication credentials and profiles
  chat        Ask a streaming RAG question against a knowledge base
  chunk       Manage document chunks (RAG retrieval debug)
  completion  Generate the autocompletion script for the specified shell
  profile     Manage CLI profiles (named connection targets)
  doc         Manage documents in a knowledge base
  doctor      Run 4 self-checks: base URL, auth, server version, credential storage
  help        Help about any command
  kb          Manage knowledge bases
  link        Bind the current directory to a knowledge base
  mcp         Run weknora as a Model Context Protocol server
  message     Inspect and manage messages inside chat sessions
  search      Search across chunks, knowledge bases, documents, or sessions
  session     Manage chat sessions (incl. tool-approval resolve)
  unlink      Remove the directory's knowledge-base binding
  version     Show CLI build metadata
```

The wire contract for AI agents is documented [below](#wire-contract).
For contributing to the CLI source, see [AGENTS.md](AGENTS.md).

---

## Install

### From source

Requires Go 1.26+.

```bash
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora/cli
go build -o weknora .
sudo mv weknora /usr/local/bin/   # or anywhere on $PATH
```

### Pre-built binaries

Building from source is the supported install method today. Pre-built binaries,
`go install`, and a Homebrew formula are planned to accompany a tagged release;
until then, use the from-source build above.

---

## 5-minute quickstart

```bash
# 1. Register your WeKnora server as a profile and make it active
weknora profile add prod --host https://kb.example.com --use

# 2. Authenticate the active profile (interactive password prompt)
weknora auth login

# 2b. Or pipe an API key from stdin (for CI / AI agents)
echo "sk-..." | weknora auth login --with-token

# 3. List knowledge bases
weknora kb list

# 4. Bind this directory to a knowledge base — subsequent commands auto-resolve --kb
weknora link --kb my-knowledge-base

# 5. Upload a document, then block until parsing finishes
weknora doc upload notes.md
weknora doc wait doc_abc                          # exit 0 completed, 1 failed, 124 --timeout, 130 ^C
weknora doc reparse doc_abc                       # re-trigger parsing if it failed, then wait again

# 6. Search
weknora search chunks "what is reciprocal rank fusion?"

# 7. Ask the LLM (streams to terminal)
weknora chat "summarise the design doc"

# 8. Manage custom agents and run them (see `weknora agent --help` / `weknora session --help`)
weknora model list                               # discover a model id for --model
weknora agent list
weknora session ask --agent ag_abc "what's our q4 retention plan?"

# 9. Inspect a document's chunks for RAG retrieval debug
weknora chunk list --doc doc_xyz

# 10. Inspect messages in a session / search across sessions
weknora message list --session sess_abc
weknora message search "retry policy"                      # cross-session Q&A retrieval

# 11. Resolve a pending tool approval (agent run blocked on approval event)
weknora session tool-approval resolve pend_xxx -y          # approve (after user go-ahead)
weknora session continue-stream sess_abc --message msg_xyz # resume the blocked stream

# 12. Health & verification verbs
weknora kb status kb_abc       # fast snapshot: reachable / counts / processing flag (1 HTTP)
weknora kb check kb_abc        # deep verify: also aggregates failed_count via doc list (1+N HTTP)
weknora agent status ag_abc    # fast: reachable / model_id
weknora agent check ag_abc     # deep: probes every KB in the agent's scope
```

---

### Agent quick start

For AI agents (any MCP-capable host) integrating WeKnora:

1. Install: build from source (see [Install](#install))
2. Authenticate. In a sandbox / CI, the **stateless** path needs no `auth login`
   and writes nothing to disk — just set two env vars:
   ```bash
   export WEKNORA_API_KEY="sk-…"   # or WEKNORA_TOKEN for a bearer JWT
   export WEKNORA_HOST="https://kb.example.com"
   weknora kb list                 # already authenticated
   ```
   Or, for a persisted local profile:
   ```bash
   weknora profile add prod --host <server-url> --use
   weknora auth login
   ```
3. Register MCP in the host's MCP config:
   ```json
   {"mcpServers": {"weknora": {"command": "weknora", "args": ["mcp", "serve"]}}}
   ```
4. Read the [wire contract](AGENTS.md#wire-contract-for-ai-agents) before
   parsing `--format json` output.
5. Read the [exit-10 anti-patterns](AGENTS.md#exit-10-anti-patterns) before
   any destructive call.

**Bundled Agent Skills.** This CLI ships [Agent Skills](https://agentskills.io/specification)
under [`skills/`](skills/) that teach an agent to drive WeKnora without trial and error:

- [`weknora-shared`](skills/weknora-shared/SKILL.md) — **read first**: auth/profile
  sequence, `--kb` resolution, the JSON-envelope + exit-code contract, the exit-10
  protocol, `--dry-run`, and CLI-vs-MCP selection.
- [`weknora-rag-search`](skills/weknora-rag-search/SKILL.md) — when to use `chat`
  vs `session ask` vs `search chunks`, plus retrieval gotchas.

Install them with the CLI (the skills are embedded in the binary, no checkout
needed):

```bash
weknora skills install                      # writes to ~/.claude/skills
weknora skills install --dir <agent-skills-dir> --force   # other agents / overwrite
weknora skills list --format json           # what would be installed
```

Existing files are left untouched without `--force`; `--dry-run` previews the
file list. Each skill's frontmatter records the CLI version it was
`tested_against`; a CI parity test (`internal/skillparity`) fails if a skill
ever references a command, flag, or MCP tool the CLI no longer has.

---

## Multi-profile

`profile.*` manages profile *records* (positional `<name>`); `auth.*` operates
on the *active* profile (override per-invocation with the global `--profile`
flag). Create a profile first, then authenticate it:

```bash
weknora profile add prod    --host https://prod.example.com --use     # add + switch
weknora auth login                                                    # authenticate active (prod)

weknora profile add staging --host https://staging.example.com        # add (stays inactive)
echo "sk-..." | weknora --profile staging auth login --with-token     # authenticate staging

weknora auth list
weknora profile use prod                                              # switch back
```

Credentials are persisted to your OS keyring (Keychain on macOS, libsecret on
Linux, Wincred on Windows) when available, otherwise to a 0600-mode file
under `$XDG_CONFIG_HOME/weknora/secrets/`. The active profile lives in
`~/.config/weknora/config.yaml`.

To remove a profile's stored credentials:

```bash
weknora auth logout                       # active profile
weknora --profile staging auth logout     # specific profile
weknora auth logout --all
```

---

## Wire contract

Designed to be AI-agent-first. Stable across minor releases; breaking
changes announced in the changelog and the corresponding
`weknora --version` bump.

### Streams

- **stdout** is the data channel: bare JSON with `--format json`, or
  human-formatted output. Never carries error text.
- **stderr** is logs, progress, warnings, and errors. A non-empty
  stderr does **not** mean failure — read the exit code.

### JSON output

Every command supports `--format json`, wrapping the resource in the
symmetric envelope `{ok, data, meta?}` — `data` is an array for `list` /
`search`, a single object for `view` and write outcomes. `--jq` runs
against the whole envelope, so reach into list items with `.data[]`:

```bash
weknora kb list --format json                              # {"ok":true,"data":[{"id":"kb_x",…}],"meta":{"count":1,…}}
weknora kb view kb_x --format json                         # {"ok":true,"data":{"id":"kb_x","name":"Eng",…}}
weknora kb list --format json --jq '.data[] | {id, name}'  # project listed fields out of each item
weknora kb list --format json --jq '.data[].id'            # ids only
weknora kb list --format json --jq '.meta.count'           # number returned
```

`--format ndjson` is also accepted for streaming list commands; each
element is emitted as its own JSON line. `--format json` is the default
regardless of TTY — running `weknora kb list | jq` works without an
explicit flag. Use `--format text` for human-readable output.

### Errors

On failure, stdout stays empty and the typed error goes to stderr in
this format:

```
<code.namespace>: <message>[: <wrapped cause>]
hint: <actionable next-step>
```

Example:

```
auth.unauthenticated: fetch current user: HTTP error 401: ...
hint: run `weknora auth login`
```

The full code registry is in `cli/internal/cmdutil/errors.go`
(`AllCodes()`). Code namespaces: `auth.*` / `resource.*` / `input.*` /
`server.*` / `network.*` / `local.*` / `mcp.*` / `operation.*` (CLI-level
wait/poll outcomes: `operation.timeout`, `operation.failed`, `operation.cancelled`).

### Exit codes

| Code | Meaning | Agent action |
|---|---|---|
| `0`   | success                                                | continue |
| `1`   | typed `local.*` / `operation.failed` / unclassified    | read stderr, decide retry/abort |
| `2`   | flag / argument validation error                       | re-check `weknora <cmd> --help` |
| `3`   | `auth.*` (token missing / expired / forbidden)         | re-auth, then retry |
| `4`   | `resource.not_found`                                   | verify the resource id |
| `5`   | `input.*` (other than `confirmation_required`)         | adjust args, retry |
| `6`   | `server.rate_limited`                                  | back off, retry |
| `7`   | `server.*` / `network.*`                               | transient — retry with backoff |
| `10`  | **`input.confirmation_required`** (high-risk write)    | ask the human, retry with `-y` only after explicit approval |
| `124` | `operation.timeout` (e.g. `doc wait --timeout` reached) | raise `--timeout` or check the underlying job |
| `130` | cancelled by signal — typed `operation.cancelled` errors exit 1; `main.go` promotes the process exit to 130 when the root context was signal-cancelled (SIGINT / SIGTERM) | stop, do not retry |

Run `weknora exit-codes` for the machine-readable matrix (JSON); `weknora help exit-codes` for the human-readable table.

**Exit 10** is the wire-level signal for "destructive write needs
explicit confirmation". Pass `-y/--yes` on `kb delete` /
`doc delete` (including `--all --kb=<id>`) / `session delete` /
`profile remove` (on the current profile) / `agent delete` /
`chunk delete` when running headless.
**Never auto-add `-y` without the user's explicit go-ahead** — exit 10
is the guard against unintended writes.

### Other AI-agent ergonomics

- For chat / session ask in AI-agent contexts, pass `--format json` for a
  bounded answer-event envelope. Add `--reference` for indexed citations,
  `--verbose` for reasoning/tools/lifecycle events, or `--format ndjson` for
  the unmodified raw stream.
- `--format json` composes with the global `--profile <name>` for
  single-shot profile overrides without disk writes.
- `weknora mcp serve` exposes a curated read-only tool surface over
  stdio MCP for any MCP-compatible client.
- `weknora schema` enumerates every command with its `used_for`, and
  `weknora schema <cmd path>` (e.g. `weknora schema doc update`) prints that
  command's full contract — `used_for`, `required_flags`, `examples`,
  `output`, `warnings`, `risk`, and local `flags` — as the standard envelope,
  so an agent can discover the surface without scraping `--help` prose.

---

## Advanced operations not exposed as flags

WeKnora CLI exposes top use cases as polished commands; deep
configuration goes through the raw HTTP passthrough. CLI flag coverage
targets common workflows, not 1:1 API parity. Examples of deep
operations that intentionally go through `weknora api`:

- **Tuning a KB's nested config** — chunking strategy, summary model,
  multimodal extraction defaults, FAQ thresholds, VLM model. Use
  `weknora api PUT /api/v1/knowledge-bases/<id> --input -` with a JSON
  body matching the server's `UpdateKnowledgeBaseRequest`. (Note: the
  storage provider is set once at create time via
  `kb create --storage-provider <name>` and is not updatable.)
- **Per-request `chat` parameters** — multi-KB scope, summary model
  override, image attachments, web search toggle. Use `weknora api POST
  /api/v1/knowledge-chat/<session-id> --input -`.
- **Per-request `session ask --agent` overrides** — same shape via
  `weknora api POST /api/v1/agent-chat/<session-id> --input -`.
- **Operations without a CLI verb** — register / change-password /
  OIDC flows, organization / sharing endpoints, tenant management.

`weknora api --help` documents the raw passthrough. Run
`weknora doctor` first to verify auth and base URL.

---

## Dry-run preview

Add `--dry-run` to any mutation command to preview the would-be action without executing it. Useful for verifying flag/arg parsing before committing to a destructive operation, or for agent-side action planning.

```bash
# Preview a kb create without actually creating
weknora kb create --name "test-kb" --description "for review" --dry-run

# Output (single line; pretty-printed here for readability):
# {
#   "ok": true,
#   "meta": {
#     "dry_run": true,
#     "plan": {
#       "action": "kb.create",
#       "args": {"name": "test-kb", "description": "for review"}
#     }
#   }
# }
# Exit code: 0
```

dry-run is **offline**: no network calls, no file IO, no credential touches. Works without an active profile.

For destructive commands, dry-run does NOT trigger the exit-10 confirmation flow:

```bash
weknora kb delete kb_xxxx --dry-run   # exit 0, no prompt
weknora kb delete kb_xxxx             # exit 10, prompts for -y
```

For the `api` command, dry-run requires explicit write method (POST/PUT/PATCH/DELETE); GET returns FlagError:

```bash
echo '{"name":"foo"}' | weknora api -X POST /api/v1/knowledge-bases --input - --dry-run   # OK
weknora api /api/v1/knowledge-bases --dry-run                                              # exit 2: requires explicit -X
```

---

## Resuming streams

The `weknora session continue-stream` command resumes an SSE event stream for an existing assistant message. Useful for network-blip recovery or polling long-running agent invocations:

```bash
# Original streaming call captures session_id + message_id from init event:
weknora session ask "..." --agent ag_xxxx --format ndjson | tee /tmp/stream.ndjson
# {"type":"init","session_id":"sess_abc","message_id":"msg_xyz"}
# ... events flow ...
# [network blip]

# Resume the same stream:
weknora session continue-stream sess_abc --message msg_xyz
# Server REPLAYS all stored events from the start, then tails new ones.
# Agent must dedupe (by message_id or event hash) to avoid double-processing.
```

### Tool-approval unlock chain

An agent run may pause the stream on a tool-approval event until a human approves or rejects the pending tool call. The unlock sequence:

```bash
# 1. Stream pauses with a tool-approval event carrying a pending_id.
# 2. Surface the pending tool call to the user; get explicit go-ahead.
weknora session tool-approval resolve pend_xxx -y                      # approve
# weknora session tool-approval resolve pend_xxx --reject --reason "..." -y  # reject
# 3. Resume the stream — server replays + tails from where the run was blocked.
weknora session continue-stream sess_abc --message msg_xyz
```

Pass `--modified-args '{"key":"value"}'` to replace tool arguments on approve (must be a non-empty JSON object). Never auto-pass `-y` — the approval is the exit-10 human-in-the-loop gate.

Server-side buffer TTL: 1 hour for redis mode; process lifetime for memory mode (default). After TTL, expect `local.sse_stream_aborted` typed error.

See `cli/AGENTS.md` "Stream recovery" section for the full agent contract.

---

## Health check

Run `weknora doctor` for a 4-status diagnostic (OK / warn / fail /
skip) covering base URL reachability, authentication, server-CLI
version skew, and credential storage backend. Add `--format json` for
machine-readable output, `--offline` to skip network checks.

For per-resource verification, the `status` / `check` verb pair gives
a fast vs deep choice:

| Verb | Cost | Use |
|---|---|---|
| `weknora kb status <kb-id>`     | 1 HTTP    | live counts / processing flag |
| `weknora kb check <kb-id>`      | 1+N HTTP  | adds `failed_count` via doc-list page-walk |
| `weknora agent status <agent-id>` | 1 HTTP  | reachable / model_id |
| `weknora agent check <agent-id>`  | 1+N HTTP | also probes every KB in the agent's scope |

`weknora doc wait <doc-id> [<doc-id>...]` blocks until each document
reaches a terminal `parse_status` (completed or failed). Exit codes:
0 (all completed), 1 (any failed), 124 (`--timeout` reached), 130
(Ctrl-C / SIGTERM). Multi-target is polled concurrently (max 5 in
flight; pipe through `xargs -P` for more).

---

## Development

```bash
# Run unit + contract tests
go test ./...

# Run the real-server e2e suite (requires WEKNORA_E2E_HOST + token env vars)
go test -tags acceptance_e2e ./acceptance/e2e/...

# Static analysis
go vet ./...
```

CI (`.github/workflows/cli.yml`) runs build + unit + contract tests on Linux /
macOS / Windows × Go 1.26, path-filtered to changes under `cli/`.

---

## Contributing / Reporting issues

- **Bugs and feature requests**: file an issue at
  [github.com/Tencent/WeKnora/issues](https://github.com/Tencent/WeKnora/issues).
- **Security disclosures**: see the repository-level
  [SECURITY.md](../SECURITY.md). Do not file public issues for
  security findings.
- **Pull requests**: the developer guide for editing the CLI lives in
  [AGENTS.md](AGENTS.md) (build / test / command-surface design SOP /
  CRUD flag conventions). Run `go test ./... -race -count=1` and `go vet ./...`
  before submitting.

---

## License

MIT — see the repository [LICENSE](../LICENSE).
