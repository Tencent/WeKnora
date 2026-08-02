# Versioned Artifact DAG and Knowledge Reconciliation

This document describes the implementation added for
[issue #1679](https://github.com/Tencent/WeKnora/issues/1679). The goal is to
reuse expensive deterministic processing outputs without allowing cached data
to own mutable knowledge, chunk, or attempt state.

## Invariants

- Artifact identity is tenant scoped and immutable.
- An artifact key contains the exact direct-input digests, processor identity,
  rendered request, effective options, canonicalizer version, and output schema
  version.
- Downstream keys depend on upstream `output_digest` values, not only on the
  upstream input key.
- Stored payloads contain reusable provider output only. Tenant, knowledge,
  chunk, and attempt ownership is rebound when the desired state is built.
- Cache failures are fail-open. Provider failures and invalid provider output
  remain visible to the caller.
- Publication is add/update first and stale deletion last. A stale attempt must
  not publish final knowledge state.

## Data flow

```text
source bytes
  -> DocReader artifact
  -> normalized source chunks
  -> chat/VLM/wiki artifacts
  -> stable desired chunk IDs
  -> embedding and graph artifacts
  -> desired-state diff
  -> add/update/index
  -> attempt fence
  -> conditional knowledge publication + storage accounting
  -> delete stale vector, graph, and chunk state
```

`internal/artifact` owns canonical keys, codecs, payload validation, immutable
freeze semantics, cache lookup, corruption eviction, singleflight, Redis
leases, stable UUIDv5 identities, desired-state diffs, and attempt fences.
Model and service adapters keep provider-specific request and response details
outside the reconciliation layer.

## Persistence

`processing_artifacts` is keyed by:

```text
(tenant_id, stage, key_version, artifact_key)
```

The row records processor and output digests, schema, codec, checksum, size, and
hit metadata. `object_ref` is reserved for a future object-store implementation;
the current implementation stores bounded inline payloads and bypasses
DocReader caching above 16 MiB.

`knowledge_attempt_counters` allocates monotonically increasing attempts
independently of span history. This prevents attempt reuse after spans are
cleaned up.

Migration locations:

- PostgreSQL: `migrations/versioned/000079_processing_artifacts.{up,down}.sql`
- SQLite: `migrations/sqlite/000002_processing_artifacts.{up,down}.sql`
- MySQL bootstrap: `migrations/mysql/00-init-db.sql`
- ParadeDB bootstrap: `migrations/paradedb/00-init-db.sql`

## Concurrency and publication

The runtime suppresses duplicate work in three layers:

1. in-process `singleflight` for an artifact key;
2. a Redis lease for workers in different processes;
3. database `put-if-absent` uniqueness as the final correctness boundary.

Batch embedding selects a deterministic missing artifact as the batch leader.
The leader lease covers the provider batch, while the remaining results are
frozen together. This keeps one provider call for concurrent identical batches
without changing caller output order.

Knowledge processing allocates an attempt before work begins. Final publication
and destructive stale cleanup recheck that attempt. A per-knowledge mutation
lock spans chunk/vector/graph binding through exact stale cleanup (local gate in
Lite mode, ownership-token Redis lease in standard mode), preventing a newer
generation from being deleted between a fence check and an external-store
delete. Knowledge publication and tenant storage accounting share one database
transaction; retries calculate the delta from the already-published row and
therefore cannot double-charge. Graph storage uses per-chunk contributions so
stale chunks can be removed exactly without deleting unchanged contributions.

## Compatibility and rollback

The schema addition is non-destructive to existing knowledge and chunk tables.
Artifact misses rebuild data through the existing providers, so an empty
artifact table is valid.

`artifact_cache` supports independent read/write controls and exact stage
overrides. Missing configuration defaults to read/write enabled.

| Mode | `read_enabled` | `write_enabled` |
| --- | --- | --- |
| Disabled | `false` | `false` |
| Shadow write | `false` | `true` |
| Read fallback | `true` | `true` |
| Read only | `true` | `false` |

Environment overrides use `ARTIFACT_CACHE_READ_ENABLED` and
`ARTIFACT_CACHE_WRITE_ENABLED`. Individual stages can be disabled in
`artifact_cache.stages`; omitted stages remain enabled.

To roll back:

1. disable artifact reads, then writes, and restart or drain workers;
2. deploy the previous application version;
3. verify no process reads or writes artifact/attempt tables;
4. optionally run migration `000079` down.

Dropping the new tables discards only reusable artifacts and attempt counters;
it does not delete knowledge, chunks, vectors, or graph data. Do not run the
down migration while the new worker version is active.

## Observability and log safety

The runtime observer emits structured fields for stage, outcome, reason, key
version, output schema, provider calls, singleflight wait time, and embedding
batch totals/hits/misses/deduplication. Outcomes use `hit`, `miss`, `computed`,
`wait`, `bypass`, `corrupt`, and `error_fallback`.

The artifact observer never receives request or payload bytes. New DAG logs and
span fields omit complete artifact keys, bodies, prompts, file/image references,
and provider error details; they retain IDs, booleans, lengths, counts, and
concrete error classes.

## Validation

The implementation has focused tests for:

- canonical keys, tenant isolation, credential exclusion, and schema versions;
- checksum validation, corrupt-row eviction, fail-open storage, and immutable
  first-writer-wins behavior;
- local and Redis-backed concurrent provider suppression;
- exact input bytes, duplicate input ordering, partial batch hits, and invalid
  provider response rejection;
- stable chunk/generated IDs, desired-state diffs, and stale-attempt fencing;
- all eight crash boundaries, with final DB/vector/Wiki/Graph snapshots equal
  to a clean run after retry;
- local and Redis per-knowledge mutation-lock ownership and wait behavior;
- atomic, idempotent knowledge publication plus tenant storage accounting;
- SQLite migration up/down and uniqueness behavior;
- duplicate migration-version rejection for PostgreSQL and SQLite directories;
- DocReader, chat, embedding, VLM, wiki, multimodal, and graph stage adapters.

Run the focused suite with:

```bash
go test -count=1 \
  ./internal/artifact \
  ./internal/application/repository \
  ./internal/application/service \
  ./internal/database \
  ./internal/models/chat \
  ./internal/models/embedding \
  ./internal/models/vlm

go test -race -count=1 \
  ./internal/artifact \
  ./internal/application/repository \
  ./internal/application/service \
  ./internal/models/embedding
```

Live PostgreSQL/MySQL/Redis/Neo4j/vector integration should also be exercised in
the deployment environment before a broad rollout. URL-based DocReader inputs
currently bypass artifacts because their remote content cannot be proven stable
from the URL alone.
