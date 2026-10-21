# Optional PostgreSQL BM25 backend

`POSTGRES_SEARCH_EXTENSION=pg_textsearch` enables the pg_textsearch 1.3.1
dialect for PostgreSQL retrieval. The default remains `pg_search` (ParadeDB).

Requirements: PostgreSQL with pgvector and pg_textsearch installed, with
`pg_textsearch` in `shared_preload_libraries` before initialization. This does
not configure a managed cloud instance or install server extensions for you.
`DB_SSLMODE` configures both application and migration connections; use
`verify-full` with `PGSSLROOTCERT` for authenticated TLS.

The new backend stores Jieba-tokenized text separately in `search_content`.
Original text and vectors are preserved. Migration 134 creates the BM25
index, and startup backfills missing derived text in batches of 500 rows.
Failures in this opt-in initialization path abort startup. A target database
with `pg_search` already installed is rejected: prepare a separate target,
retain the old database, and validate the copy before switching connections.
This option does not promise preservation of ParadeDB indexes or SQL APIs.

Keyword queries retain knowledge-base/document/tag/enabled filters. Negative
BM25 distances become positive scores for the existing higher-is-better
fusion contract. Keyword-only rows store NULL instead of an empty vector.
Existing vector retrieval and default ParadeDB retrieval are unchanged.

Tests (use only disposable databases; the fixtures modify their contents):

```sh
go test ./internal/application/repository/retriever/postgres ./internal/container -count=1
# Real pg_textsearch fixture, guarded by the database name:
export WEKNORA_TEXTSEARCH_TEST_DSN='postgres://user:password@localhost:5432/weknora_textsearch_test?sslmode=disable'
go test ./internal/application/repository/retriever/postgres -run TestTextSearch -count=1
go test ./internal/container -run TestInitDatabaseTextSearch -count=1
# Optional default-backend regression uses WEKNORA_PARADEDB_TEST_DSN and
# requires a disposable database named weknora_082_upgrade_test.
```

Integration tests skip unless their DSNs are explicitly supplied. No API keys,
embedding models, production databases, or paid services are used by the tests.
