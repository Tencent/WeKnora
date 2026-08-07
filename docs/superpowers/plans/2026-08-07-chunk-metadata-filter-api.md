# Chunk Metadata Filter API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a validated boolean `metadata_filter` to REST hybrid search and enforce it during chunk-level vector/keyword retrieval with a PostgreSQL reference implementation.

**Architecture:** Define a typed recursive filter AST in `internal/types`, carry it through `SearchParams` and `RetrieveParams`, and compile it into parameterized PostgreSQL JSONB predicates. Store a reserved `access_metadata` object with each indexed chunk record. Retrieval fails closed when a store group cannot enforce the filter, and result enrichment applies the same predicate before returning parent, nearby, or relation chunks.

**Tech Stack:** Go, Gin, GORM, PostgreSQL JSONB, pgvector, ParadeDB BM25, `sqlmock`, `testify`, versioned SQL migrations, Swagger annotations, VitePress documentation.

## Global Constraints

- Only the REST `POST /knowledge-bases/:id/hybrid-search` contract is in scope; Agent tools, MCP, session QA, and web search remain unchanged.
- `metadata_filter` is a retrieval filter, not automatic identity authorization; the caller must be trusted if it is used as an ACL input.
- Supported operators in this PR are only `eq` and `in`; supported boolean groups are only `and` and `or`.
- Missing metadata fields are non-matches when a filter is supplied; no filter preserves existing behavior.
- The filter must be applied before vector/keyword top-K finalization; post-filtering a limited result set is not acceptable.
- Unsupported retriever stores must return a typed capability error instead of silently ignoring the filter.
- Chunk access metadata is read from a reserved `access_metadata` object inside `Chunk.Metadata`; no public metadata-write API is added in this PR.
- Every task ends with its own focused test command and a small commit.

---

## File map

- `internal/types/metadata_filter.go`: Filter AST, operator constants, validation, and pure metadata matching used by context-enrichment defense.
- `internal/types/metadata_filter_test.go`: AST validation and matching tests.
- `internal/types/search.go`: REST/application `SearchParams.MetadataFilter` field.
- `internal/types/retriever.go`: Backend `RetrieveParams.MetadataFilter` field.
- `internal/types/embedding.go`: `IndexInfo.AccessMetadata` indexing payload.
- `internal/types/chunk_access_metadata.go`: Reserved `Chunk.Metadata["access_metadata"]` extraction helper and tests.
- `internal/application/service/knowledgebase_search.go`: Validate and copy the filter into vector and keyword retrieval params; pass it into result processing.
- `internal/application/service/knowledgebase_search_storegroup.go`: Fail closed before retrieval when a resolved engine cannot enforce the filter.
- `internal/application/service/retriever/composite.go`: Report whether all active engines in a composite support metadata filtering.
- `internal/application/service/knowledgebase_search_results.go`: Apply the same filter to primary and enrichment chunks.
- `internal/handler/knowledgebase.go`: REST validation and Swagger description.
- `internal/handler/knowledgebase_hybrid_search_test.go`: HTTP acceptance/rejection and request propagation coverage.
- `internal/application/repository/retriever/postgres/structs.go`: Persist access metadata in PostgreSQL index rows.
- `internal/application/repository/retriever/postgres/repository.go`: Preserve metadata during index copy and apply predicates to keyword/vector SQL.
- `internal/application/repository/retriever/postgres/metadata_filter.go`: Parameterized JSONB predicate compiler.
- `internal/application/repository/retriever/postgres/metadata_filter_test.go`: Compiler and SQL-injection regression tests.
- `migrations/versioned/000079_chunk_access_metadata_index.up.sql`: Add PostgreSQL index metadata storage and JSONB index.
- `migrations/versioned/000079_chunk_access_metadata_index.down.sql`: Revert the migration.
- `internal/application/service/chunk.go`, `knowledge_process.go`, `image_multimodal.go`, `extract.go`, `knowledge_faq.go`, `knowledge_faq_import.go`: Carry chunk access metadata into every existing `IndexInfo` construction path.
- `website-docs/04-api/02-api-knowledge.md`: REST request grammar, semantics, and example.
- `website-docs/03-features/05-retrieval-engines.md`: PostgreSQL support matrix, indexing/backfill requirement, and unsupported-backend behavior.
- `docs/swagger.json`, `docs/swagger.yaml`, `docs/docs.go`: Regenerated Swagger output after handler annotations change.

