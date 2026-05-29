# Changelog — `weknora` CLI

All notable changes to the `weknora` CLI (the binary under `cli/` in this
repository) will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)
and the CLI follows [Semantic Versioning](https://semver.org/) independently
of the WeKnora server / frontend release cadence.

CLI history before v0.3 is recorded in the project root
[CHANGELOG.md](../CHANGELOG.md) under the release that introduced the CLI.

## [Unreleased]

### v0.8 — Agent safety nets + MCP annotations

#### Added
- `--dry-run` flag on every mutation cobra command (`kb create/edit/delete`, `agent create/edit/delete`, `doc create/upload/fetch/delete`, `chunk delete`, `session delete`, `auth refresh/logout`, `link/unlink`, `profile add/remove`) and on `weknora api` (POST/PUT/PATCH/DELETE only; GET returns FlagError exit 2).
- envelope `meta.dry_run: true` + `meta.plan: {action, args | method+path+body}` open-map fields (omitempty in non-dry-run envelopes).
- `weknora session continue-stream <session-id> --message <msg-id>` command for SSE event stream replay/recovery.
- MCP `Tool.Annotations` on all 10 MCP serve tools (`destructiveHint` / `readOnlyHint` / `idempotentHint` / `openWorldHint` + `Title`) per MCP spec 2025-06-18.
- `cli/internal/cmdutil/risk.go`: `SetRisk(cmd, action)` helper + `RiskDestructive` const + `GetRisk(cmd)` reader.
- Help output "Risk: <action> (<level>)" line at top of 9 destructive commands' `--help` (via modified `SetAgentHelp` wrapper).
- `cli/AGENTS.md` sections: Stream recovery / Dry-run contract / Risk metadata.
- `cli/README.md` sections: Dry-run preview / Resuming streams.

#### Changed
- `SetAgentHelp` wrapper in `cli/internal/cmdutil/agenthelp.go` now prepends "Risk:" line in default (non-JSON) help branch when `cmd.Annotations["risk.action"]` is set. WEKNORA_AGENT_HELP=1 JSON path unchanged.
- 9 destructive commands' `SetAgentHelp` Warnings standardized: line 1 is a verbatim exit-10 / `-y` reminder; line 2 carries per-command destructive context.
- `cli/cmd/api/api.go` now has `SetAgentHelp` with runtime exit-10 note for `-X DELETE/PUT/PATCH`.
- `cli/cmd/doc/delete.go` Warnings adds a 3rd line describing `--all` blast radius.
- Bumped `github.com/modelcontextprotocol/go-sdk` v1.6.0 → v1.6.1 (patch; opt-in `MCPGODEBUG` env var).

#### Breaking
*(none — v0.8 is fully additive on top of v0.7 envelope shape, NDJSON vocab, and typed code contracts; existing consumers continue to work and the new fields are optional via `omitempty`)*

---

### v0.7 — Agent-first wire contract + command-surface cleanup

#### BREAKING (v0.6 → v0.7)
- **All JSON output now wrapped in symmetric envelope.**
  - Success on stdout: `{ok:true, data?:<T>, meta?, _notice?, profile?}` (`data`
    omitted on mutation-only success).
  - Error on stderr (json mode): `{ok:false, error:{type, message, hint?,
    retry_command?, retry_after_seconds?, risk?, detail?}, _notice?}`.
  - `meta.count` / `meta.has_more` surface list totals and server-side
    pagination state. `meta.next_cursor` / `meta.total_count` /
    `meta.request_id` are reserved — populated when the SDK exposes them
    (planned for v0.8).
  - Migration: replace `jq '.[]'` with `jq '.data[]'`; `.id` → `.data.id`;
    list-count consumers read `.meta.count`.
- **`--format` default flips to `json` regardless of TTY.**
  - v0.6: smart default (text on TTY, json on pipe).
  - v0.7: always json; TTY only affects indent (compact in pipe). Enum
    `text | json | ndjson` unchanged.
  - Migration: humans on a TTY pass `--format text` (or set
    `WEKNORA_FORMAT=text` env) for the prior auto-text behavior.
- **`chat` / `session ask` default to NDJSON event-stream (SDK passthrough).**
  - v0.6: TTY rendered a live SSE animation; `--format json` produced a buffered
    object; NDJSON was opt-in.
  - v0.7: `--format json` and `--format ndjson` both emit one JSON event per line
    (no envelope wrapping). CLI injects exactly one `init` event at stream head;
    all subsequent events pass through verbatim from the SDK (`answer` /
    `tool_call` / `tool_result` / `references` / `thinking` / `reflection` /
    `error` / `complete` for chat; agent vocab is a subset).
  - For prose rendering: `--format text`.
- **`weknora context` command group renamed to `weknora profile`.**
  - Subcommands `context list/add/remove/use` → `profile list/add/remove/use`.
  - Global flag `--context` → `--profile`.
  - On-disk config `~/.config/weknora/config.yaml` keys `current_context:` /
    `contexts:` → `current_profile:` / `profiles:` (no backwards-compat
    alias; delete the file or rename the keys by hand to migrate).
  - Binding file `.weknora/project.yaml` field `context:` → `profile:`
    (re-run `weknora link` to regenerate).
  - `profile use` JSON fields `current_context` / `previous_context` →
    `current_profile` / `previous_profile`.
  - `weknora link` JSON field `context` → `profile`.
  - Rationale: `context` collided with LLM "context window" / RAG "context" /
    Go `context.Context`. Mainstream multi-credential CLIs (AWS, Stripe,
    OpenAI, Anthropic) settle on `profile` as the term of art.
- **`weknora agent invoke` removed; use `weknora session ask --agent <id>`.**
  - Server route is `POST /sessions/{session_id}/agent-qa` — session-anchored.
  - `weknora agent` keeps CRUD only (list / view / create / edit / delete /
    status / check).
  - Migration: `weknora agent invoke ag_x "Q"` →
    `weknora session ask --agent ag_x "Q"` (auto-creates session if none given).
- **`weknora doc upload` split into three commands.**
  - `weknora doc upload <file>` — local file only.
  - `weknora doc fetch <url>` — server-side remote fetch (was `upload --from-url`).
  - `weknora doc create --text "..."` — direct text knowledge.
  - URL-only flags (`--title`, `--file-type`, `--tag-id`) moved to `doc fetch`.
  - Rationale: `upload --from-url` mixed semantics ("send out" vs "pull in");
    the three-verb split matches the server's three endpoints and gives each
    one a single unambiguous shape.
- **`weknora kb empty` removed; use `weknora doc delete --all --kb=<id>`.**
  - Atomic server `ClearKnowledgeBaseContents` (no list-then-delete race).
  - Same exit-10 `-y/--yes` guard as `kb delete`.
  - Migration: `weknora kb empty kb_x -y` →
    `weknora doc delete --all --kb=kb_x -y`.
- **`weknora api -d/--data` flag removed; use `--input <file>` or `--input -`
  (stdin).**
  - `weknora api` now accepts any non-empty HTTP method (whitelist removed)
    so the escape hatch can hit endpoints the CLI doesn't natively model.
  - Migration: `weknora api -d '{"foo":1}' /endpoint` →
    `echo '{"foo":1}' | weknora api --input - /endpoint`.
- **Batch operations envelope shape — per-item `ok` pattern.**
  - `weknora doc delete id1,id2,id3` and similar multi-id mutations now emit:
    `{ok, data:[{id, ok, result?|error?}, ...], meta:{count, successes, failures}}`.
  - Top-level `ok` = AND-aggregate of per-item `ok` (false on partial failure).
  - All-fail stays in batch shape (not error envelope) — agents can iterate
    detail per id.
  - jq pattern: `jq '.data[] | select(.ok == false) | .id'`.
- **MCP server tool errors now return `StructuredContent`.**
  - `CallToolResult{IsError: true, Content:[text-fallback],
    StructuredContent:{type, message, hint?, retry_command?, risk?, detail?}}`.
  - Shape mirrors stderr `envelope.error` sub-object — one parser handles both.
- **Unknown subcommand emits typed envelope.**
  - `input.unknown_subcommand` with `detail.{unknown, command_path, available[]}`
    + `retry_command: "<parent> --help"`. Replaces v0.6's free-form
    `"unknown command \"x\" for \"weknora\""` prose.
- **`weknora chat` requires the query as a single quoted argument.**
  - v0.6: `MinimumNArgs(1)` silently joined `weknora chat hello world` into
    `"hello world"`.
  - v0.7: `ExactArgs(1)` rejects multi-arg with exit 2; matches
    `weknora session ask`. Quote the query: `weknora chat "hello world"`.

#### Added
- **`WEKNORA_PROFILE` env var** selects the active profile for a single
  invocation (equivalent to `--profile <name>` global flag). Overridden by
  explicit `--profile`. Useful for CI scripts that cannot pass global flags.
- **`WEKNORA_FORMAT` env var** sets the default `--format`. Values:
  `text | json | ndjson`. Overridden by explicit `--format`. Invalid values
  ignored.
- **`error.retry_command`** — directly-executable retry argv, distinct from
  prose `hint`. Agents read `retry_command` without regex-parsing `hint`.
- **`error.retry_after_seconds`** — `server.rate_limited` / `server.timeout`
  surface server `Retry-After` header verbatim. CLI-direct (`weknora api`)
  parses HTTP `Retry-After` headers; SDK-mediated paths will gain coverage
  as the SDK exposes typed transport errors.
- **`error.risk.{level, action}`** — destructive writes carry
  `{level:"destructive", action:"<noun.verb>"}` (e.g. `doc.delete_all`,
  `kb.delete`). Reserved levels `"read"` / `"write"` not yet emitted.
- **`_notice` envelope channel reserved** — open-map infrastructure in place
  for deprecation / version_skew / security notices. Producer wiring planned
  for v0.8 when the SDK exposes version metadata. Additive non-breaking;
  unknown keys must be ignored.
- **`meta.count` / `meta.has_more`** on list commands. `meta.next_cursor` /
  `meta.total_count` / `meta.request_id` reserved — populated when the SDK
  exposes them (planned for v0.8).
- **`weknora doc fetch <url>`** — new command (see split above).
- **`weknora doc create --text "..."`** — new command (see split above).
- **`weknora session ask --agent <id> "..."`** — new command (replaces
  `agent invoke`).
- **`weknora doc delete --all --kb=<id>`** — new mode of `doc delete`
  (replaces `kb empty`).
- **NDJSON `init` event** at stream head for `chat` / `session ask` —
  carries `session_id` + optional `kb_id` / `agent_id` / `model` / `profile`.
  `request_id` field is reserved (not currently populated; planned for v0.8
  when the SDK exposes response headers).
- **`AgentHelp.Warnings`** — destructive commands (`kb delete`, `doc delete`,
  `agent delete`, `session delete`, `chunk delete`, `profile remove`,
  `kb edit`, `agent edit`, `auth logout`) render an "AI agents:" warnings
  block in `--help` to set explicit expectations around `-y/--yes`.

#### Changed
- `AGENTS.md` adds `## Wire contract for AI agents`, `## Deliberate deviations
  + mainstream alignments`, `## Pre-1.0 breaking policy`, `## Exit-10
  anti-patterns` sections.
- `README.md` adds `### Agent quick start` under `## Wire contract`.
- `chunk` command group help: disambiguation prose vs `search chunks` removed
  in favour of plain verb descriptions.

#### Deprecated (will remove in v0.8+)
- *(none — pre-release breaking release; no deprecation alias period.)*

---

### v0.6 — agent runtime hardening: --format, doc wait, --log-level, status, multi-id delete, paginate

#### BREAKING (v0.5 → v0.6)
- **`--json` flag removed** → use **`--format json`** (with optional
  `--jq '<expr>'` for projection / filtering). The v0.5 `--json=fields,...`
  per-field projection drops entirely; rewrite as
  `--format json --jq '.[] | {id, name}'` (jq is the canonical projection
  mechanism going forward).
- **`--no-stream` flag removed** on `chat` / `agent invoke` → use
  **`--format json`** to buffer the full answer before printing. The bare
  text-accumulate use case (TTY but no streaming) is dropped.
- **`WEKNORA_SDK_DEBUG=1` env removed** → use **`WEKNORA_LOG_LEVEL=debug`**.
- **`kb create --name <name>` flag removed** → use positional
  **`kb create <name>`** (consistent with `agent create <name>`).

#### Added
- **`--format text|json|ndjson`** flag selecting the stdout serialization.
  Registered per-command (only commands that honor `--format` register it;
  others reject it with `unknown flag` / exit 2). Output mode auto-resolved
  to `text` on a TTY and `json` when stdout was piped (v0.7 promoted the
  flag to a persistent global and made the default always `json`).
- **`--jq '<expr>'`** flag pairs with `--format json|ndjson` to filter or
  project the JSON output via a jq expression.
- **`weknora doc wait <id> [<id>...]`** — block until every document reaches a
  terminal `parse_status`. Always wait-all — use shell composition
  (`wait id1 && wait id2`) for fail-fast.
  - `--timeout DURATION` (default 10m; exit 124 on hit)
  - `--interval DURATION` (default 2s; exponential backoff to 15s + jitter)
  - Multi-id concurrent (max 5 parallel); exit code priority 1 > 124 > 0
- **`--log-level error|warn|info|debug`** persistent flag + `WEKNORA_LOG_LEVEL`
  env. Wires into the SDK's debug logger via the additive
  `client.SetDebugLevel(level string)` function.
- **`kb create --storage-provider <local|minio|cos|tos|s3|oss|ks3>`** —
  sets the new KB's `storage_provider_config.provider` at creation time
  (server only accepts it on create, not update). Required on self-hosted
  deployments where the server-side default doesn't pre-populate a
  provider — without it, subsequent `doc upload` returns `kb not found`.
- **`weknora kb status <id>`** — fast health snapshot (1 HTTP). Returns
  reachable / counts / is_processing.
- **`weknora kb check <id>`** — deep verification: status fields + `failed_count`
  aggregated via doc list page-walk (1 + N HTTP). The verb split between
  `status` (read state cheaply) and `check` (actively verify) communicates
  cost to the caller.
- **`weknora agent status <id>`** — fast health snapshot (1 HTTP):
  reachable / model_id.
- **`weknora agent check <id>`** — deep verification: status fields +
  `kb_scope_all_reachable` from probing each KB in scope (1 + N HTTP). Same
  status/check verb split as kb status/check.
- **`weknora doc delete <doc-id> [<doc-id>...]`** — positional multi-id.
  Default keep-going on failure. Single `-y/--yes` confirms the entire
  batch; non-TTY without `-y` still exits 10.
- **`weknora session delete <session-id> [<session-id>...]`** — positional
  multi-id with the same keep-going semantics as `doc delete`.
- **`weknora chunk delete <chunk-id> [<chunk-id>...] --doc <doc-id>`** — positional
  multi-id, all chunks share the same `--doc` parent (server route requires it).
- **`weknora api <path> --paginate`** — follows weknora's offset-based
  pagination (`?page=N&page_size=M`) and merges all pages into a single
  `{data, total}` JSON response.
- **MCP `chat` and `agent_invoke` tools** output schemas extended with
  `thinking` / `tool_calls` / `assistant_message_id`. Tool descriptions
  callout "server-side accumulated, NOT streaming" (MCP tools/call has
  no standard partial-response).
- **`SetAgentHelp` pattern** — `cmdutil.SetAgentHelp(cmd, AgentHelp{...})`
  exposes a stable JSON used_for / required_flags / examples / output
  shape, activated by `WEKNORA_AGENT_HELP=1` at `--help` time. Applied
  to `chat` and `kb list` as proof-of-pattern; extending to another
  command requires touching only that command's `NewCmd`.
- **`cli/AGENTS.md`** gains an "Error code reference" section (35 typed
  codes + exit codes + retryable / hint), with `<!-- ERROR_REFERENCE_START -->`
  markers and CI parity test (`errors_doc_test.go`) — every new typed
  code in `AllCodes()` must be documented or CI fails.
- New `operation.*` typed error namespace for CLI-level wait/poll outcomes:
  - `operation.timeout` → exit 124 (distinct from `server.timeout` → exit 7;
    matches the convention from GNU `timeout(1)`). Used by `doc wait` and
    any future CLI-level wait/poll surfaces.
  - `operation.failed` → exit 1. Emitted when one or more wait targets
    reach a terminal failure (`doc wait` finds `parse_status=failed`) or
    when multi-id `delete` rolls up partial failures. Distinct from
    `server.error` because the failure is the target's own terminal state,
    not a transient transport issue — `server.error`'s "retry with backoff"
    hint would be misleading.
  - `operation.cancelled` → exit 1, raised to **130** by `main.go` when the
    root context was signal-cancelled. Surfaced by chat / agent invoke /
    doc wait on Ctrl-C or SIGTERM. Carries a hint pointing at the signal,
    not at `-y/--yes` (which would have been the misleading
    `local.user_aborted` hint).
- **Signal-aware root context** — `main.go` wires `signal.NotifyContext` for
  SIGINT and SIGTERM so long-running commands observe `ctx.Done()` and run
  their cancellation cleanup (re-emit auto-created session id, return
  `operation.cancelled`); the process exits 130 whenever the context was
  signal-cancelled, matching Unix signal convention.
- **MCP tool input renames for consistency**: `doc_view` and `doc_download`
  now accept `doc_id` (was `knowledge_id`) so every MCP tool that
  references a document uses the same parameter name as `chunk_list` and
  the CLI's `<doc-id>` positional.
- `WriteNDJSON` helper in `internal/format/` (per http://ndjson.org:
  arrays split per-line, single records emit one line).

#### Changed
- `cli/README.md` "Exit codes" subsection extended with `124`
  (`operation.timeout`); rows for `1` and `130` now name `operation.failed`
  and `operation.cancelled` alongside the existing groupings.
- `cli/README.md` gains a "Status / check verb pair" subtable under "Health
  check" and a `doc wait` paragraph with full exit-code list (0/1/124/130).
- `cli/AGENTS.md` gains design SOPs for **Status / check verb pair pattern**
  and **Long-poll wait commands**, plus a note on the SetAgentHelp pattern
  and current coverage (chat / kb list).
- **Multi-id delete partial-failure exit code**: `doc delete` /
  `session delete` / `chunk delete` (multi-id mode) now exit `1`
  (`operation.failed`) when some targets fail, rather than exit `7`
  (`server.error`). The retry-with-backoff hint for server.* would have
  misled callers when the actual cause is a target's terminal state.
- **`doc upload` with no path / no `--from-url`** now exits `2`
  (`FlagError`, matching cobra's `MinimumNArgs` convention for commands
  that need a positional), rather than `5` (`input.invalid_argument`).
- **`--log-level` invalid value** exits `2` (`FlagError`) for consistency
  with `--format` invalid-value behaviour. Env values still fall through
  silently (env is best-effort).
- **Multi-id delete stdout contract**: pre-flight failures (e.g. missing
  `-y` confirmation) no longer emit the empty `{ok, failed}` envelope to
  stdout — stdout stays empty per the wire contract in README.md, the
  typed error goes to stderr only.
- **Positional id help strings now namespaced** for clarity in both human
  help and agent `--help` parsing: `<id>` → `<kb-id>` / `<doc-id>` /
  `<session-id>` on kb / doc / session subtrees. `agent` and `chunk`
  subtrees were already namespaced. Pure help-text change — argument
  parsing is unchanged.
- `chat "<text>"` Use string now shows quotes — matches `agent invoke` and
  `search chunks` quoting hint for queries that contain spaces.

#### SDK additions (strictly additive)
- `client.SetDebugLevel(level string)` — programmatic control over the SDK's
  internal slog debug logger.

### v0.5 — agent CRUD, chunk subtree, MCP chunk_list, audit-driven cleanup

#### Added
- `weknora agent create <name> --model <id>` / `agent edit <id>` /
  `agent delete <id>` — hybrid surface (hot-path flags for the common
  fields + `--config-file` YAML/JSON for the long tail +
  `--generate-skeleton` template emit). `--from <agent-id>` copies
  from an existing agent.
- `weknora chunk list --doc <doc-id>` / `chunk view <chunk-id>` /
  `chunk delete <chunk-id> --doc <doc-id>` — new subtree for RAG retrieval
  debug. Paginated with v0.4 `--limit` / `--page-size` / `--all-pages` canon.
- `weknora mcp serve` adds `chunk_list` as the 10th curated tool.
- `weknora agent view <id>` human output now renders all 34 AgentConfig
  fields (previously 7), grouped into 10 presentation sections.
- `--all-pages` / `--page-size` on `search docs` and `search sessions`
  (catching up with `session list` / `doc list` canon from v0.3+v0.4).
- `weknora doc list` gains `--keyword` / `--file-type` / `--source` /
  `--tag-id` / `--start-time` / `--end-time` (RFC3339) — matches the
  SDK's `KnowledgeListFilter` surface. Time flags reject malformed
  input with `input.invalid_argument`.
- MCP `doc_list` tool gains the same 6 filter fields (`keyword`,
  `file_type`, `source`, `tag_id`, `start_time`, `end_time`) so agents
  have parity with the CLI.
- `weknora session view --full` (with `--limit`, default 50, bounds
  1..1000) loads chat history via `LoadMessages` and renders messages
  inline after session metadata. JSON mode projects messages into a
  `messages` array. `--limit` without `--full` errors with
  `input.invalid_argument`.
- `weknora kb view` human render now includes `TYPE`, `PINNED` (badge,
  only when set), `TEMPORARY` (badge), `PROCESSING` (with doc count,
  only when active), `SUMMARY MODEL`, and `CREATED`. Nested config
  structs stay JSON-only.
- `weknora doc view` human render expands to include `TITLE` (when
  distinct from filename), `DESC`, `SOURCE`, `CHANNEL`, `TAG`,
  `STORAGE` (human-readable bytes), `SUMMARY`, `ENABLED`, and `HASH`
  (12-char prefix). All omit-empty.
- `weknora doc upload` gains `--enable-multimodel` (tri-state:
  unset/true/false), repeatable `--metadata key=value`, and
  `--channel` flags. `--enable-multimodel` and `--channel` apply to
  file / `--recursive` / `--from-url`; `--metadata` is file /
  `--recursive` only (the URL-ingest request carries no metadata
  field server-side, so passing it with `--from-url` is rejected
  up-front as `input.invalid_argument`). URL mode additionally
  accepts `--title`, `--file-type`, and `--tag-id`. Threads through
  to the SDK's `CreateKnowledgeFromFile` / `CreateKnowledgeFromURL`
  signatures (previously hardcoded to nil/"api" and dropped URL
  extras).

#### Fixed
- MCP `search_chunks` tool: `limit` arg now correctly threads into
  `SearchParams.MatchCount`. Previously the server's default cap won,
  silently capping below the requested limit.
- `search sessions` human time format: now renders a relative
  duration ("2 hours ago") matching `session list`, instead of raw
  RFC3339.
- `doc upload` (file path): re-uploading a file already ingested into
  the KB now surfaces as `resource.already_exists` (exit 1) instead of
  the misleading `network.error` ("check base URL reachability"). The
  SDK returns its `ErrDuplicateFile` sentinel with no `HTTP error <n>:`
  prefix because the duplicate is detected via file-hash short-circuit,
  not by HTTP status; the previous fall-through to `WrapHTTP` therefore
  misclassified it. The `--from-url` branch already handled the
  symmetric `ErrDuplicateURL` correctly.

#### Breaking changes
- `weknora search docs` now applies the keyword filter server-side via
  `ListKnowledgeWithFilter` (was: page through every doc and
  substring-match client-side). Smaller wire payload on large KBs.
  **The match is now case-sensitive** (server uses `LIKE %keyword%`),
  whereas the previous client-side path lowered both sides. Callers
  that relied on case-insensitive matching (e.g. `search docs Q3`
  finding `q3 retro`) must lower-case the query themselves, or fall
  back to `weknora api` with a custom filter.

#### Changed
- `cli/AGENTS.md` MCP curation rationale rewritten: curated read-only
  is a deliberate product call gated on the absence of server-side
  per-token scope. When server-side scope ships, mutation tools can
  land in the MCP surface.
- `cli/AGENTS.md` adds "Command surface design SOP" and "CRUD command
  flag canon" sections for future contributors. The design-SOP
  section includes a step reminding contributors to decide
  flag-vs-escape-hatch per field rather than trying to flag-mirror
  every SDK capability.
- `cli/README.md` now documents the `weknora api` raw HTTP passthrough
  as the canonical escape hatch for deep KB config, per-request `chat`
  / `agent invoke` overrides, and operations without a CLI verb.

### v0.4 — output contract hardening and mainstream alignment

#### Breaking changes
- Dropped the JSON envelope. `stdout` now emits bare typed data
  (`{...}` or `[...]`); errors are written to `stderr` as `code: msg`
  with an actionable `hint:` line. Pipelines using `--json | jq` no
  longer have to filter out an envelope wrapper.
- Dropped `--dry-run`. Destructive writes still require `-y/--yes`;
  non-TTY callers that omit `-y` exit with code 10 and
  `input.confirmation_required` so an agent must surface the prompt
  to a human before retrying.
- Dropped the per-command AI footer that rendered when AI-coding-agent
  env detection fired. The same machine-readable guidance now lives in
  the standard `--help` (visible to all callers) and in `mcp serve`'s
  tool descriptions.

#### Added
- `weknora mcp serve` — curated read-only stdio MCP server exposing 9
  tools (`kb_list`, `kb_view`, `doc_list`, `doc_view`, `doc_download`,
  `search_chunks`, `chat`, `agent_list`, `agent_invoke`). Destructive
  verbs are intentionally excluded.
- `weknora agent list` / `agent view` / `agent invoke` — manage and
  call WeKnora's server-side Custom Agent resources.
- `weknora auth token` — print the active credential to `stdout` for
  scripting (raw secret by default; `--json` emits `{token, mode, context}`).
- `weknora doc upload --from-url` — ingest a remote URL.
- `--json=fields,...` field projection and `--jq <expr>` filtering on
  every command that emits JSON.
- `--limit` and `--all-pages` on list / search commands for bounded
  output and explicit pagination control.
- Per-resource filter flags: `kb list --pinned`, `doc list --status`,
  `session list --since`.

#### Changed
- Go toolchain bumped from 1.24 to 1.26.
- `auth login --with-token` validates the supplied key against
  `/auth/me` before persisting, and prints an advisory if the keyring
  is unavailable and credentials fall back to a 0600 file under
  `$XDG_CONFIG_HOME/weknora/secrets/`.
- AGENTS.md rewritten as a developer guide (~170 lines, 6 H2 sections).

### v0.3 — extended management surface and a `session` subtree

#### Added
- `context add` / `context list` / `context remove` — first-class CRUD over
  connection targets (previously implicit via `auth login --name`).
  Removing the *current* context requires explicit `-y` (exit-10 protocol)
  because subsequent commands have no default target.
- `auth refresh` — exchanges the stored refresh token for a new access +
  refresh pair (OAuth refresh-token grant). Transparent 401 → refresh →
  retry is also wired into the SDK transport with singleflight de-dup, so
  most callers never need to invoke this explicitly.
- `kb edit` — partial-update edit with only-sent-fields semantics
  (`*string` options so unset fields stay unset in the PUT body).
- `kb pin` / `kb unpin` — idempotent pin/unpin toggle; no-op when already
  in the target state (emits `_meta.warnings`, no server call).
- `kb empty` — bulk-delete documents while preserving the KB record and
  its config. High-risk-write; exit-10 confirmation in non-TTY / `--json`
  paths.
- `doc view <id>` — show one document's metadata (title, file name,
  type, size, parse status, embedding model, processed-at, error
  message). Counterpart to `kb view` and `session view`.
