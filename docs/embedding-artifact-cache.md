# Ingestion embedding artifact cache

Ingestion embeddings are persisted as tenant-scoped `embedding.vector`
derived artifacts. The cache decorator is installed when an embedding model is
created, but it is active only when the context contains both a tenant ID and
one of the explicitly supported ingestion operations: chunk, summary,
question, FAQ, graph entity, graph relation, or Wiki page embedding. Search
queries, interactive queries, reranking, and unclassified embedding calls pass
through without cache reads or writes.

The artifact input digest is SHA-256 of the exact string received at the
embedding model boundary. In vector indexing this boundary is after the
existing image-payload scrubbing and safety truncation. No whitespace,
punctuation, case, or Unicode normalization is added by the cache. Chunk IDs,
source IDs, knowledge IDs, knowledge-base IDs, and batch positions are not part
of the identity.

Keys use `artifactkey.KeyInput`, artifact schema `embedding-artifact/v1`, and
producer `embedding-producer/v1`. Identity includes the stable `tenant:<id>`
scope, effective model name, explicitly configured model/prompt revision,
vector dimension, truncation and dimension-override settings, document mode,
and reviewed embedding instruction/prefix fields.

Configuration identity is an explicit allowlist. API keys, `Authorization` and
other custom headers, arbitrary extra configuration, passwords, tokens,
secrets, credentials, and BaseURL are excluded. BaseURL is treated as transport
routing rather than model identity; deployments that route different models
through different endpoints must give those models distinct effective model
names or revisions. Credential and endpoint rotation therefore does not
invalidate already computed vectors. VLM, chunk-size, Wiki, and other unrelated
ingestion settings are also excluded.

## Binary vector payload

Vectors use payload encoding `embedding-f32-le-v1`; JSON floats are not used.
The byte layout is:

1. eight-byte magic/schema header `WKNEMB`, byte `1`, byte `0`;
2. unsigned 32-bit little-endian dimension;
3. exactly `dimension` IEEE-754 float32 values in little-endian order.

Encoding preserves each finite float32 bit pattern exactly. A zero dimension,
NaN, positive or negative infinity, wrong encoding/magic/dimension, truncated
payload, or extra byte is corrupt. Corrupt succeeded artifacts fail closed with
`ErrArtifactCorrupt`; ingestion does not silently call the provider.

Batch lookup is performed per unique text digest. Cache hits are placed into
their original positions immediately; only unique misses are sent through the
existing provider batch/pool path, and their vectors are then fanned back out
to every original occurrence. Consequently, partial hits preserve input order
and the existing caller-side `SourceID` mapping while permitting reuse across
documents in the same tenant.

Every returned position owns an independent vector slice. Cache-decoded,
provider-returned, and duplicate fan-out values do not share mutable backing
arrays, so caller mutation cannot alter another result or a later cache read.

## Lease, failure, and retry behavior

Claims use the PR4 repository lease protocol. Busy callers wait for the
canonical owner and never bypass the cache by calling the provider. During a
provider batch, an instance-scoped heartbeat renews every owned artifact. A
lost owner stops before `Complete`, allowing an expired lease to be taken over
and preventing an old result from overwriting the new canonical vector. The
heartbeat and ticker stop on success, provider error, lost ownership, or
context cancellation.

Failed claims are cleaned up with the request context while it is usable. If
the request was canceled, cleanup uses `context.WithoutCancel` plus a bounded
cleanup timeout; it never uses an unbounded background context. Persisted error
messages contain neither source text, vectors, provider responses, nor owner
tokens.

Completion is per artifact. If completion fails partway through a batch,
already succeeded artifacts remain succeeded and only the current and later
owned claims are failed. A retry hits the completed prefix and computes only
unfinished vectors.

## Observation and vector-store writes

The ingestion observation wrapper receives cache outcomes without changing the
public `Embedder` API. `total_items` is original batch size,
`computed_items` is the number of unique texts actually sent to the provider,
and `reused_items` includes cache hits, busy-then-hit results, and within-batch
duplicates. Request and batch counts describe actual provider calls only. A
full hit reports zero provider requests and `cache_status=hit`; partial hits and
new computations report `cache_status=miss`; repository, corrupt, timeout, and
lease failures report `cache_status=error`.

Embedding artifact reuse is separate from vector-store reconciliation. A cache
hit still supplies a precomputed vector to the existing `BatchSave` path; the
vector database remains responsible for its own idempotency and persistence.