## Interfaces between tasks

The following names and shapes are fixed before implementation begins:

```go
type MetadataFilterOperator string

const (
	MetadataFilterOpEqual MetadataFilterOperator = "eq"
	MetadataFilterOpIn    MetadataFilterOperator = "in"
)

type MetadataFilter struct {
	And    []MetadataFilter       `json:"and,omitempty"`
	Or     []MetadataFilter       `json:"or,omitempty"`
	Field  string                 `json:"field,omitempty"`
	Op     MetadataFilterOperator `json:"op,omitempty"`
	Value  any                    `json:"value,omitempty"`
	Values []any                  `json:"values,omitempty"`
}

func (f *MetadataFilter) Validate() error
func (f *MetadataFilter) Matches(metadata JSONMap) bool
```

`types.SearchParams` and `types.RetrieveParams` each expose:

```go
MetadataFilter *MetadataFilter `json:"metadata_filter,omitempty"`
```

The indexing payload is separate from the filter AST:

```go
type IndexInfo struct {
	// existing fields...
	AccessMetadata JSONMap
}
```

`Chunk.AccessMetadata()` reads only the reserved object:

```json
{
  "access_metadata": {
    "employee_nature": "formal",
    "department": "research"
  }
}
```

The first PostgreSQL capability check is exposed by:

```go
func (c *CompositeRetrieveEngine) SupportsMetadataFilter() bool
```

It returns true only when every active engine in that composite reports `PostgresRetrieverEngineType`; all other engines are unsupported in this PR.

---

### Task 1: Add the typed filter AST and pure evaluator

**Files:**
- Create: `internal/types/metadata_filter.go`
- Create: `internal/types/metadata_filter_test.go`
- Modify: `internal/types/search.go:230-258`

**Interfaces:**
- Consumes: JSON request bodies decoded by `encoding/json` and row metadata represented as `types.JSONMap`.
- Produces: `MetadataFilter`, `MetadataFilterOperator`, `(*MetadataFilter).Validate()`, `(*MetadataFilter).Matches()`, and `SearchParams.MetadataFilter` for the transport/application layers.

- [ ] **Step 1: Write failing validation and matching tests**

Add table-driven tests with these exact cases:

```go
func TestMetadataFilterValidateAcceptsNestedPolicy(t *testing.T) {
	filter := MetadataFilter{
		Or: []MetadataFilter{
			{And: []MetadataFilter{
				{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "formal"},
				{Field: "department", Op: MetadataFilterOpEqual, Value: "research"},
			}},
			{And: []MetadataFilter{
				{Field: "employee_nature", Op: MetadataFilterOpEqual, Value: "contractor"},
				{Field: "department", Op: MetadataFilterOpEqual, Value: "finance"},
			}},
		},
	}
	if err := filter.Validate(); err != nil {
		t.Fatalf("valid nested filter rejected: %v", err)
	}
}

func TestMetadataFilterMatchesMissingFieldAsFalse(t *testing.T) {
	filter := MetadataFilter{Field: "department", Op: MetadataFilterOpEqual, Value: "research"}
	if filter.Matches(JSONMap{"employee_nature": "formal"}) {
		t.Fatal("missing protected field must not match")
	}
}
```

Also cover malformed mixed nodes, empty `and`/`or`, empty `in`, wrong operator/value shape, invalid field length/control characters, depth greater than 8, more than 64 nodes, scalar equality, scalar membership, array equality, array intersection, and the cross-field combination that must not over-grant.

- [ ] **Step 2: Run the focused tests and confirm RED**

Run:

```bash
go test ./internal/types -run 'TestMetadataFilter' -count=1
```

Expected: FAIL because the filter types and methods do not exist.

- [ ] **Step 3: Implement the minimal AST and evaluator**

Implement recursive validation with depth/node counters. A node must be exactly one of group or predicate. Accept only string, number, and boolean JSON scalars. Validate field names as trimmed non-empty keys of at most 64 characters and reject control characters. Implement `Matches` with `and`/`or`, scalar equality/membership, array element matching, and false for missing fields.

