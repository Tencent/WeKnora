# GraphRAG per-chunk extraction artifact cache

PR8 adds a tenant-scoped, persistent content-addressed cache around the single
LLM call that extracts candidate graph nodes and relationships from one source
chunk.

The key includes the exact chunk text digest, model ID and model revision,
an allowlisted model-output configuration (runtime name, provider/interface/revision/thinking
controls), the complete extraction template configuration, prompt version, and
producer version. Credentials, routing secrets, chunk database IDs, document
IDs, knowledge-base IDs, and graph-store state are deliberately excluded.
An explicit `model_revision` is preferred, then `revision`, with the runtime
model name used only as the compatibility fallback. This separates the stable
model identity from a mutable display/runtime name while retaining safe
invalidation for providers that do not expose revisions.

The cached JSON contains only copied candidate nodes and relations. It excludes
`GraphData.Text` and all node chunk bindings, even if either is present in an
extractor result. Nodes are normalized by trimmed name, merged by name, and
sorted; attributes are trimmed, deduplicated, and sorted. Relations are
trimmed, deduplicated by `(source, target, type)`, and sorted by that tuple.
Consequently equivalent provider results have deterministic artifact bytes.

On a hit, the current chunk ID is rebound to every node and the normal
namespace write, aggregation, deduplication, and graph indexing path still runs. This
makes identical content reusable across chunks and documents in one tenant
without caching mutable graph-store side effects. Tenant scope prevents reuse
across tenants.

Concurrent requests use the derived-artifact lease protocol. One worker owns
the extraction while other workers wait for the durable result. Provider or
parser failures mark the artifact failed; expired leases can be taken over.
The owner renews its lease during a slow provider request. Lost ownership
prevents the stale worker from publishing, while the takeover winner becomes
the durable result. Busy waits are bounded and context-cancellable. Lease,
wait, poll, and detached failure-cleanup durations are injectable in tests.

Malformed artifacts are never returned to graph materialization. They trigger
a safe uncached recomputation and report a cache error in observation. Because
the repository intentionally does not allow a worker without ownership to
mutate a succeeded row, that recomputation is not written over the corrupt row;
subsequent maintenance can inspect it instead of losing forensic evidence.
Complete failures best-effort transition the owned claim to `failed`, allowing
the next request to reclaim and recompute it.

The `graph.extract_chunk` observation reports `cache_status`,
`artifact_cache_event`, `artifact_kind`, and the input digest prefix. Cache hits
report zero model requests and one reused item; misses preserve the real model
request and report one computed item. A recomputation caused by corrupt cache
state preserves the real request count and reports `cache_status=error` with
`artifact_cache_event=failed`.

## Validation

The focused test suite covers partial hits across multiple chunks, production
`Handle -> AddGraph` hits and current chunk-ID rebinding, payload isolation,
single-computation concurrency, bounded waits and cancellation, heartbeat and
lease takeover, lost ownership, Complete cleanup/retry, corrupt payload
fallback, model revision invalidation, production span output, and deterministic
normalization.

Run on Linux/CI with CGO enabled (SQLite test repository):

```bash
go test ./internal/application/service \
  -run 'TestGraphExtractArtifact|TestGraphExtractCandidate|TestChunkExtractHandleCache' \
  -count=1
```

The full repository gate remains:

```bash
go test ./...
```
