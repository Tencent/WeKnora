# Model context boundary

`modelcontext.Registry` is the only application-facing boundary for values
that are shortened before an LLM call and restored afterwards. The whole
codec lives in this one package:

| File | Role |
| --- | --- |
| `registry.go` | `Registry` facade — the only type request lifecycles use |
| `handle_table.go` | `handleTable[M]`, the generic bidirectional primitive behind every handle space |
| `sources.go` | source handles (`cN`/`dN`/`bN`/`wN`) and the tool-argument codec |
| `citations.go` | protocol prompts, `<ref/>` → `<kb/>`/`<web/>` expansion, citation stream expander |
| `model_output.go` | compact source-centric renderings of tool results |
| `resources.go` | durable-resource handles (`res://NNNN`) |
| `stream.go` | `streamHold` suffix-hold primitive and every streaming decoder |
| `tool_policy.go` | the single policy layer: per-tool key contracts + `sourceKeySpaces` dispatch |
| `handles.go` | exported `HandleTable` for invocation-local spaces (`iN`, `ref-N`, `c000`) |

## Identity rules

- Durable application identities: UUIDs, Wiki slugs, URLs, and
  `resource://...` references.
- Request-local model handles: `cN`, `dN`, `bN`, `wN`, and `res://NNNN`.
- Tool-private request handles: `iN` for Wiki issues.
- Ingest-call handles: `cNNN` and `ref-N`, allocated with `HandleTable`.
- Temporary handles are decoded before tool execution, persistence, or UI
  delivery. They are never accepted as durable authorization or routing data.

## Request lifecycle

1. Create one `Registry` for the complete request or Agent execution.
2. Register structured source identities as they enter the context.
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
into `summary/d1`.

## Observability

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
`database_query.sql` and `data_analysis.sql` have an explicit policy because
source handles can be embedded inside quoted SQL values; unquoted SQL aliases
and arbitrary prose are never rewritten. Wiki issue IDs use the same lifecycle
via an `iN` handle space.

Dynamic MCP tools are intentionally opaque in both arguments and results.
Their schemas and identity semantics are controlled by the MCP server, so the
application must not guess that an arbitrary `id`, `knowledge_id`, or `url`
field is durable or rewrite it. Durable resource handles inside MCP arguments
still use the normal `res://NNNN` codec. A future MCP ID mapping must be an
explicit server/tool annotation and should plug into this registry rather than
create a parallel mapper.

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