Add the pointer field to `types.SearchParams` without changing any existing request defaults.

- [ ] **Step 4: Run the focused tests and confirm GREEN**

Run:

```bash
go test ./internal/types -run 'TestMetadataFilter' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the domain contract**

```bash
git add internal/types/metadata_filter.go internal/types/metadata_filter_test.go internal/types/search.go
git commit -m "feat: add metadata filter contract"
```

### Task 2: Accept and validate `metadata_filter` at the REST boundary

**Files:**
- Modify: `internal/handler/knowledgebase.go:270-330`
- Modify: `internal/handler/knowledgebase_hybrid_search_test.go`
- Modify: `website-docs/04-api/02-api-knowledge.md:99-135`

**Interfaces:**
- Consumes: `types.SearchParams.MetadataFilter` and its `Validate` method from Task 1.
- Produces: POST hybrid-search requests that preserve a valid nested filter and reject invalid filters before calling the service.

- [ ] **Step 1: Write failing HTTP tests**

Extend the existing `hybridSearchTestService` capture test with:

```go
func TestHybridSearchAcceptsNestedMetadataFilter(t *testing.T) {
	svc := &hybridSearchTestService{}
	body := `{"query_text":"报销","metadata_filter":{"or":[{"and":[{"field":"employee_nature","op":"eq","value":"formal"},{"field":"department","op":"eq","value":"research"}]}]}}`
	response := performHybridSearchRequest(svc, body)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	if svc.searchParams.MetadataFilter == nil || len(svc.searchParams.MetadataFilter.Or) != 1 {
		t.Fatalf("metadata_filter was not propagated: %+v", svc.searchParams.MetadataFilter)
	}
}
```

Add a malformed filter case and assert the service is not called and the response is a validation error.

- [ ] **Step 2: Run the handler tests and confirm RED**

Run:

```bash
go test ./internal/handler -run 'TestHybridSearch.*MetadataFilter' -count=1
```

Expected: FAIL because the request field is not yet validated/propagated.

- [ ] **Step 3: Validate after JSON binding and update API documentation**

In `KnowledgeBaseHandler.HybridSearch`, after `ShouldBindJSON`, call `req.MetadataFilter.Validate()` when non-nil and return `apperrors.NewValidationError` without invoking `HybridSearch` on failure. Keep the existing query-text/precomputed-vector validation unchanged.

Add the `metadata_filter` field table and the nested `or`/`and` example to `website-docs/04-api/02-api-knowledge.md`. State that omitted filters preserve existing behavior and that the filter is not an identity resolver.

- [ ] **Step 4: Run the handler tests and confirm GREEN**

Run:

```bash
go test ./internal/handler -run 'TestHybridSearch' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the REST contract**

```bash
git add internal/handler/knowledgebase.go internal/handler/knowledgebase_hybrid_search_test.go website-docs/04-api/02-api-knowledge.md
git commit -m "feat: expose metadata filter on hybrid search"
```

### Task 3: Carry the filter through the common retrieval pipeline

**Files:**
- Modify: `internal/types/retriever.go:39-72`
- Modify: `internal/application/service/knowledgebase_search.go:87-255,322-402`
- Modify: `internal/application/service/knowledgebase_search_storegroup.go:90-150`
- Modify: `internal/application/service/knowledgebase_search_fanout_test.go`
- Create: `internal/application/service/knowledgebase_search_metadata_filter_test.go`
- Modify: `internal/errors/errors.go:20-48,236-270`

**Interfaces:**
- Consumes: validated `SearchParams.MetadataFilter` from Task 2.
- Produces: every vector/keyword `RetrieveParams` carries the same pointer; unsupported resolved store groups return `ErrMetadataFilterUnsupported` before retrieval.

- [ ] **Step 1: Write failing propagation and fail-closed tests**

Add tests that construct `types.SearchParams{MetadataFilter: filter}` and verify both vector and keyword params built by `buildRetrievalParams` contain the same filter. Add a store-group test where a non-Postgres composite is resolved with a filter and assert the typed capability error is returned before `Retrieve` is invoked.

Add a fan-out copy test that uses `paramsWithTopK` and asserts the filter pointer and expression are preserved for every generated parameter.

