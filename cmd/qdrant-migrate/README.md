# Enable ranked keyword search for existing Qdrant data

This guide is for users who already use Qdrant and want to enable the optional
ranked keyword search. It does not move data from another database into Qdrant.
If you use another search engine, or want to keep Qdrant's current keyword search,
you do not need to change anything.

The `qdrant-migrate` command copies existing Qdrant data into new Qdrant collections
with the additional search index. It keeps the original collections unchanged.
For a fresh Qdrant installation with no existing data, skip the copy step and use
the settings below.

Qdrant keyword search defaults to `text`, the existing payload-text filter. Set
`QDRANT_KEYWORD_SEARCH=bm25` to rank keyword candidates using Qdrant's native BM25
model. Dense retrieval and WeKnora's existing result fusion remain in use; no
external embedding model or additional service is needed for BM25.

This path is tested with Qdrant **1.16.2**, the version shipped in Docker Compose.
It requires native BM25 inference support. A connection test only checks the
connection, not collection readiness for BM25.

## Fresh Qdrant installations: no migration needed

Use a fresh collection prefix:

```dotenv
RETRIEVE_DRIVER=qdrant
QDRANT_COLLECTION=ranked_embeddings
QDRANT_KEYWORD_SEARCH=bm25
```

New collections get both the unnamed dense vector and a sparse BM25 vector.
For a new database-backed vector store, select `bm25` under **Keyword Search**
in its advanced settings, or set `index_config.qdrant_keyword_search` to `bm25`
through the existing vector-store API. Use the same mode on every writer to a
collection; a writer configured for `text` does not maintain BM25 vectors.

## Existing Qdrant data: enable ranked keyword search

The following steps apply to Qdrant stores configured through environment variables.

Existing collections must be copied before enabling BM25. Qdrant 1.16.2 cannot
add a new sparse vector field to an existing collection. Migration copies all
dimension-specific collections, including disabled points, into a new prefix.
It preserves point IDs, dense vectors and payloads; it does not call the dense
embedding model or modify the source collections.

1. Stop every application instance and worker that can write to this store.
   Keep them stopped through migration, validation and cutover. This is an
   offline migration, not a live synchronization job. Allow disk space for both
   copies plus the new sparse index.
2. Export the existing `QDRANT_HOST`, `QDRANT_PORT`, `QDRANT_API_KEY` and
   `QDRANT_USE_TLS` environment variables in the migration shell. The command
   does not load `.env` itself. Use the gRPC port, normally `6334`.
3. From the repository root, run:

   ```sh
   go run ./cmd/qdrant-migrate \
     --source weknora_embeddings \
     --target ranked_embeddings
   ```

   Host, port and TLS may also be supplied with `--host`, `--port` and `--tls`.
   `--timeout` defaults to `1h`. Credentials are read from the environment.
   Neither prefix may start with the other: older text-search implementations
   match collection prefixes broadly, which would otherwise compromise rollback.

4. The command exits successfully only when source and destination point counts
   match the copied count for every collection. If interrupted, keep writers
   stopped and rerun the same command with `--resume`. It upserts the original
   IDs, so repeating completed pages does not create duplicate points. Use
   `--resume` only for this migration's destination, never an unrelated collection.
5. Change `QDRANT_COLLECTION` to `ranked_embeddings` and
   `QDRANT_KEYWORD_SEARCH` to `bm25` on every app instance. Restart with the new
   settings and check representative searches before allowing writes again.

The command does not change application database bindings. Existing stores
registered through the UI have immutable connection/index settings; this command
alone does not migrate their knowledge-base bindings. For those stores, use a
separately planned binding/reindex workflow rather than deleting a bound store.

Before new writes resume, rollback means restoring the old collection prefix
and `text` mode. After new writes resume, the source is a stale snapshot: reverting
to it would lose visibility of subsequent changes. Keep the source until the
migration has been accepted; this command never deletes it.

## Ranking and language behavior

`weknora_bm25_v1` uses Qdrant's multilingual tokenizer, lowercase normalization,
no language-specific stemming/stopwords, and fixed BM25 options `k=1.2`, `b=0.75`,
`avg_len=256` tokens. These settings are shared by indexing and queries. Changing
them requires rebuilding the sparse vectors; do not change them in place.

Chinese and Japanese segmentation and Korean term matching are covered locally.
This is lexical matching, not translation or morphological analysis. For example,
Korean `도서관` does not match the inflected token `도서관에서`; dense retrieval
still provides the semantic side of hybrid search. Identifier punctuation is
tokenized, so a BM25 hit is not a strict whole-identifier equality check.

Each dimension collection returns its own ranked list, up to `TopK` candidates.
The existing service normalizes/fuses these lists. Raw BM25 scores from different
collections are not directly compared or truncated in collection iteration order.
Existing enabled/KB/document/tag/exclusion filters apply before candidate ranking.
Backend failures remain errors or explicitly marked partial results, not empty
successful searches or silent fallback to unranked search.

BM25 adds index storage and work during ingestion. The existing storage estimate
does not include sparse-vector storage. The small local relevance fixture is a
regression check, not a production quality or throughput benchmark. Evaluate your
own queries and ingestion cost before enabling the option broadly.

## Local verification

Start a disposable Qdrant 1.16.2 server, then run:

```sh
QDRANT_TEST_PORT=6334 go test ./internal/application/repository/retriever/qdrant -count=1
go test ./internal/types ./cmd/qdrant-migrate
```

The integration test connects only to localhost and creates/deletes random test
collections. It checks ranking, multilingual queries, filters, migration/resume,
dense retrieval and write/copy/move/delete compatibility. Without the environment
variable the live test is skipped; the package's ordinary tests still run.

References: [Qdrant full-text search](https://qdrant.tech/documentation/search/text-search/full-text-search/)
and [hybrid search](https://qdrant.tech/documentation/search/text-search/hybrid-search/).
