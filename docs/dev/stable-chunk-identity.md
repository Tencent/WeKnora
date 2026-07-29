# Stable chunk identity

This document defines deterministic chunk identity and its initial persistence
for ingestion text chunks. The computation lives in `internal/contentkey`; the
identity is stored separately from the random database row ID.

## Purpose

A stable identity lets later ingestion work compare newly parsed chunks with
existing chunks. Identical tenant, knowledge, chunk type, normalized content,
document structure, parent identity, and duplicate occurrence produce the same
UUID.

Stable chunk identity is a business identity. It is not an embedding cache key
and, in this phase, is not a database row ID.

## Current phase boundaries

The implementation includes:

- conservative content normalization;
- SHA-256 content and context digests;
- canonical identity-field encoding;
- deterministic UUIDv5 generation;
- duplicate-ordinal assignment;
- unit tests for these contracts;
- nullable `stable_identity` and `identity_version` chunk columns;
- non-unique stable-identity lookup indexes;
- stable identity assignment for flat `text`, `parent_text`, and parent-child
  child `text` chunks through the production path
  `processChunks -> buildIngestionTextChunks -> contentkey.AssignChunkIdentities`.

It does not change random `Chunk.ID` generation, parent/previous/next row-ID
reference semantics, deletion behavior, vector indexes, provider calls, or PR1
observation counts. Rebuild still soft-deletes and recreates rows and still
performs parser and embedding work.

## Normalization v1

`chunk-normalization-v1` applies these rules in order:

1. convert CRLF to LF;
2. convert remaining CR to LF;
3. normalize Unicode to NFC;
4. remove whitespace around the complete string.

It deliberately preserves:

- internal spaces and tabs;
- consecutive newlines;
- Markdown markup and image references;
- fenced-code formatting;
- table formatting;
- case;
- punctuation.

Normalization removes representation-level differences and does not attempt
semantic cleanup. `ChunkContentDigest` is the lowercase hexadecimal SHA-256 of
the normalized UTF-8 content.

## Identity v1

`chunk-identity-v1` contains the following fields in fixed order:

1. identity version;
2. normalization version;
3. tenant ID;
4. knowledge ID;
5. chunk type;
6. parent identity;
7. normalized content digest;
8. normalized context digest;
9. duplicate ordinal.

The normalization version is explicit because a future normalization contract
must not silently reuse v1 identities. Context has its own digest so body-content
identity is not confused with structural placement.

Strings use an unsigned 64-bit big-endian byte-length prefix followed by their
UTF-8 bytes. Tenant ID is unsigned 64-bit big-endian; duplicate ordinal is
encoded as a signed value in the same fixed-width representation. Length
prefixes prevent concatenation collisions such as `("ab", "c")` and
`("a", "bc")`.

The canonical bytes are passed to UUIDv5 using a fixed WeKnora chunk namespace.
The result is a valid 36-character UUID and remains compatible with existing
UUID-shaped storage and downstream assumptions. A raw 64-character SHA-256
digest is not used as a chunk ID.

## Duplicate content

Duplicate ordinal is assigned in document order within one identity base. The
base contains every identity field except the ordinal:

```text
base -> number of earlier occurrences of the same base
```

The first occurrence is zero, the second is one, and so on. Unrelated content
inserted between equal chunks does not change their ordinals. Inserting another
equal chunk before existing occurrences shifts later ordinals; this is an
intentional known limitation of the v1 contract.

The helper returns a copy and does not mutate its input slice.

## Parent-child identity

Parent-child assignment is connected to ingestion in this order:

1. compute all parent content/context digests;
2. assign parent duplicate ordinals;
3. generate all parent stable identities;
4. place the stable parent identity in each child identity;
5. assign child duplicate ordinals within that parent scope;
6. generate child identities;
7. only then resolve database parent/previous/next references.

Flat text chunks use an empty parent identity. Equal child content beneath two
different parents produces different identities. Equal child content beneath
one parent is separated by duplicate ordinal.

## Title and ContextHeader

The document title does not participate in chunk identity. A title is metadata
and currently affects embedding input; changing it should not invalidate every
database chunk reference. A future embedding cache key must independently bind
the exact title-bearing embedding input.

`ContextHeader` represents a structural heading breadcrumb. It participates as
a separately normalized `ContextDigest`. This distinguishes equal body content
under different document sections without merging structural data into the
body-content digest.

## ContentHash

The existing `Chunk.ContentHash` remains unchanged. It currently has FAQ-specific
normalization, matching, import, and diff semantics and is not the v1 chunk
content digest.

## Excluded chunk types and paths

The first production integration includes only flat `text`, `parent_text`, and
parent-child child `text` chunks.

The following remain outside the initial identity integration:

- image OCR and caption chunks, which need image-byte digest and stable image
  occurrence material rather than URL or VLM output text;
- summaries and questions, which need purpose, prompt, model, and artifact
  version semantics;
- FAQ chunks, which have their own business identity and `ContentHash`;
- Wiki pages, which have page identity and existing materialization rules;
- Graph entities and relationships, which are graph business objects;
- table summaries and columns, which are generated artifacts;
- clone/move paths, which already remap random row IDs and need a separately
  tested target-knowledge identity pass.

## Old data and soft deletion

No history is backfilled. Existing random UUID chunks remain readable with
empty stable-identity fields. New or rebuilt ingestion text chunks populate the
new fields; other chunk types leave them empty.

`DeleteChunksByKnowledgeID` currently performs a GORM soft delete. Reusing a
deterministic UUID directly as the existing `Chunk.ID` would collide with the
soft-deleted row on a repeated rebuild. Consequently, stable business identity
must initially remain separate from random database row identity.

The persistence schema uses nullable stable-identity/version columns and a
non-unique lookup index while retaining random row IDs. It deliberately does
not add an ordinary unique constraint that would conflict with soft-deleted
rows.

## Later reconcile phase

After stable identity is persisted, a dedicated reconcile phase can:

- match newly parsed and existing chunks by stable identity;
- preserve row IDs and vectors for unchanged chunks;
- allocate random row IDs for added chunks;
- soft-delete removed chunks;
- optionally restore a previously removed matching row;
- resolve parent/previous/next references after final row IDs are known;
- embed only added or otherwise changed inputs.

Parser execution and formal derived-artifact caches remain separate concerns.