- [ ] **Step 2: Run the service tests and confirm RED**

Run:

```bash
go test ./internal/application/service -run 'Test.*MetadataFilter|Test.*ParamsWithTopK' -count=1
```

Expected: FAIL because `RetrieveParams` has no field and the capability error/check do not exist.

- [ ] **Step 3: Add the retrieval field and typed capability error**

Add `MetadataFilter *MetadataFilter` to `types.RetrieveParams`. Add `ErrMetadataFilterUnsupported ErrorCode = 2202` after the existing vector-store errors and a constructor that maps to HTTP 400 without exposing a store UUID or backend internals.

In `HybridSearch`, validate the filter again for non-HTTP callers. In both vector and keyword `RetrieveParams` literals inside `buildRetrievalParams`, set `MetadataFilter: params.MetadataFilter`.

- [ ] **Step 4: Add the fail-closed store-group check**

After `resolveStoreGroups` resolves each engine, reject the whole search if `params.MetadataFilter != nil` and the composite reports unsupported. Do this before `retrieveFromStores` so a multi-KB request cannot return a mixture of filtered and unfiltered groups. Preserve the existing all-or-nothing fan-out error behavior.

- [ ] **Step 5: Run the service tests and confirm GREEN**

Run:

```bash
go test ./internal/application/service -run 'Test.*MetadataFilter|Test.*ParamsWithTopK' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit common propagation**

```bash
git add internal/types/retriever.go internal/application/service/knowledgebase_search.go internal/application/service/knowledgebase_search_storegroup.go internal/application/service/knowledgebase_search_fanout_test.go internal/application/service/knowledgebase_search_metadata_filter_test.go internal/errors/errors.go
git commit -m "feat: propagate metadata filter through retrieval"
```

### Task 4: Extract chunk access metadata and carry it into index records

**Files:**
- Create: `internal/types/chunk_access_metadata.go`
- Create: `internal/types/chunk_access_metadata_test.go`
- Modify: `internal/types/embedding.go:29-43`
- Modify: `internal/application/service/chunk.go:670-700`
- Modify: `internal/application/service/knowledge_process.go:523-540,1337-1360,1723-1745,2052-2075,2839-2868`
- Modify: `internal/application/service/image_multimodal.go:481-500`
- Modify: `internal/application/service/extract.go:735-755`
- Modify: `internal/application/service/knowledge_faq.go:2050-2120`
- Modify: `internal/application/service/knowledge_faq_import.go:1885-1950`
- Modify: `internal/application/service/retriever/keywords_vector_hybrid_indexer_test.go`

**Interfaces:**
- Consumes: `Chunk.Metadata` containing an optional reserved `access_metadata` object.
- Produces: `Chunk.AccessMetadata() (types.JSONMap, error)` and `IndexInfo.AccessMetadata`, including generated-question and FAQ index records.

- [ ] **Step 1: Write failing extraction and index-payload tests**

Add tests for:

```go
func TestChunkAccessMetadataExtractsReservedObject(t *testing.T) {
	chunk := &types.Chunk{Metadata: types.JSON(`{"access_metadata":{"department":"research","employee_nature":"formal"},"generated_questions":[]}`)}
	got, err := chunk.AccessMetadata()
	if err != nil || got["department"] != "research" {
		t.Fatalf("access metadata = %#v, err=%v", got, err)
	}
}
```

Also assert missing `access_metadata` returns an empty map, a non-object reserved value returns an error, unrelated generated-question fields are not copied, and `BatchIndex` forwards `IndexInfo.AccessMetadata` to the repository.

- [ ] **Step 2: Run the extraction/index tests and confirm RED**

Run:

```bash
go test ./internal/types ./internal/application/service/retriever -run 'Test.*AccessMetadata|Test.*Metadata' -count=1
```

Expected: FAIL because the extractor and `IndexInfo.AccessMetadata` do not exist.

- [ ] **Step 3: Implement reserved metadata extraction**

Add `Chunk.AccessMetadata()` that parses `Chunk.Metadata` as an object, reads only `access_metadata`, requires that value to be an object, and returns `types.JSONMap`. Return an empty map when the chunk has no metadata or no reserved key. Do not expose or index the rest of `Chunk.Metadata`.

Add `AccessMetadata types.JSONMap` to `IndexInfo`. Update every `IndexInfo` construction listed above to copy the source chunk's access metadata. Generated-question and image/FAQ derivatives inherit the originating chunk's access metadata. If extraction fails, fail the indexing operation instead of silently creating an unprotected index record.

- [ ] **Step 4: Run the extraction/index tests and confirm GREEN**

Run:

```bash
go test ./internal/types ./internal/application/service/retriever -run 'Test.*AccessMetadata|Test.*Metadata' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the index contract**

