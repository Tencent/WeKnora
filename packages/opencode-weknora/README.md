# @wxg-prc-cpg/opencode-weknora

[简体中文](./README_CN.md)

[npm](https://www.npmjs.com/package/@wxg-prc-cpg/opencode-weknora)

An [opencode](https://opencode.ai) plugin that gives the coding agent first-class access to a
[WeKnora](https://github.com/Tencent/WeKnora) knowledge base: hybrid retrieval, full document reading, and WeKnora's
own composed answers with citations.

opencode ships no retrieval or knowledge-base capability of its own — its tools read the workspace and the internet.
This plugin fills that gap with your organization's documents, ported from WeKnora's own
[`dsh-weknora`](https://github.com/Tencent/WeKnora/tree/main/packages/dsh-weknora) (DeepSeek Harness plugin).

## Install

In your opencode config (`opencode.json`), add the plugin and point it at your deployment:

```json
{
  "plugin": [
    ["@wxg-prc-cpg/opencode-weknora", { "baseUrl": "https://weknora.example.com", "apiKey": "sk-..." }]
  ]
}
```

The quickest start is environment variables (options override them):

```sh
export WEKNORA_BASE_URL=https://weknora.example.com   # or http://localhost:8080
export WEKNORA_API_KEY=sk-...                          # a WeKnora API key with `retrieve` (+ `chat` for ask)
export WEKNORA_KNOWLEDGE_BASE_IDS=kb-123,kb-456        # optional default scope
```

Mounting two deployments side by side is two rows with two prefixes:

```json
{
  "plugin": [
    ["@wxg-prc-cpg/opencode-weknora", { "baseUrl": "https://kb.internal.example.com", "toolPrefix": "internal_kb" }],
    ["@wxg-prc-cpg/opencode-weknora", { "baseUrl": "https://kb.public.example.com", "toolPrefix": "public_kb" }]
  ]
}
```

## Tools

| Tool | WeKnora endpoint | What the model gets |
|---|---|---|
| `weknora_list_knowledge_bases` | `GET /knowledge-bases` | Knowledge base names and ids, to report what exists or to narrow a later search |
| `weknora_search` | `POST /knowledge-search` + `GET /knowledge/search` | Ranked passages verbatim, each with a `knowledge_id`, score and chunk index, plus any document the query names |
| `weknora_read_document` | `GET /chunks/:knowledge_id` + `GET /knowledge/:id` | One document's passages reassembled in order, led by its title and summary, with paging |
| `weknora_ask` | `POST /sessions` + `POST /knowledge-chat/:id` or `POST /agent-chat/:id` | WeKnora's own answer, its citations, the server-side tools it used, and a resumable `session_id` |

`weknora_search` is the workhorse: it returns the source text for the agent to reason over, which keeps the agent's own
reasoning auditable. When `knowledgeBaseIds` is empty the plugin resolves every knowledge base the credential can see
and uses all of them — knowledge bases are frequently named too poorly for the model to choose between.

`weknora_ask` delegates the whole question to WeKnora's RAG pipeline. Reserve it for broad or synthesis questions
spanning many documents — it runs another model server-side, so it is slow, and it returns a conclusion rather than the
evidence behind it. With `agentId` set it sends the question to a WeKnora custom agent (ReAct pipeline) instead.

## Permissions and data flow

The plugin only reads. It never writes to WeKnora: no ingestion, no chunk edits, no deletion. A WeKnora API key scoped
to `retrieve` is enough for three of the four tools; `weknora_ask` additionally needs `chat`, because it creates a
session and streams an answer.

The key is sent as `X-API-Key`. Set `tenantId` as well if you use a platform-scoped key, which needs `X-Tenant-ID` to
select a workspace. Errors are reported with the HTTP status and WeKnora's own reason, never with the key.

Passage content is clipped to `maxChunkChars` before it reaches the model, so one oversized document cannot eat the
context window. The clip is reported (`truncated: true`) instead of silently hiding text.

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
| `resourceUrls` | `public` | `public` returns directly loadable file URLs; `handle` keeps internal `resource://` refs |
| `toolPrefix` | `weknora` | Tool-name prefix, `^[a-z][a-z0-9_]*$` |
| `tools.*` | all `true` | Register fewer tools to spend fewer prompt tokens |

Environment variables: `WEKNORA_BASE_URL`, `WEKNORA_API_KEY`, `WEKNORA_TENANT_ID`, `WEKNORA_KNOWLEDGE_BASE_IDS`,
`WEKNORA_AGENT_ID`, `WEKNORA_RESOURCE_URLS`, `WEKNORA_TOOL_PREFIX`. Plugin options take precedence over the
environment.

## Development

```sh
npm install
npm test          # build + unit and contract tests against the mock backend
```

The API contract is asserted from both sides: `test/contract.test.mjs` pins the exact WeKnora calls this plugin makes,
and `contract/contract_test.go` (run from the repository root: `go test ./packages/opencode-weknora/contract/...`)
checks the WeKnora server still accepts those request bodies and still serves the response fields the plugin reads.

A live integration suite runs against a real deployment when given credentials:

```sh
WEKNORA_LIVE_URL=http://localhost:8080 \
WEKNORA_LIVE_API_KEY=sk-... \
WEKNORA_LIVE_ASK=1 \
  node --test test/live.integration.test.mjs
```

## License

MIT — as is [`dsh-weknora`](https://github.com/Tencent/WeKnora/tree/main/packages/dsh-weknora), whose client and
tool design this plugin ports.
