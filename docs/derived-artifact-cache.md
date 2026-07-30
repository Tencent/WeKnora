# Persistent derived artifact cache foundation

## Scope

PR4 provides a tenant-scoped, persistent, content-addressed foundation for
reusable derived artifacts. It defines stable keys, durable records,
claim/lease ownership, integrity checks, migrations, and observation mapping.

PR4 does not connect the cache to parsing, VLM OCR/caption, embedding, wiki,
GraphRAG, summary, question, or FAQ producers. It does not implement object
upload, cache GC, or a `GetOrCompute` framework. Consequently PR4 has not
reduced any model calls yet.

## Key v1 and invalidation

`internal/artifactkey` encodes these fields as the fixed-order JSON array
`derived-artifact-key/v1` and hashes it with SHA-256:

1. artifact kind
2. tenant scope
3. input digest
4. model ID
5. model revision
6. prompt version
7. config digest
8. producer version

Raw document text, prompts, and image bytes are never key material; callers
first reduce them to digests. Map-backed config is encoded with sorted keys.
Absent optional metadata is normalized to an explicit empty string, so v1
intentionally does not distinguish absent from empty.

Changing kind, model, revision, prompt, config, or producer version produces a
new key and therefore a precise miss. Invalidation never overwrites or
soft-deletes an older key. Old records may be garbage-collected by future work.

The table also has a mandatory `tenant_id`; `(tenant_id, artifact_key)` is
unique. Every read and state transition includes the tenant, so one tenant
cannot observe or mutate another tenant's row even if both use the same key.

## State machine and ownership

```text
pending / failed / expired computing
                | Claim
                v
            computing
             /   |   \
      Complete  Fail  lease expires
          |      |         |
          v      v         v
     succeeded  failed   takeover by a new Claim
```

`Claim` returns `hit`, `claimed`, or `busy`. A live computing lease is busy.
Failed and pending rows can be claimed again. An expired computing row can be
taken over; takeover replaces the owner token and increments `attempt_count`.
A succeeded row is immutable and can only be returned as a hit after integrity
validation.

The owner token is an opaque, non-secret random identifier. `Complete`, `Fail`,
and `RenewLease` condition updates on tenant, key, computing status, owner, and
a live lease. A stale or superseded worker therefore cannot overwrite, fail, or
renew work owned by its replacement. Zero affected rows means lost ownership.
An exact repeated `Complete` is idempotent; a different result is a conflict.

Lease renewal only extends a live lease and never shortens it. If a worker
crashes, it makes no state change: after lease expiry another worker recovers
the work through Claim takeover. Callers use UTC timestamps and should renew
before expiry for long-running computations.

## Payload, object references, and integrity

Small results are stored inline in `payload`. Future large results may use
`object_uri`; PR4 neither uploads nor downloads those objects. A successful
result requires payload or object URI and always requires a valid SHA-256
`payload_digest`.

For inline payloads, `Complete` recomputes SHA-256 before writing succeeded.
`GetSucceeded` and the succeeded path in `Claim` recompute it again before
returning a hit. A mismatch returns `ErrArtifactCorrupt`; a corrupt row is never
a hit. Reads deliberately do not rewrite a corrupt row to failed, because a
read must not create an implicit state transition. Object URI results retain a
digest for later object-integrity validation, but PR4 does not fetch the object.

## Concurrency foundation

The database unique constraint is the correctness boundary. Claim first uses a
conflict-safe insert, then reads the existing row and uses a conditional update
for retry or takeover. Concurrent updates include the previous status and, for
takeover, the expired lease predicate. Only a positive affected-row count can
be reported as claimed.

This works across SQLite, PostgreSQL, and MySQL without an in-process mutex.
SQLite lock retries are a bounded dialect-specific accommodation, not the
ownership mechanism. An in-process `singleflight` may later reduce redundant
local waiters, but it cannot replace the durable lease: it cannot coordinate
multiple server instances and disappears when a process crashes.

## Observation semantics

Artifact events reuse `IngestionOperationObservation` and add only the optional
`artifact_cache_event` detail:

- hit: `cache_status=hit`, `reused_items=1`
- computed: `cache_status=miss`, `computed_items=1`
- failed: `cache_status=error`, `success=false`
- miss, claimed, busy, and lease takeover: miss with zero computed/reused items
- all repository events: `request_count=0`

Observations contain kind and digest prefixes, never payload, prompt, image
bytes, or owner tokens. Because PR4 has no production consumer, existing
ingestion paths continue to report their current cache status and do not
fabricate hits.

## Follow-up boundary

PR5 may connect VLM OCR/caption producers by constructing canonical PR4 keys,
claiming ownership before provider calls, and completing or failing the durable
record afterward. That integration must preserve operation/request/item counts
and is not part of PR4. Embedding/parser/wiki/GraphRAG integration, object-store
upload, and complete cache GC also remain outside PR4.