```bash
git add internal/types/chunk_access_metadata.go internal/types/chunk_access_metadata_test.go internal/types/embedding.go internal/application/service/chunk.go internal/application/service/knowledge_process.go internal/application/service/image_multimodal.go internal/application/service/extract.go internal/application/service/knowledge_faq.go internal/application/service/knowledge_faq_import.go internal/application/service/retriever/keywords_vector_hybrid_indexer_test.go
git commit -m "feat: carry chunk access metadata into indexes"
```

### Task 5: Add PostgreSQL access-metadata storage and copy preservation

**Files:**
- Create: `migrations/versioned/000079_chunk_access_metadata_index.up.sql`
- Create: `migrations/versioned/000079_chunk_access_metadata_index.down.sql`
- Modify: `internal/application/repository/retriever/postgres/structs.go:15-80`
- Modify: `internal/application/repository/retriever/postgres/repository.go:486-570`
- Create: `internal/application/repository/retriever/postgres/structs_test.go`

**Interfaces:**
- Consumes: `IndexInfo.AccessMetadata` from Task 4.
- Produces: PostgreSQL `embeddings.access_metadata` JSONB storage available to both BM25 and pgvector queries; copy operations preserve the payload.

- [ ] **Step 1: Write failing persistence tests**

Add a unit test for `toDBVectorEmbedding` that supplies `AccessMetadata: types.JSONMap{"department":"research"}` and asserts the mapped `pgVector.AccessMetadata` is equal. Add a copy test or focused helper assertion that a copied row keeps the source access metadata while changing only source/chunk/knowledge identifiers.

- [ ] **Step 2: Run the PostgreSQL unit tests and confirm RED**

Run:

```bash
go test ./internal/application/repository/retriever/postgres -run 'Test.*AccessMetadata|Test.*Copy' -count=1
```

Expected: FAIL because the model field and mapping do not exist.

- [ ] **Step 3: Add the versioned migration**

Create the `000079` up migration with idempotent statements:

```sql
ALTER TABLE embeddings
    ADD COLUMN IF NOT EXISTS access_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_embeddings_access_metadata
    ON embeddings USING GIN (access_metadata);
```

The down migration drops the index and column with `IF EXISTS`. Keep the migration compatible with the existing conditional embeddings setup.

- [ ] **Step 4: Persist and copy the field**

Add `AccessMetadata types.JSONMap` to both `pgVector` and `pgVectorWithScore`, map it in `toDBVectorEmbedding`, and include it in `CopyIndices` when constructing `targetVector`. Do not add it to `IndexWithScore` or API results; it is an internal retrieval predicate.

- [ ] **Step 5: Run the PostgreSQL unit tests and confirm GREEN**

Run:

```bash
go test ./internal/application/repository/retriever/postgres -run 'Test.*AccessMetadata|Test.*Copy' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit PostgreSQL metadata storage**

```bash
git add migrations/versioned/000079_chunk_access_metadata_index.up.sql migrations/versioned/000079_chunk_access_metadata_index.down.sql internal/application/repository/retriever/postgres/structs.go internal/application/repository/retriever/postgres/repository.go internal/application/repository/retriever/postgres/structs_test.go
git commit -m "feat: store chunk access metadata in postgres indexes"
```

### Task 6: Compile and apply the PostgreSQL JSONB filter before top-K

**Files:**
- Create: `internal/application/repository/retriever/postgres/metadata_filter.go`
- Create: `internal/application/repository/retriever/postgres/metadata_filter_test.go`
- Modify: `internal/application/repository/retriever/postgres/repository.go:165-235,266-430`

**Interfaces:**
- Consumes: validated `types.MetadataFilter` and `RetrieveParams.MetadataFilter`.
- Produces: parameterized SQL fragments for both GORM keyword queries and numbered-parameter pgvector queries.

- [ ] **Step 1: Write failing compiler tests**

Test that the compiler produces parenthesized predicates for nested `and`/`or`, binds field names and values as arguments, never interpolates a field string into SQL, and supports scalar equality, scalar membership, and stored-array intersection. Include a field such as `department'); DROP TABLE embeddings; --` and assert it is passed as a bound value or rejected by validation, never present as executable SQL.

