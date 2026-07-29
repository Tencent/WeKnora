# Ingestion operation observability

This document defines the operation-level observability contract for expensive
work performed by the knowledge ingestion pipeline. It is a baseline for later
reusable-computation and cache work; it does not implement or imply a cache.

## Goals

The processing-span tree already describes coarse pipeline stages such as
document parsing, chunking, embedding, and post-processing. A stage can contain
multiple model or parser operations, so stage timing alone cannot answer how
many provider calls or input items a rebuild performed.

Operation observations add non-sensitive counters around the existing
DocReader, Chat, VLM, and Embedder interfaces without changing their requests,
responses, retry behavior, batching, or error propagation. They make the
pre-cache cost of a repeated ingestion measurable before any cache is added.

## Operation taxonomy

`types.IngestionOperation` is the canonical taxonomy. Operation names are
stable internal identifiers and use a dotted namespace.

| Namespace | Operations | Meaning |
|---|---|---|
| Parser | `parse.document` | One document-reader operation. |
| Multimodal | `multimodal.ocr`, `multimodal.caption` | OCR and caption VLM operations for an image. |
| Embedding | `embedding.batch`, `embedding.chunk`, `embedding.summary`, `embedding.question`, `embedding.faq`, `embedding.graph_entity`, `embedding.graph_relation`, `embedding.wiki_page` | Embedding work classified by its business input. |
| Post-process | `postprocess.summary`, `postprocess.question` | Chat operations that generate summaries or questions. |
| Wiki | `wiki.extract`, `wiki.summary`, `wiki.classify`, `wiki.reduce`, `wiki.deduplicate`, `wiki.index_intro` | Chat operations in the wiki ingestion pipeline. |
| Graph | `graph.extract_chunk` | Graph extraction for a source chunk. |

An operation is more specific than a processing stage. Multiple operations may
belong to one stage, and the same operation may issue multiple provider
requests because of application-visible batching.

## Counter semantics

The following counters intentionally describe different quantities:

- `operation_count` is the number of logical business operations represented
  by the observation. A normal operation observation reports one even when it
  fails before reaching a provider.
- `request_count` is the number of calls that enter a WeKnora provider
  interface or adapter. For embedding, each observed `Embed` or `BatchEmbed`
  provider call counts once. Pool orchestration is not an additional request.
- `batch_count` is the number of observed provider batches. It currently
  matches embedding request count and is kept separate so later providers can
  expose a different batching model without redefining `request_count`.
- `total_items` is the number of operation input items. For embedding it is the
  number of texts; for VLM it is the number of images; for a chat request the
  request is one logical item even when it contains multiple messages.
- `computed_items` is the number of input items whose operation completed
  successfully in the current execution.
- `reused_items` is reserved for later reusable-computation work and is zero in
  this baseline.

Retries hidden inside a provider SDK, HTTP client, gateway, or remote service
are not visible through WeKnora's interfaces and therefore are not included in
`request_count`. A retry that re-enters an observed WeKnora interface is a new
request and is counted.

Zero counts are meaningful and are emitted explicitly. For example, an image
read failure may have `operation_count=1`, `request_count=0`, and
`total_items=0`.

## Cache status

Production observations in this phase report:

```text
cache_status=not_supported
```

This means the operation has no cache lookup or reusable-artifact decision. It
does not mean cache miss. `hit`, `miss`, and `error` are reserved values and
must not be emitted until a real cache or artifact lookup exists.

Adding observability must not reduce provider calls. Repeating the same
ingestion is expected to repeat parser, VLM, chat, and embedding computation;
the counting test doubles preserve this pre-cache baseline for later PRs.

## Observation sinks

When an operation belongs to a knowledge-processing attempt, its output is
stored in the existing processing span. Failure output is retained through
`FailSpanWithOutput`, so calls performed before an error remain observable.

Some ingestion paths, including current FAQ operations and selected wiki
helpers, do not own a compatible processing span. They emit the same structured
fields through the structured logger and set:

```text
observation_sink=structured_log
```

The logging fallback does not introduce a parallel span lifecycle.

## Provider wrappers and test doubles

Production wrappers delegate to the selected provider unchanged and record
only aggregate metadata:

- `ingestionObservedDocReader` observes document parsing.
- `ingestionObservedChat` observes chat requests for one classified operation.
- `ingestionObservedEmbedder` observes actual embedding interface calls,
  including sub-batches produced by the configured pooler.
- multimodal processing classifies OCR and caption calls in their contexts and
  records their request counts in the image-processing span.

`internal/testutil/modelcount` provides thread-safe `CountingChat`,
`CountingVLM`, and `CountingEmbedder` implementations. They return configured
or deterministic results without contacting external providers and expose
immutable snapshots for baseline assertions.

## Privacy and cardinality

Observations retain counts, byte/character sizes, operation identifiers, model
metadata, and existing chunk/image indexes where applicable. Provider prompts,
document text, image bytes, and model response bodies are not retained by the
counting wrappers.

Formal cache keys and complete content digests are deliberately absent.
`artifact_kind`, digest-prefix fields, and artifact schema version are reserved
for later work and remain unset in this phase.

## Non-goals

This observability baseline does not implement:

- parser, VLM, chat, embedding, wiki, or graph caches;
- cache-key or artifact-key generation;
- provider-call suppression;
- chunk identity, chunk diff, or reconcile;
- changes to batching, retry, task enqueueing, or processing-stage ownership;
- new user-visible APIs.

Later cache work must compare its provider request counts against this baseline
and explicitly change `cache_status` only where a real lookup is performed.
