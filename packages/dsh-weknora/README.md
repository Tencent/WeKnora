# @wxg-prc-cpg/dsh-weknora

English | [简体中文](./README_CN.md)

[npm](https://www.npmjs.com/package/@wxg-prc-cpg/dsh-weknora)

A [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) plugin that gives the harness agent
first-class access to a [WeKnora](https://github.com/Tencent/WeKnora) knowledge base: hybrid retrieval, full document
reading, and WeKnora's own composed answers with citations.

dsh ships no retrieval, embedding or knowledge-base capability of its own — its search tools (`grep`, `glob`) read the
workspace, and `web_search` reads the internet. This plugin fills that gap with your organization's documents.

## Install

```sh
# from npm (recommended: installs prebuilt code, no build permission needed)
dsh plugin --profile web add @wxg-prc-cpg/dsh-weknora

# or from a checkout of this repository
dsh plugin --profile web add ./packages/dsh-weknora
```

Then point it at your deployment. The shipped configuration layer reads environment variables, so the quickest start is:

```sh
export WEKNORA_BASE_URL=https://weknora.example.com   # or http://localhost:8080
export WEKNORA_API_KEY=sk-...                          # a WeKnora API key with `retrieve` (+ `chat` for ask)
export WEKNORA_KNOWLEDGE_BASE_IDS=kb-123,kb-456        # optional default scope
export WEKNORA_RESOURCE_URLS=public                    # default; `handle` keeps internal file refs
dsh web
```

For anything beyond that, override the row from your profile's `cordis.patch.yml`
(`$DSH_HOME/profiles/<name>/cordis.patch.yml`):

```yaml
- id: weknora
  config:
    baseUrl: https://weknora.example.com
    apiKey: !!js process.env.WEKNORA_API_KEY
    knowledgeBaseIds:
      - kb-product-docs
    agentId: ''            # a WeKnora custom agent id makes `weknora_ask` use the ReAct pipeline
    maxResults: 8
    maxChunkChars: 1200
    requestTimeoutMs: 30000
    chatTimeoutMs: 300000
    resourceUrls: handle   # default is `public` so cited images are directly loadable; `handle` keeps internal refs
    toolPrefix: weknora    # rename the tools, e.g. to mount two deployments side by side
    tools:
      listKnowledgeBases: true
      search: true
      readDocument: true
      ask: true
```

A patch replaces the row's whole `config`, so restate every field you keep. Invalid configuration fails the plugin load
with a message naming each violation, rather than failing inside the first tool call.

Mounting two deployments is two rows with two prefixes:

```yaml
- insert:
    - id: weknora-internal
      name: "@wxg-prc-cpg/dsh-weknora"
      config: { baseUrl: https://kb.internal.example.com, toolPrefix: internal_kb }
    - id: weknora-public
      name: "@wxg-prc-cpg/dsh-weknora"
      config: { baseUrl: https://kb.public.example.com, toolPrefix: public_kb }
```

## Tools

| Tool | WeKnora endpoint | What the model gets |
|---|---|---|
| `weknora_list_knowledge_bases` | `GET /knowledge-bases` | Knowledge base names and ids, to report what exists or to narrow a later search |
| `weknora_search` | `POST /knowledge-search` + `GET /knowledge/search` | Ranked passages verbatim, each with a `knowledge_id`, score and chunk index, plus any document the query names |
| `weknora_read_document` | `GET /chunks/:knowledge_id` + `GET /knowledge/:id` | One document's passages reassembled in order, led by its title and summary, with paging |
| `weknora_ask` | `POST /sessions` + `POST /knowledge-chat/:id` or `POST /agent-chat/:id` | WeKnora's own answer, its citations, the server-side tools it used, and a resumable `session_id` |

`weknora_search` is the workhorse: it returns the source text for the agent to reason over, which keeps the agent's own
reasoning auditable. It answers two questions the model cannot always tell apart — *where is this discussed* and *where
is the document called X* — by matching passage content and document names in the same call. A query that reads like a
title additionally reports the documents it names, so a document whose wording differs from its own title is still
reachable.

Scope works without configuration. An unscoped call is refused by the retrieval endpoint and answered from nothing by
the RAG one, so when `knowledgeBaseIds` is empty the plugin resolves every knowledge base the credential can see and
uses all of them, resolving that list once per process and sharing it between `weknora_search` and `weknora_ask`.
Making the model choose first would be worse: knowledge bases are frequently named too poorly to choose between.

`weknora_ask` delegates the whole question to WeKnora. Reserve it for broad or synthesis questions spanning many
documents, where retrieving passages yourself would take several rounds — it runs another model server-side, so it is
slow, and it returns a conclusion rather than the evidence behind it. With `agentId` set it leaves the scope alone: a
custom agent resolves its own from its KB selection mode, and ids sent from here would override that.

`weknora_read_document` exists because retrieval returns fragments: once a passage looks right, the agent usually needs
its neighbours. Every search hit carries the `knowledge_id` that call needs, and page 1 leads with the document's title
and WeKnora's generated summary so a long document can be judged without paging through it.

In [Code Mode](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/core/tools/README.md) the same tools
are available as `await tools.weknora_search({ query })`, which is a good fit for multi-hop retrieval: one program can
fan out across ten sub-questions without ten model round trips.

## Permissions and data flow

The plugin only reads. It never writes to WeKnora: no ingestion, no chunk edits, no deletion. A WeKnora API key scoped
to `retrieve` is enough for three of the four tools; `weknora_ask` additionally needs `chat`, because it creates a
session and streams an answer.

The key is sent as `X-API-Key`. Set `tenantId` as well if you use a platform-scoped key, which needs `X-Tenant-ID` to
select a workspace. Errors are reported with the HTTP status and WeKnora's own reason, never with the key.

Passage content is clipped to `maxChunkChars` before it reaches the model, so one oversized document cannot eat the
context window. The clip is reported to the model (`truncated: true`) instead of silently hiding text.

With the default `resourceUrls: public`, WeKnora rewrites cited `resource://` figures into directly loadable `http(s)`
URLs. Copy those Markdown images into the assistant reply and the dsh web UI will render the picture. A
knowledge-base-restricted API key cannot receive public URLs (WeKnora answers 403); the plugin retries that call in
handle mode and keeps using handles afterwards.

The shipped configuration layer also reads `WEKNORA_RESOURCE_URLS` (`public` or `handle`).

## Configuration reference

| Field | Default | Notes |
|---|---|---|
| `baseUrl` | `http://localhost:8080/api/v1` | `/api/v1` is appended when missing |
| `apiKey` | unset | `X-API-Key`; unset means an unauthenticated deployment |
| `tenantId` | unset | `X-Tenant-ID`, required for platform-scoped keys |
| `knowledgeBaseIds` | `[]` | Default scope when a call names none; empty means every knowledge base the credential can see |
| `agentId` | unset | Sends `weknora_ask` to the ReAct pipeline |
| `maxResults` | `8` | Also the ceiling for `max_results` and for cited references |
| `maxChunkChars` | `1200` | Per-passage character budget |
| `requestTimeoutMs` | `30000` | Retrieval and document reads |
| `chatTimeoutMs` | `300000` | `weknora_ask`, which waits on WeKnora's own model calls |
| `resourceUrls` | `public` | `public` returns directly loadable file URLs; `handle` keeps internal `resource://` refs. A knowledge-base-restricted API key is retried as `handle`. Override with `WEKNORA_RESOURCE_URLS`. |
| `toolPrefix` | `weknora` | Tool-name prefix, `^[a-z][a-z0-9_]*$` |
| `tools.*` | all `true` | Register fewer tools to spend fewer prompt tokens |

## Development

```sh
npm install
npm test          # builds, then runs the unit and contract tests
npm run typecheck
```

The end-to-end check installs the package into a throwaway dsh profile, boots the headless profile against a mock
WeKnora backend and a deterministic OpenAI-compatible model, and asserts that the harness really called the tools and
answered from what they returned:

```sh
npm run build
node test/e2e/run-in-dsh.mjs                        # installs dsh from npm on first run
DSH_BIN=/path/to/dsh node test/e2e/run-in-dsh.mjs   # or reuse an existing install
```

dsh needs a Node build with zstd support (Node ≥ 22.15 or ≥ 24) for its session persistence, and pnpm ≥ 10 on `PATH`,
because `dsh plugin add` installs into the profile through pnpm and the profile it writes keeps its settings in
`pnpm-workspace.yaml`. The plugin itself runs on Node ≥ 20.11 and needs neither.

`test/fixtures/api-contract.json` records every WeKnora call this plugin makes. `test/contract.test.mjs` asserts the
plugin still emits exactly those calls, and `contract/contract_test.go` asserts WeKnora's real Go request and response
types still accept and serve them — so a rename on either side fails CI instead of a user's agent.

## Compatibility

Verified against dsh `0.1.0-rc.8` and WeKnora `0.7.2`. dsh is in developer preview and states that breaking changes
will happen; this package deliberately has **no runtime dependencies** and hands `ctx.tools.register()` a plain object,
so it does not pin any harness package version. If a harness release changes the tool-definition contract, open an issue
against this repository.

## License

MIT, same as WeKnora and dsh.
