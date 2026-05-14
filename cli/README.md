# weknora — WeKnora CLI

A command-line interface for the WeKnora RAG knowledge-base server. Lets you
authenticate, manage knowledge bases and documents, run hybrid search, and
ask streaming RAG questions from your terminal or from an AI agent.

```bash
$ weknora --help
WeKnora CLI lets you authenticate, browse knowledge bases, and run
hybrid searches against a WeKnora server from your shell or an AI agent.

Available Commands:
  agent       Manage and invoke custom agents
  api         Make a raw API request to the WeKnora server
  auth        Manage authentication credentials and contexts
  chat        Ask a streaming RAG question against a knowledge base
  completion  Generate the autocompletion script for the specified shell
  context     Manage CLI contexts (named connection targets)
  doc         Manage documents in a knowledge base
  doctor      Run 4 self-checks: base URL, auth, server version, credential storage
  help        Help about any command
  kb          Manage knowledge bases
  link        Bind the current directory to a knowledge base
  mcp         Run weknora as a Model Context Protocol server
  search      Search across chunks, knowledge bases, documents, or sessions
  session     Manage chat sessions
  unlink      Remove the directory's knowledge-base binding
  version     Show CLI build metadata
```

The command surface mirrors `gh` CLI's `<noun> <verb>` convention. The
wire contract for AI agents (Claude Code, Cursor, Aider, …) is documented
[below](#wire-contract). For contributing to the CLI source, see
[AGENTS.md](AGENTS.md).

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

Pre-built binaries for Linux / macOS / Windows are produced by CI on each
release. Grab the latest from the [Releases page](https://github.com/Tencent/WeKnora/releases).

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
weknora search chunks "what is reciprocal rank fusion?"

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

## Wire contract

Designed to be agent-first. Stable across minor releases; breaking
changes announced in the changelog and the corresponding
`weknora --version` bump.

### Streams

- **stdout** is the data channel: bare JSON with `--json`, or
  human-formatted output. Never carries error text.
- **stderr** is logs, progress, warnings, and errors. A non-empty
  stderr does **not** mean failure — read the exit code.

### JSON output

Every command supports `--json`, emitting bare JSON for the resource it
produces — an array for `list` / `search`, a single object for `view`
and write outcomes:

```bash
weknora kb list --json                        # [{ "id": "kb_x", "name": "Eng" }, …]
weknora kb view kb_x --json                   # { "id": "kb_x", "name": "Eng", … }
weknora kb list --json=id,name                # project to listed fields
weknora kb list --json --jq '.[].id'          # jq over the bare data
```

Note the `=` form for projection: pflag's optional-value parser treats
space-separated arguments after a bare `--json` as positionals, so
`--json id,name` would be interpreted as bare `--json` + the positional
`id,name`. Always use `--json=field,...`.

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
`server.*` / `network.*` / `local.*` / `mcp.*`.

### Exit codes

| Code | Meaning | Agent action |
|---|---|---|
| `0`   | success                                                | continue |
| `1`   | typed `local.*` or unclassified                        | read stderr, decide retry/abort |
| `2`   | flag / argument validation error                       | re-check `weknora <cmd> --help` |
| `3`   | `auth.*` (token missing / expired / forbidden)         | re-auth, then retry |
| `4`   | `resource.not_found`                                   | verify the resource id |
| `5`   | `input.*` (other than `confirmation_required`)         | adjust args, retry |
| `6`   | `server.rate_limited`                                  | back off, retry |
| `7`   | `server.*` / `network.*`                               | transient — retry with backoff |
| `10`  | **`input.confirmation_required`** (high-risk write)    | ask the human, retry with `-y` only after explicit approval |
| `130` | cancelled (SIGINT / Ctrl-C)                            | stop, do not retry |

**Exit 10** is the wire-level signal for "destructive write needs
explicit confirmation". Pass `-y/--yes` on `kb delete` / `kb empty` /
`doc delete` / `session delete` / `context remove` (on the current
context) when running headless. **Never auto-add `-y` without the
user's explicit go-ahead** — exit 10 is the guard against unintended
writes.

### Other agent ergonomics

- For chat / agent invoke in agent contexts, prefer `--no-stream --json`
  — streaming tokens to stdout makes JSON parsing impossible.
- `--json` composes with the global `--context <name>` for single-shot
  context overrides without disk writes.
- `weknora mcp serve` exposes a curated readonly tool surface over
  stdio MCP for Claude Desktop / Code / custom MCP clients.

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
macOS / Windows × Go 1.26, path-filtered to changes under `cli/`.

---

## License

MIT — see the repository [LICENSE](../LICENSE).