Use `sqlmock` for repository-level tests. Assert that keyword and vector queries include the compiled predicate before their `LIMIT` arguments and that the argument order remains stable after existing vector, dimension, KB, knowledge, tag, and enabled arguments.

- [ ] **Step 2: Run the PostgreSQL filter tests and confirm RED**

Run:

```bash
go test ./internal/application/repository/retriever/postgres -run 'Test.*MetadataFilter' -count=1
```

Expected: FAIL because the compiler and query integration do not exist.

- [ ] **Step 3: Implement the parameterized compiler**

Implement a small binder abstraction that supports both `?` placeholders for GORM clauses and `$N` placeholders for the raw vector SQL. Compile each leaf as JSONB containment against `access_metadata`, using a JSON-encoded scalar and an array-containing alternative for stored array values. Compile `in` as an OR of the same field equality predicate, and recursively wrap child groups in parentheses.

The compiler must accept only the operators already validated by `MetadataFilter.Validate`; it must return an error for any unexpected operator rather than falling through to an unfiltered query.

- [ ] **Step 4: Add the predicate to keyword retrieval**

In `KeywordsRetrieve`, append the compiled expression to the existing `conds` before the ParadeDB content predicate and `Limit`. Keep existing KB/knowledge/tag/enabled filters unchanged and use GORM `clause.Expr` so values remain bound parameters.

- [ ] **Step 5: Add the predicate to vector retrieval**

In `VectorRetrieve`, compile the filter after the existing base variables are appended, append its SQL to `whereParts`, and append its bound values to `allVars` before the subquery limit/threshold/final-limit parameters are assigned. Keep the HNSW ORDER BY expression unchanged so the existing index plan and iterative-scan behavior remain intact.

- [ ] **Step 6: Run the PostgreSQL filter tests and confirm GREEN**

Run:

```bash
go test ./internal/application/repository/retriever/postgres -run 'Test.*MetadataFilter' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit pre-top-K filtering**

```bash
git add internal/application/repository/retriever/postgres/metadata_filter.go internal/application/repository/retriever/postgres/metadata_filter_test.go internal/application/repository/retriever/postgres/repository.go
git commit -m "feat: filter postgres retrieval by chunk metadata"
```

### Task 7: Add composite capability detection and context-enrichment defense

**Files:**
- Modify: `internal/application/service/retriever/composite.go:80-105`
- Modify: `internal/application/service/knowledgebase_search_results.go:14-90,127-270`
- Modify: `internal/application/service/knowledgebase_search.go:245-260`
- Create: `internal/application/service/knowledgebase_search_results_metadata_filter_test.go`
- Create: `internal/application/service/retriever/composite_metadata_filter_test.go`

**Interfaces:**
- Consumes: `MetadataFilter.Matches`, `SearchParams.MetadataFilter`, and `CompositeRetrieveEngine.SupportsMetadataFilter` from earlier tasks.
- Produces: no filtered-out primary, parent, nearby, relation, or second-level parent chunk can enter the API response.

- [ ] **Step 1: Write failing context and capability tests**

Add tests for:

- a composite containing only PostgreSQL reports support;
- a composite containing a non-PostgreSQL engine reports unsupported;
- a primary allowed chunk with a neighboring disallowed chunk returns only the primary chunk;
- a disallowed parent/relation/second-level parent is not added to `chunkMap` or final results;
- `skip_context_enrichment=true` still filters primary chunks.

- [ ] **Step 2: Run the focused tests and confirm RED**

Run:

```bash
go test ./internal/application/service/retriever ./internal/application/service -run 'Test.*MetadataFilter|Test.*Enrichment' -count=1
```

Expected: FAIL because capability reporting and filter-aware result processing do not exist.

- [ ] **Step 3: Implement capability reporting**

Add `SupportsMetadataFilter` to `CompositeRetrieveEngine`. Iterate all active engine members and return false for any engine type other than PostgreSQL. In the store-group resolution path, use the typed unsupported error from Task 3 before any retrieval call.

- [ ] **Step 4: Make result processing filter-aware**

Change `processSearchResults` to accept `metadataFilter *types.MetadataFilter`. Fetch the primary chunk rows needed to resolve retrieval IDs, remove chunks whose `AccessMetadata` does not match, rebuild the chunk index from the allowed primary set, and only then assemble knowledge/context data. Apply the same check to every batch of enrichment chunks before adding it to `chunkMap`; retain existing enabled/index-status/searchable checks.

Pass `params.MetadataFilter` from `HybridSearch` into `processSearchResults`. Do not include access metadata in `SearchResult` or logs.

- [ ] **Step 5: Run the focused tests and confirm GREEN**

Run:

```bash
go test ./internal/application/service/retriever ./internal/application/service -run 'Test.*MetadataFilter|Test.*Enrichment' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit response safety**

