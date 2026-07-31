# Ingestion cache end-to-end acceptance

PR9 is the integration and regression gate for the ingestion work delivered in
PR1 through PR8. It introduces no new cache kind. Its purpose is to prove that
stable chunk identity, chunk reconciliation, persistent artifacts, provider
observation, and live business materialization compose safely.

## Composition

The production composition under test is:

```text
document parse
  -> stable text chunk identities
  -> reconcile desired and active chunk rows
  -> content-addressed embedding artifacts
  -> vector materialization
  -> image OCR and caption artifacts
  -> multimodal chunk materialization
  -> per-document Wiki map artifact
  -> live Wiki reduce/contributor reconciliation
  -> per-chunk graph extraction artifact
  -> live AddGraph materialization
```

All artifact users share the production `DerivedArtifactRepository` and its
tenant-scoped lease state machine. The production container provides that
repository to model construction, image multimodal processing, Wiki ingestion,
and graph extraction. Lite mode uses the same repository contract over SQLite;
the normal server uses the versioned PostgreSQL schema.

## Cache boundary

| Layer | Artifact kind | Reusable pure computation |
| --- | --- | --- |
| VLM OCR | `multimodal.ocr` | canonical OCR of exact image bytes |
| VLM caption | `multimodal.caption` | canonical caption of exact image bytes |
| Embedding | `embedding.vector` | float32 vector for exact input text |
| Wiki map | `wiki.document-map` | frozen per-document map contribution |
| Graph extraction | `graph.chunk-extraction` | candidate graph for exact chunk text |

The following state is deliberately not cached and must execute on every
applicable attempt, including an artifact hit:

- vector-store writes and current source IDs;
- Wiki reduce, contributor reconciliation, retract/retractStale, and finalize;
- Graph `AddGraph` and current `node.Chunks` binding;
- chunk-row reconciliation and current row IDs;
- delete, cancellation, supersede, pending-task, parse-status, and span state.

The governing invariant is therefore:

```text
pure computation hit + current business materialization
```

An artifact is never proof that a knowledge, chunk, Wiki page, graph, or vector
is currently live.

## Executable acceptance tests

The cross-layer fixtures use the production key builders, cache wrappers,
repository implementation, Wiki map/reduce path, graph Handle/AddGraph path,
embedding BatchIndex path, stable identities, and observation wrappers. They do
not implement a parallel fake cache.

### Cold run, restart, row-ID rebinding, and lease takeover

`TestIngestionCachesEndToEndAcrossRestartInvalidationAndCrashRecovery` uses one
durable SQLite artifact table for OCR, caption, embedding, Wiki map, and graph
extraction. It proves:

- all five kinds compute on a cold run and coexist in one table;
- reconstructed services over the same database hit without provider calls;
- a Wiki map hit rebinds a stable chunk identity to the rebuilt `Chunk.ID`;
- a graph-only prompt change leaves the other four kinds reusable;
- an expired graph `computing` row is taken over and its attempt count advances.

### Repeated rebuild cost

`TestIngestionArtifactReuse_ThreeIdenticalRebuildsDoNotIncreaseProviderCalls`
runs three source chunks through embedding and graph extraction three times.
Only run 1 calls providers. Runs 2 and 3 return hits with zero additional graph
requests and zero additional embedding input items.

The production-path materialization tests provide the complementary proof that
reuse does not suppress writes:

- `TestImageMultimodalArtifactCache_RebuildReusesOCRAndCaption` recreates OCR
  and caption business chunks while VLM counts remain fixed;
- `TestEmbeddingArtifactProductionBatchIndexReusesAndPartiallyRecomputes`
  writes rebuilt source IDs from cached vectors;
- `TestWikiMapArtifactSecondRunSkipsPureMapChatAndStillReduces` executes reduce
  from a map hit;
- `TestChunkExtractHandleCacheHitMaterializesCurrentChunkIDAndSpanObservation`
  executes `AddGraph` and binds the current chunk ID from an extraction hit.

