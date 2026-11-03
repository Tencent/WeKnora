# Model context boundary

`modelcontext.Registry` is the only application-facing boundary for values
that are shortened before an LLM call and restored afterwards. The whole
codec lives in this one package:

| File | Role |
| --- | --- |
| `registry.go` | `Registry` facade — the only type request lifecycles use |
| `handle_table.go` | `handleTable[M]`, the generic bidirectional primitive behind every handle space |
| `sources.go` | source handles (`chunk://N`/`doc://N`/`kb://N`/`url://N`) and the tool-argument codec |
| `source_text.go` | handle syntax, `weknora://` durable URIs, and free-text / streaming handle rewriting |
| `citations.go` | protocol prompts, `<ref/>` → `<kb/>`/`<web/>` expansion, citation stream expander |
| `model_output.go` | compact source-centric renderings of tool results |
| `resources.go` | durable-resource handles (`res://NNNN`) |
| `stream.go` | `streamHold` suffix-hold primitive and every streaming decoder |
| `tool_policy.go` | the single policy layer: per-tool key contracts + `sourceKeySpaces` dispatch |
| `mcp.go` | MCP bridge routing handles (`msN`/`mtN`), definition projection and envelope codec |
| `mcp_sources.go` | bounded citation sidecar for HTTP(S) links in successful MCP results |
| `handles.go` | exported `HandleTable` for invocation-local spaces (`iN`, `ref-N`, `c000`) |
| `leaks.go` | `LeakedIdentifiers` report of raw UUIDs that survive `EncodeMessages`, logged at every model-call site |

## Identity rules

- Durable application identities: UUIDs, Wiki slugs, URLs, and
  `resource://...` references.
- Request-local model handles: `chunk://N`, `doc://N`, `kb://N`, `url://N`,
  `msN`, `mtN`, and `res://NNNN`. The legacy bare `cN`/`dN`/`bN`/`wN` form is
  still accepted, but only as the whole value of a declared ID field.
- Durable text forms of source handles: `weknora://kb/<id>`,
  `weknora://doc/<id>`, `weknora://chunk/<id>`; a web page becomes its URL.
- Tool-private request handles: `iN` for Wiki issues.
- Ingest-call handles: `cNNN` and `ref-N`, allocated with `HandleTable`.
- Temporary handles are decoded before tool execution, persistence, or UI
  delivery. They are never accepted as durable authorization or routing data.

## Request lifecycle

1. Create one `Registry` for the complete request or Agent execution.
2. Register structured source identities as they enter the context.
   For Agent tools, use `EncodeTools` to project MCP routing enums and source summaries.
3. Call `EncodeMessages` immediately before the model call.
4. Decode tool calls through `DecodeToolCalls` before parsing or execution.
5. Decode complete responses with `DecodeResponse`, or streaming text with one
   `StreamDecoder` per response channel.

Unknown handle-shaped values in declared tool fields are rejected before tool
execution. The same per-tool policy is applied when historical assistant tool
calls and tool results are replayed, so handles neither disappear nor drift to
new numbers across Agent rounds.

The registry owns codec ordering. Resource references are encoded before
source IDs so a Wiki slug such as `summary/<knowledge-id>` cannot be corrupted
into `summary/doc://1`.

Source addressability is separate from citation eligibility. Use
`RegisterContextChunk` for bound-KB directory entries. Replayed citations,
legacy tool history, and tool arguments also register navigation handles only.
`ModelToolResultForTool` and explicitly supplied retrieval results authorize
citations for the current execution; rereading a historical source promotes the
same handle without renumbering it. Both complete and streaming decoders drop
references that have not been backed by current evidence.

## Observability

Every model-call site runs `LeakedIdentifiers` on the encoded messages and
logs a warning naming the role or tool whose text still carries a raw UUID.
A *registered* leak means the codec missed a rewrite site; an *unregistered*
leak means a producer emitted an identifier that never entered the registry.
Text-level compaction (`CompactKnownText`) stays in place as defense in depth
until those producers are fixed and the report stays quiet.

Langfuse generation observations contain the exact encoded payload sent to and
returned by the model. Agent tool spans contain both `model_arguments` (the
model-emitted handles) and `resolved_arguments` (the durable values actually
executed), plus `argument_resolution` and any unresolved handles. Sensitive
tools report only argument keys and resolution counts.

Langfuse is an observer of this boundary; it must not implement another handle
mapping.

## Built-in tool policies

