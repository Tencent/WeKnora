# Wiki document-map artifact cache

## Boundary

Wiki ingestion is split into a reusable per-document map and a knowledge-base-state-dependent materialization/reduce phase.

The cached map is a pure function of the active source document, synthesis model, prompts, language, and Wiki configuration. It contains candidate entity/concept contributions, the document summary draft, chunk classifications, stable chunk citations, and the document's canonical page contribution set.

The cache never stores the current page corpus, current contributors, previous summary content, retract operations, taxonomy placement, directory state, or reduced page bodies. On every run—including a cache hit—the pipeline still performs cross-document page deduplication and slug remapping, computes the current active contribution set, creates reparse/stale retract operations, and runs the existing reduce/finalize lanes.

```text
document + model + prompts + config -> canonical document map (cached)
canonical document map + live KB state -> reconcile -> reduce -> finalize
```

## Source chunk boundary

Only enabled, non-empty ingestion text chunks (`text`, including the historical empty-type representation) participate. Summary, question, FAQ, graph/entity/relationship, Wiki page, and other derived chunks are excluded. Soft-deleted rows are excluded by the repository's normal GORM scope.

Every cacheable source chunk must have:

- a non-empty database row ID for the current run;
- a non-empty `StableIdentity`;
- `IdentityVersion == contentkey.ChunkIdentityVersion`;
- a stable identity unique within the active document.

Legacy, duplicate, or unsupported identities make the document ineligible for cache reuse. In that case Wiki map is computed normally without reading or writing a reusable artifact.

## Artifact key

The tenant-scoped content-addressed key includes:

- tenant ID and knowledge ID;
- document title;
- enriched document content, including canonical OCR/caption enrichment;
- ordered source chunk content, type, position, `StableIdentity`, and `IdentityVersion`;
- synthesis model ID/name and allowlisted output-affecting model settings;
- language, extraction granularity, content instructions, and extraction instructions;
- content truncation limit;
- prompt, producer, and artifact schema versions.

Transport and credential fields such as API keys, base URLs, and custom headers do not affect identity.

## Payload and stable citation rebinding

The JSON payload stores citations as `(StableIdentity, IdentityVersion)`, never as a database `Chunk.ID`. `extractedItem.SourceChunks` is cleared before serialization so a row ID cannot leak through the nested item.

On every hit, the worker loads the current active source chunks and builds a one-to-one stable-identity-to-row-ID map. All cached citations are rebound to current `Chunk.ID` values before creating `SlugUpdate` values. Reduce therefore receives only live row IDs and can resolve the cited content through the existing chunk repository.

A hit is rejected and recomputed safely when:

- an identity is missing from the current active set;
- an identity occurs more than once;
- the identity version is absent or unsupported;
- the payload/schema/encoding is malformed.

Rejected artifacts are not returned to reduce. Because the derived-artifact repository deliberately has no destructive invalidation API, a corrupt succeeded row is bypassed for that request rather than overwritten in place.

## Observation

The document-level Wiki span keeps its existing lifecycle: map opens it and the batch driver closes it only after live-state reconciliation and page reduce have completed. Cache hits create the same span shape as misses.

For an eligible lookup, map output reports `artifact_kind=wiki.document-map`, `cache_status`, `computed_items`, `reused_items`, and the pure-map `request_count`. A miss counts only candidate extraction, document summary, and chunk citation/classification provider calls; claim, polling, heartbeat, persistence, live-state dedup/remap, and reduce/finalize calls are excluded. A hit reports `request_count=0`, `computed_items=0`, and `reused_items=1`. Model calls made later by stateful dedup or reduce retain their own existing operation observations.

When the repository is not configured, or legacy/duplicate/unsupported stable identities make caching unsafe, the map reports `cache_status=not_supported` rather than a false hit or miss. Cache infrastructure/corruption fallback reports `cache_status=error` without logging document content, prompts, payloads, or credentials.

## Invalidation matrix

| Change | Result |
| --- | --- |
| Database `Chunk.ID` changes, stable identity/content unchanged | Hit; citations rebind to the new row ID |
| Source text/OCR/caption changes | Miss |
| Stable identity or identity version changes | Miss |
| Source chunk order/type/position changes | Miss |
| Derived summary/question/GraphRAG chunk changes | No invalidation |
| Synthesis model/model revision changes | Miss |
| Prompt or producer version changes | Miss |
| Language/granularity/custom Wiki instructions change | Miss |
| Tenant changes | Isolated keyspace; miss |
| Current Wiki pages/contributors change | Artifact may hit; live reconcile/reduce still runs |

## Concurrency and failure behavior

Artifacts use the shared persistent claim state machine. One worker claims a miss, renews its lease while Chat calls execute, and completes or fails the row. Concurrent workers observe `busy`, poll within a bounded wait, and then consume the succeeded artifact. Expired leases may be taken over by the repository. A worker that loses ownership cannot complete over the takeover result.

Provider failures and cancellation mark the claimed artifact failed using a short detached cleanup context when necessary. A later request may claim the failed artifact and recompute it. Busy waits and lease timings have production defaults and injectable test timings.

Once a claim is owned, ordinary provider, stable-reference conversion, encoding, lease-renewal, and completion failures make a best-effort transition to `failed`. Cancellation uses a bounded `context.WithoutCancel` cleanup. `ErrArtifactLostOwnership` is different: the old worker does not call `Fail` or `Complete` against the new owner and returns the ownership error instead.

## Delete and retract behavior

Knowledge deletion checks run before map work. Cached artifacts do not make a deleted document live again. Additions are filtered again immediately before reduce. Retract operations are never stored in the artifact; they are reconstructed from the authoritative current page/source-reference state, so deletion, reparse, page recycling, contributor calculation, references, and directory/finalize convergence retain their existing semantics.