### One changed chunk

`TestIngestionArtifactReuse_OneChunkChanged` starts with A/B/C, changes only B,
and asserts actual provider deltas:

| Layer | A | B changed | C | Provider delta |
| --- | --- | --- | --- | --- |
| Embedding | hit | miss | hit | one input item |
| Graph extraction | hit | miss | hit | one chat request |

The production embedding BatchIndex test also verifies partial reuse while
materializing new source IDs. Wiki map remains per-document by design, so a
change to a consumed source chunk invalidates that document's map artifact.

### Exact invalidation

Layer tests enforce the complete key dimensions:

- embedding: exact text, tenant, model ID/revision, dimensions, provider and
  output-affecting configuration;
- OCR/caption: exact image bytes, tenant, VLM model, operation-specific prompt,
  prompt version, and producer version;
- Wiki map: frozen consumed source chunks, title/map inputs, tenant, model,
  prompt and map configuration;
- graph: exact chunk content, tenant, model/revision, structured prompt and
  output-affecting model configuration.

Unrelated settings are intentionally absent from each key. The combined tenant
test `TestIngestionArtifactInvalidation_TenantIsolation` computes all five kinds
for two tenants and verifies that both tenants receive independent rows and
provider work; tenant B cannot reuse tenant A.

### Materialization failure and retry

Succeeded computation remains reusable when a later business write fails:

| Test | First attempt | Retry |
| --- | --- | --- |
| `TestIngestionArtifactRecovery_OCRAndCaptionSucceededDownstreamFailed` | OCR and caption succeed, multimodal chunk creation fails | both artifacts hit, chunk creation reruns |
| `TestIngestionArtifactRecovery_WikiMapSucceededReduceFailed` | map succeeds, Wiki reduce fails | map hit, reduce reruns and creates the page |
| `TestIngestionArtifactRecovery_GraphExtractSucceededAddGraphFailed` | extraction succeeds, `AddGraph` fails | extraction hit, `AddGraph` reruns with current chunk ID |
| `TestIngestionArtifactRecovery_EmbeddingSucceededVectorWriteFailed` | embedding succeeds, vector write fails | embedding hit, vector write reruns |

Provider failures, cancellation, completion failures, corrupt payloads,
heartbeat renewal, lost ownership, busy waiting, partial embedding completion,
and expired takeovers remain covered by the layer-specific artifact tests.
These tests verify that a claimed row becomes failed or takeover-eligible and
that an old owner cannot overwrite a takeover result.

### Delete, supersede, and concurrency

- `TestWikiMapArtifactHitCannotResurrectDeletedKnowledge` proves a hit cannot
  publish additions for deleting knowledge.
- `TestApplyIngestionChunkReconcile_SQLite_RejectsSupersededAttempt` proves an
  old attempt cannot commit its chunk-row diff.
- `processChunks` checks deleting/cancelled and superseded attempts before
  indexing, after indexing, and before reconciliation commit, compensating
  newly added vector IDs when necessary.
- repository and layer concurrency tests prove a single lease owner, bounded
  busy waits, and one provider computation for identical keys.
- `ReparseKnowledge` preserves active chunks/vectors/graph until replacement
  parsing succeeds; it no longer performs unconditional pre-reparse deletion.

Artifact retention across deletion is allowed. Publishing retained artifacts
as current business state is not.

### Observation

The hit contract remains:

| Operation | `cache_status` | `request_count` | `computed_items` | `reused_items` |
| --- | --- | ---: | ---: | ---: |
| OCR | `hit` | 0 | 0 | 1 |
| Caption | `hit` | 0 | 0 | 1 |
| Embedding batch | `hit` | 0 | 0 | number of reused inputs |
| Wiki map | `hit` | 0 | 0 | 1 |
| Graph chunk | `hit` | 0 | 0 | 1 |