Canonical ID-bearing arguments for knowledge, graph, data, web, and Wiki tools
are decoded centrally through a tool-name plus JSON-field allowlist. The
`sourceKeySpaces` table is the single source of truth for which keys are
ID-bearing and which handle space they register into; per-tool `sourceIDKeys`
contracts gate which of those keys each tool may use.
`database_query.sql` has an explicit policy: handles anywhere in the SQL text
decode to bare durable IDs so the model can filter the real
`knowledges`/`chunks` tables. `data_analysis.sql` carries no handles: the
selected document is always exposed as the fixed table `dataset`, so only
`knowledge_id` is decoded. Wiki issue IDs use the same lifecycle via an `iN`
handle space.

## Handles in free text

Models copy handles into prose they write: automation instructions, tasks
delegated to other agents, Markdown links, reasoning and final answers. Because
the scheme-shaped syntax cannot collide with ordinary words, the registry
rewrites handles in every string, not just declared fields:

- Tool arguments (built-in and MCP): declared ID fields receive bare durable
  IDs, SQL fields receive bare IDs, and every other string receives the durable
  text form (`weknora://kb/<id>`, `weknora://doc/<id>`, `weknora://chunk/<id>`,
  or the page URL). An unknown handle anywhere in the arguments fails closed.
- Model output (complete and streaming, content and reasoning): `<ref/>`
  citations expand as before; bare `kb://N`/`doc://N` become `weknora://` URIs,
  `url://N` becomes the URL, and `chunk://N` becomes a `<kb/>` citation when it
  is current evidence, otherwise its document URI. Unknown handles are dropped.
- Input: `weknora://` URIs in any message, reasoning or replayed tool call are
  registered as navigation-only handles and shown to the model as handles, so
  a saved instruction round-trips across requests and agents.

Handles and URIs are never authorization: every tool still authorizes the
durable ID it receives.

Dynamic MCP tools do not get per-field ID contracts. Their schemas and
identity semantics are controlled by the MCP server, so the application does
not guess that an arbitrary `id`, `knowledge_id`, or `url` field is durable;
values that are not scheme-shaped handles pass through untouched. Handles the
model copied into MCP arguments get the free-text rewrite above, and durable
resource handles still use the normal `res://NNNN` codec. A future MCP ID mapping must be an
explicit server/tool annotation and should plug into this registry rather than
create a parallel mapper.

Successful MCP execution results additionally receive an
`external_source_candidates` sidecar with up to 50 distinct observed HTTP(S)
links mapped to `url://N`. The external payload is not rewritten, discovery schemas
are not scanned, and article IDs are never used to guess URLs. A candidate link
does not establish that its page was read: the model must select the link whose
associated result supports the claim. When no handle is supplied, the citation
protocol permits an exact supplied source URL as a Markdown link, never a
borrowed knowledge-base citation.

The application-owned MCP bridge is an explicit exception for routing fields:
`discover_mcp_tools.server_id` uses `msN`, and `call_mcp_tool.tool_ref` uses
`mtN`. Tool definition enums, service summaries, discovery results and replayed
calls share the same request registry. Only directory envelope fields are
registered; `input_schema` and external execution results remain opaque, and
`call_mcp_tool.arguments` only gets the free-text handle rewrite. Arguments are restored before authorization, validation,
execution, persistence and UI events. Unknown routing handles fail closed.

For model-emitted `call_mcp_tool` calls, one extra JSON-string encoding of the
`arguments` envelope is accepted only when it contains a complete JSON object
(including `{}`). Normalization runs before resource-handle decoding; original
model arguments are retained for tracing. Nested business strings are unchanged,
and malformed JSON, arrays, `null`, and further encoding layers remain invalid.
The canonical object still passes the existing routing, schema and approval checks.

## Wiki routing

`WikiRouteResolver` is intentionally separate. It stores server-side
slug-to-knowledge-base provenance gathered during Wiki search/read operations,
and can only return knowledge bases already present in the Agent's authorized
Wiki scope. It is routing state, not a model handle table. Reads scan every
legal Wiki KB so cached provenance cannot hide a duplicate slug; mutations
require one unique owner. New pages use link provenance, a single Wiki scope,
or a single source-document owner and reject ambiguous routing instead of
asking the model for a durable KB ID.

Document/tag-constrained Wiki and graph requests are filtered by the same
server-owned `SearchTargets` provenance. Uncited/global Wiki pages and graph
results outside that subset fail closed; only an explicit whole-KB target
authorizes whole-KB content.
