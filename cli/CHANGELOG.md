# Changelog — `weknora` CLI

All notable changes to the `weknora` CLI (the binary under `cli/` in this
repository) will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)
and the CLI follows [Semantic Versioning](https://semver.org/) independently
of the WeKnora server / frontend release cadence.

CLI history before v0.3 is recorded in the project root
[CHANGELOG.md](../CHANGELOG.md) under the release that introduced the CLI.

## [Unreleased]

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
- Dropped the per-command AI footer that rendered when `CLAUDECODE`
  or `CURSOR_AGENT` was set. The same machine-readable guidance now
  lives in the standard `--help` (visible to all callers) and in
  `mcp serve`'s tool descriptions.

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
  user in a subdirectory can unlink without cd-ing to the project root
  (mirrors `vercel unlink` / `netlify unlink`).
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