Claim, poll, heartbeat, complete, fail, Wiki reduce, `AddGraph`, and vector
write operations do not increment provider request counts. Repository errors,
corrupt artifacts, provider failures, cancellation, and not-supported paths
retain the PR1 operation/request/item semantics and close their spans.

### Payload safety

`TestIngestionArtifactPayloads_ContainNoSecretsOrRowIDs` creates all five kinds
and verifies:

- every payload digest matches its bytes;
- OCR, caption, Wiki, and graph payloads use JSON encoding;
- embedding uses the expected binary encoding;
- payloads contain no API-key/header/token, attempt, trace, or operation
  markers;
- Wiki and graph artifacts contain no database chunk row ID.

Embedding payloads contain only the binary vector envelope. Wiki stores stable
chunk references and rebinds them at materialization time. Graph stores graph
candidates without `node.Chunks` and binds current IDs in `Handle`.

## Stable identity and disabled chunks

Stable identity is a business identity, not an artifact key. Reconciliation
preserves matched row IDs, creates only new identities, soft-deletes removed
identities, and rejects a superseded attempt at commit.

`Chunk.IsEnabled` has a database/GORM default of true. GORM applies that default
to a false Go bool during create even with `Select("*")`. All production
`CreateChunks` callers were audited: new ingestion, summary, multimodal, and FAQ
paths explicitly set true; clone/move explicitly copy source state; FAQ APIs
explicitly pass the requested state. `CreateChunks` therefore records explicit
false inputs and restores them inside the create transaction after hooks have
assigned IDs. Tests cover explicit false, true, mixed batches, and rollback on
insert failure.

Legacy chunks without a supported stable identity safely bypass Wiki map cache.
No data backfill is required for startup.

## Schema and injection

The released schemas include:

- SQLite `000001` stable identity and `000002` derived artifacts;
- PostgreSQL versioned migrations `000073` and `000074`;
- MySQL bootstrap columns, table, tenant/key uniqueness, status check, and
  status/lease indexes;
- ParadeDB bootstrap stable identity columns and index.

`derived_artifacts` is unique by `(tenant_id, artifact_key)`. Chunk stable
identity indexes include tenant and knowledge scope and coexist with soft
delete, allowing a rebuilt active row after an old row is soft-deleted.

The production container provides `NewDerivedArtifactRepository` and injects it
into model, image multimodal, Wiki ingest, and graph extract construction.
Legacy direct test construction may use documented not-supported fallbacks;
production constructors that require the repository reject nil.

## Verification

Windows development with the local TDM-GCC toolchain may require:

```powershell
$env:CGO_LDFLAGS='-lssp'
```

PR9 verification commands are:

```powershell
go test ./internal/contentkey/... -count=1
go test ./internal/artifactkey/... -count=1
go test ./internal/testutil/modelcount/... -count=1
go test ./internal/application/repository -run "Chunk|DerivedArtifact" -count=1
go test ./internal/application/service -run "StableIdentity|Reconcile|Artifact|Multimodal|Embedding|WikiMap|GraphExtract" -count=1
go test ./internal/application/service -run "IngestionCache|IngestionArtifact" -count=1
go test ./internal/application/service/... -count=1
```

Linux CI must additionally run the race-enabled cache suite and the repository
wide test suite:

```bash
go test -race ./internal/application/service \
  -run 'IngestionCache|IngestionArtifact|WikiMap|GraphExtract|EmbeddingArtifact|Multimodal' \
  -count=1
go test ./... -count=1
```

Linux/race results are an external CI verification item when development is
performed only on Windows; they must not be inferred from Windows results.

## Deliberately unavoidable work and later options

PR9 does not avoid provider work for genuinely new or changed content. It also
does not cache Wiki reduce, graph materialization, or vector writes because
those operations depend on current business state.

Possible later work, outside PR9, includes parser/render artifacts, rendered
image CAS, and artifact retention/GC.