```bash
git add internal/application/service/retriever/composite.go internal/application/service/knowledgebase_search_results.go internal/application/service/knowledgebase_search.go internal/application/service/knowledgebase_search_results_metadata_filter_test.go internal/application/service/retriever/composite_metadata_filter_test.go
git commit -m "feat: enforce metadata filter during result enrichment"
```

### Task 8: Document support, regenerate Swagger, and run the full verification gate

**Files:**
- Modify: `website-docs/03-features/05-retrieval-engines.md`
- Modify: `docs/swagger.json`
- Modify: `docs/swagger.yaml`
- Modify: `docs/docs.go`

**Interfaces:**
- Consumes: The final API/type/error behavior from Tasks 1–7.
- Produces: User-facing API documentation, backend support matrix, migration/backfill warning, and generated Swagger matching the code.

- [ ] **Step 1: Add the support matrix and operational warning**

Document:

- reserved chunk metadata shape under `access_metadata`;
- PostgreSQL is the first supported backend;
- unsupported stores return `metadata_filter_unsupported` and do not return unfiltered results;
- existing rows need metadata backfill/reindex before filtered authorization use;
- this request field does not derive identity or prevent a caller from omitting a filter.

- [ ] **Step 2: Regenerate Swagger and inspect the diff**

Run:

```bash
make docs
git diff -- docs/swagger.json docs/swagger.yaml docs/docs.go
```

Expected: the generated schema for `SearchParams` includes `metadata_filter`; no unrelated endpoint schema changes are accepted without review.

- [ ] **Step 3: Run focused package tests**

Run:

```bash
go test ./internal/types ./internal/handler ./internal/application/service ./internal/application/service/retriever ./internal/application/repository/retriever/postgres -count=1
```

Expected: PASS.

- [ ] **Step 4: Run the repository verification gate**

Run:

```bash
go test ./...
git diff --check origin/main...HEAD
golangci-lint run --new-from-rev=origin/main ./...
git status --short --branch
```

Expected: tests and diff-scoped lint pass; any environment-only failure is recorded separately in the PR description; the worktree contains only the intended implementation, migration, docs, generated Swagger, and tests.

- [ ] **Step 5: Commit documentation and generated API artifacts**

```bash
git add website-docs/03-features/05-retrieval-engines.md docs/swagger.json docs/swagger.yaml docs/docs.go
git commit -m "docs: document chunk metadata filtering"
```

## Plan self-review checklist

- The approved spec's boolean AST is covered by Tasks 1–2 and the nested-policy tests.
- Pre-top-K vector and keyword enforcement is covered by Tasks 5–6.
- Fail-closed unsupported stores and multi-KB behavior are covered by Task 3 and Task 7.
- Parent/nearby/relation/second-level enrichment is covered by Task 7.
- Existing no-filter behavior and old indexed rows are covered by Tasks 1, 4, 7, and 8.
- REST docs and generated Swagger are covered by Tasks 2 and 8.
- Identity resolution and metadata write APIs are explicitly excluded by the approved spec and documented as follow-up work.
- No step relies on raw SQL input, unbounded filter depth, silent unsupported behavior, or post-top-K authorization filtering.
