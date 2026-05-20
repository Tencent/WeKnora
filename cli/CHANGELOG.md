# Changelog — `weknora` CLI

All notable changes to the `weknora` CLI (the binary under `cli/` in this
repository) will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)
and the CLI follows [Semantic Versioning](https://semver.org/) independently
of the WeKnora server / frontend release cadence.

CLI history before v0.3 is recorded in the project root
[CHANGELOG.md](../CHANGELOG.md) under the release that introduced the CLI.

## [Unreleased]

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
  others reject it with `unknown flag` / exit 2). Output mode auto-resolves
  to `text` on a TTY and `json` when stdout is piped, so
  `weknora kb list | jq` works without an explicit flag.
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
