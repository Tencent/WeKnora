# weknora — WeKnora CLI

A command-line interface for the WeKnora RAG knowledge-base server. Lets you
authenticate, manage knowledge bases and documents, run hybrid search, and
ask streaming RAG questions from your terminal or from an AI agent.

```bash
$ weknora --help
WeKnora CLI lets you authenticate, browse knowledge bases, and run
hybrid searches against a WeKnora server from your shell or an AI agent.

Available Commands:
  api         Make a raw API request to the WeKnora server
  auth        Manage authentication credentials and contexts
  chat        Ask a streaming RAG question against a knowledge base
  context     Manage CLI contexts (named connection targets)
  doc         Manage documents in a knowledge base
  doctor      Run 4 self-checks: base URL, auth, server version, credential storage
  kb          Manage knowledge bases
  link        Bind the current directory to a knowledge base
  search      Hybrid (vector + keyword) chunk retrieval against a knowledge base
  version     Show CLI build metadata
```

The command surface mirrors `gh` CLI's `<noun> <verb>` convention. See
[AGENTS.md](AGENTS.md) for the operational contract that AI agents
(Claude Code, Cursor, Aider, …) can rely on: envelope schema, exit-code
protocol, error-code registry, and per-command guidance.

---

## Install

### From source

Requires Go 1.24+.

```bash
git clone https://github.com/Tencent/WeKnora.git
cd WeKnora/cli
go build -o weknora .
sudo mv weknora /usr/local/bin/   # or anywhere on $PATH
```

### Pre-built binaries

Pre-built binaries for Linux / macOS / Windows are produced by CI on each
release. Grab the latest from the [Releases page](https://github.com/Tencent/WeKnora/releases)
once v0.2 ships.

---

## 5-minute quickstart

```bash
# 1. Log in to your WeKnora server (interactive password prompt)
weknora auth login --host https://kb.example.com

# 2. Or pipe an API key from stdin (for CI / agents)
echo "sk-..." | weknora auth login --host https://kb.example.com --with-token

# 3. List knowledge bases
weknora kb list

# 4. Bind this directory to a knowledge base — subsequent commands auto-resolve --kb
weknora link --kb my-knowledge-base

# 5. Upload a document
weknora doc upload notes.md

# 6. Search
weknora search "what is reciprocal rank fusion?"

# 7. Ask the LLM (streams to terminal)
weknora chat "summarise the design doc"
```

---

## Multi-context

Switch between several WeKnora servers (or several tenants on the same server)
without re-logging in:

```bash
weknora auth login --host https://prod.example.com    --name prod
weknora auth login --host https://staging.example.com --name staging --with-token < .staging-key
weknora auth list
weknora context use prod
```

Credentials are persisted to your OS keyring (Keychain on macOS, libsecret on
Linux, Wincred on Windows) when available, otherwise to a 0600-mode file
under `$XDG_CONFIG_HOME/weknora/secrets/`. The active context lives in
`~/.config/weknora/config.yaml`.

To remove a context's stored credentials:

```bash
weknora auth logout                  # current context
weknora auth logout --name staging   # specific
weknora auth logout --all
```

---

## JSON envelope output

Every command supports `--json`, returning a stable envelope shape:

```json
{
  "ok": true,
  "data": { /* command-specific payload */ },
  "_meta": { "context": "prod", "kb_id": "kb_abc" }
}
```

On error:

```json
{
  "ok": false,
  "error": {
    "code": "auth.unauthenticated",
    "message": "...",
    "hint": "run `weknora auth login`"
  }
}
```

The full schema, error-code registry, and exit-code protocol (0 / 1 / 2 / 10
/ 130) are documented in [AGENTS.md](AGENTS.md).

---

## Agent / scripting integration

Designed to be agent-first:

- `--dry-run` previews any write command (kb create/delete, doc
  upload/delete, api POST/PUT/PATCH/DELETE) without hitting the server,
  emitting an envelope with `risk` classification and `dry_run: true`.
- `-y/--yes` skips confirmation prompts for high-risk writes. **Without
  `-y` on a non-TTY/`--json` invocation, destructive commands return
  `error.code: input.confirmation_required` and exit code 10** so an
  agent can ask the user before retrying.
- `--json` coexists with the global `--context <name>` for single-shot
  context override.
- Set `CLAUDECODE` or `CURSOR_AGENT` environment variables to surface
  per-command "AI agents:" guidance in `--help` output.

---

## Health check

Run `weknora doctor` for a 4-status diagnostic (OK / warn / fail /
skip) covering base URL reachability, authentication, server-CLI
version skew, and credential storage backend. Add `--json` for
machine-readable output, `--offline` to skip network checks.

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
macOS / Windows × Go 1.24, path-filtered to changes under `cli/`.

---

## License

MIT — see the repository [LICENSE](../LICENSE).