- `doc download` — stream a knowledge file to disk (`-O FILE` /
  `-O -` for stdout) with `--clobber` controlling overwrite. Rejects
  server-supplied path-like filenames; partial writes on error are
  cleaned up.
- `doc upload --recursive --glob '*.md'` — walk a directory and upload
  every match. Per-file `OK` / `FAIL` progress lines on the human path;
  aggregated `uploaded[]` / `failed[]` envelope on `--json`. Exit code
  typed to the first failure's class on partial failure.
- `search chunks` / `search kb` / `search docs` / `search sessions` —
  verb-noun subtree (gh `search code/repos/issues/…` shape). `search
  chunks` is hybrid (vector + keyword) retrieval; the other three are
  client-side substring filters useful for discovering identifiers.
  All four take `--limit N` / `-L N` (1..1000) to cap returned rows.
- `session list` / `session view` / `session delete` — chat session
  management.
- `api --input FILE` / `api --input -` — body source for raw HTTP
  passthrough (file or stdin); mutually exclusive with `--data`.
- `unlink` — remove the cwd's `.weknora/project.yaml` so subsequent
  commands stop auto-resolving `--kb` from it. Walks up from cwd so a
  user in a subdirectory can unlink without cd-ing to the project root.
- Completion smoke test guards against cobra bumps silently breaking
  bash / zsh / fish / powershell completion.

#### SDK additions (Go client at `client/`, strictly additive)
- `OpenKnowledgeFile(ctx, id) (filename, body io.ReadCloser, err)` — new
  primitive returning the body as a stream plus the server-suggested
  Content-Disposition filename. `DownloadKnowledgeFile` is now a thin
  wrapper (signature unchanged, gained partial-file-on-error cleanup).
- `WithTransport(http.RoundTripper) ClientOption` — lets the CLI install
  the 401-retry transport.
- `PathAuthLogin` / `PathAuthRefresh` constants — so HTTP middleware
  doesn't re-hardcode the literals.
- `IsPinned bool` field on `KnowledgeBase` (server already returned it;
  SDK just hadn't modeled it).
