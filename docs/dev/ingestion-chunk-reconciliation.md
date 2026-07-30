# Ingestion chunk reconciliation

This document describes the PR3 ingestion behavior for document `text` and
`parent_text` chunks.

## Scope

Reconciliation owns only these ingestion-managed chunk types:

- `text`
- `parent_text`

It does not reconcile derived artifacts such as image OCR/caption, summary,
FAQ, graph entities/relationships, wiki pages, table summaries, generated
questions, or clone/move-specific rows.

## Identity model

`Chunk.StableIdentity` identifies the same logical ingestion chunk between
parses. It is a matching key, not a database primary key and not an embedding
cache key.

For each rebuild, the planner classifies active rows as:

- `matched`: compatible stable identity; retain the existing `Chunk.ID`;
- `added`: no active match; retain the desired row's new random `Chunk.ID`;
- `removed`: an active identified row no longer appears; soft-delete it only
  after the replacement index write succeeds;
- `legacy`: an active ingestion row has no stable identity; do not guess a
  match, and soft-delete it at the successful reconciliation commit;
- invalid duplicate: more than one active or desired row owns the same scoped
  identity; fail explicitly.

Matching requires tenant, knowledge, chunk type, identity version, and stable
identity compatibility. Position, content text, offsets, `SeqID`, and array
order are not fallback identities.

After final IDs are bound, `ParentChunkID`, `PreChunkID`, `NextChunkID`, and
`ParsedChunk.ChunkID` are rewritten to the retained or newly allocated row IDs.

## Commit order and failure behavior

The main flow is:

1. build desired chunks and stable identities;
2. load active ingestion-managed rows;
3. plan and bind final row IDs;
4. execute Embedding and write the replacement vector records;
5. transactionally apply matched updates, inserts, and precise soft deletes;
6. delete obsolete vector IDs; enqueue the existing `index:delete` retry task
   if the immediate deletion fails;
7. reset the knowledge graph namespace and continue post-processing.

The database transaction validates both the active-row snapshot and the latest
processing attempt. A superseded worker cannot commit after a newer attempt.
Before the transaction succeeds, existing active chunk rows remain available.
Compensation after a failed vector write or failed reconciliation commit is
limited to newly added chunk IDs; retained IDs are never bulk-deleted.

## Vector-store BatchSave contract

For every formally supported vector backend, repeating a successful
`BatchSave` for the same `SourceID` is an idempotent replacement:

- exactly one current logical record is visible;
- its content, metadata, enabled state, and vector are from the last successful
  write;
- a failed replacement must not be reported as successful;
- repeated writes must not accumulate searchable duplicates.

Implementation audit:

| Backend | Replacement mechanism |
| --- | --- |
| PostgreSQL | transactionally delete the logical keys, then insert the batch |
| SQLite | one transaction replaces metadata, FTS5 rows, and vec rows |
| Elasticsearch 7/8 | bulk/index operation with stable `SourceID` document ID |
| OpenSearch | bulk index with stable `SourceID` document ID |
| Qdrant | UUIDv5 point ID derived from `SourceID`, then cleanup of historical random IDs |
| Milvus | UUIDv5 primary ID derived from `SourceID`, then cleanup of historical random IDs |
| Weaviate | existing UUID `SourceID` is preserved; non-UUID source identities receive a deterministic UUIDv5 |
| Tencent VectorDB | `SourceID` is the upsert document primary ID |
| Doris | `SourceID` is the stable row key and replacement write key |

Qdrant and Milvus historically allocated a random primary point ID during each
batch write. On a successful new upsert, the adapter deletes other physical
points with the same `source_id` while excluding the new stable
ID. This lazily repairs touched historical data without requiring a global
migration or deleting the just-written point.

## No artifact cache

PR3 introduces no parser, VLM, embedding, wiki, graph, Redis, or derived
artifact cache. Stable-identity matches still traverse the normal Embedding
path. `computed_items`, request counts, and batch counts continue to reflect
the real model calls; `reused_items` does not increase merely because a chunk
row ID was retained.

Vector-store idempotency is only a persistence guarantee for repeated writes.
It must never be used as a reason to skip model computation.

## Tests

The test suite covers:

- pure deterministic planning, duplicate rejection, and input immutability;
- final ID/reference binding;
- repository snapshot/attempt fencing and transactional rollback;
- the processChunks reconciliation segment from desired-row construction
  through the repository transaction;
- repeated embedding observation proving unchanged inputs are recomputed;
- stable Qdrant/Milvus physical IDs and legacy-point cleanup filters;
- a cross-backend architecture guard covering all formally supported stores.
