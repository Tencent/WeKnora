# Final whole-branch review fix report

Date: 2026-08-07
Branch: `codex/chunk-metadata-filter-api`
Starting HEAD: `5d1c02dc58d11910508be35f9adb347ce3c7ba00`

## Scope and outcome

This pass addresses the six Critical/Important findings from the final whole-branch review without resetting or rewriting existing commits. No push or merge was performed.

### 1. Summary isolation

- Filtered search now excludes `ChunkTypeSummary` in the defense-in-depth chunk filter, including primary and enrichment batches.
- Filtered search clears `SearchResult.KnowledgeDescription`; unfiltered behavior is unchanged.
- Initial summary chunk creation computes one common reserved `access_metadata` object across all contributing text chunks. It persists that object only when every chunk agrees.
- Heterogeneous or absent source access metadata produces no summary metadata instead of inheriting `textChunks[0]`.
- Summary refresh applies the same consensus rule to existing summary chunks before persistence and reindexing, clearing stale metadata when sources differ.
- Regression tests cover initial homogeneous/heterogeneous summary metadata, refresh synchronization/cleanup, summary search exclusion, and response description omission.

Security choice: a summary is document-wide content, so ambiguous or heterogeneous ACL metadata is treated as unsafe. Filtered search never returns summary content even when a legacy summary row happens to carry matching metadata.

### 2. Exact numeric comparison

- `MetadataFilter.UnmarshalJSON` and `Chunk.AccessMetadata` decode numbers with `json.Decoder.UseNumber`.
- ACL number comparison no longer converts through `float64`.
- JSON numbers are normalized as sign, significant digits, and an arbitrary-precision decimal exponent, preserving exact equality without allocating huge powers of ten.
- Regression coverage proves `9007199254740992` and `9007199254740993` do not compare equal while an identical integer above `2^53` still matches.

### 3. Strict AST JSON contract

- Custom decoding rejects unknown fields and duplicate keys.
- All six node fields track presence, so group nodes cannot mix `field`, `op`, `value`, or `values`, including explicit `null`.
- Explicit `null` is rejected for every AST field; leaf required fields must be present and valid.
- Scalar `false`, `0`, and `""` remain valid and round-trip correctly.
- Branch-aware `MarshalJSON` emits only group fields for group nodes and does not add `value:null`.

### 4. `in.values` resource limits

- Maximum values in one `in` leaf: 64.
- Maximum JSON encoding size of one scalar: 4,096 bytes.
- Maximum sum of encoded scalar sizes in one leaf: 16,384 bytes.
- Tests cover accepted exact boundaries and rejected count/per-value/total overflow.
- The exact limits and strict JSON rules are documented in the design and REST API documentation.

### 5. PostgreSQL keyword SQL logging

- Removed `.Debug()` from `pgRepository.KeywordsRetrieve`.
- Existing structured operational logs remain.
- A GORM logger-spy regression test proves keyword retrieval does not force SQL info/debug mode, including a bound metadata filter.

### 6. Reserved access metadata on nil setters

- `Chunk.SetDocumentMetadata(nil)` and `Chunk.SetFAQMetadata(nil)` now remove their owned metadata while retaining a valid reserved `access_metadata` object.
- When no reserved access object exists, metadata is cleared to nil.
- FAQ cleanup still clears `ContentHash`.
- Regression tests cover both reserved and unreserved cases.

## TDD evidence

Tests were added before production changes and observed failing for the intended reasons:

- Types RED: unknown AST fields decoded successfully, group JSON emitted `value:null`, an oversized `in.values` list validated, adjacent integers above `2^53` compared equal, and nil metadata setters erased `access_metadata`.
- Service RED: filtered results returned `KnowledgeDescription` and `ChunkTypeSummary`; summary creation accepted only one source chunk and no refresh metadata synchronization helper existed.
- PostgreSQL RED: the logger spy observed `.Debug()` force one info/debug log-mode request.

After the minimal production changes, the focused RED set passed.

## Files changed

- `internal/types/metadata_filter.go`
- `internal/types/metadata_filter_test.go`
- `internal/types/chunk_access_metadata.go`
- `internal/types/chunk_access_metadata_test.go`
- `internal/types/faq.go`
- `internal/application/service/knowledge_process.go`
- `internal/application/service/knowledge_summary_test.go`
- `internal/application/service/knowledgebase_search_results.go`
- `internal/application/service/knowledgebase_search_results_metadata_filter_test.go`
- `internal/application/repository/retriever/postgres/repository.go`
- `internal/application/repository/retriever/postgres/metadata_filter_test.go`
- `docs/superpowers/specs/2026-08-07-chunk-metadata-filter-api-design.md`
- `website-docs/04-api/02-api-knowledge.md`
- `.superpowers/sdd/2026-08-07-chunk-metadata-filter-api/final-review-fix-report.md`

Swagger artifacts were not regenerated because the public Go field/schema and handler annotations did not change; only validation semantics and prose constraints changed.

## Verification

Focused regression command:

```text
GOTELEMETRY=off go test ./internal/types ./internal/application/service ./internal/application/repository/retriever/postgres ./internal/handler -run 'TestMetadataFilter|TestChunkAccessMetadata|TestChunkMetadataSetters|TestInitialSummaryChunk|TestRefreshSummaryChunk|TestProcessSearchResultsMetadataFilter|TestCompileMetadataFilter|TestKeywordsRetrieve|TestVectorRetrieve|TestHybridSearch.*MetadataFilter' -count=1
ok  github.com/Tencent/WeKnora/internal/types
ok  github.com/Tencent/WeKnora/internal/application/service
ok  github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres
ok  github.com/Tencent/WeKnora/internal/handler
```

Complete related-package tests:

```text
GOTELEMETRY=off go test ./internal/types ./internal/application/service ./internal/application/repository/retriever/postgres ./internal/handler -count=1
ok  github.com/Tencent/WeKnora/internal/types
ok  github.com/Tencent/WeKnora/internal/application/service
ok  github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres
ok  github.com/Tencent/WeKnora/internal/handler
```

Static verification:

```text
GOTELEMETRY=off go vet ./...
# exit 0, no output

git diff --check
# exit 0, no output
```

## Unverified external boundaries

- No live PostgreSQL/ParadeDB/pgvector instance was used. SQL execution shape is covered with `sqlmock`; JSONB numeric behavior relies on PostgreSQL's exact JSONB numeric semantics.
- No live summary model, Asynq worker, or retrieval-engine reindex was run. Initial/refresh metadata decisions and the refresh persistence path are covered by Go tests and related-package regression tests.
- No deployed HTTP environment or protected-data acceptance test was run; handler binding and filtered response behavior are local Go tests.

---

## Final narrow follow-up at `205d55d4`

Starting HEAD: `205d55d484faa7aaae7e2c854e53666f613dfa46`

This follow-up addresses the independently confirmed remaining one Critical and two Important findings. It does not reset or rewrite earlier commits, and no push or merge is performed.

### 1. Filtered image enrichment isolation

- `processSearchResults` no longer calls child-image enrichment when `metadataFilter` is present.
- This prevents URL, OCR text, or caption data from image OCR/caption child chunks from bypassing the chunk metadata predicate.
- Unfiltered search keeps the existing enrichment behavior.
- The regression repository now returns a real restricted image child with URL, OCR, and caption payload. Before the fix, that payload appeared in `SearchResult.ImageInfo`; after the fix, the filtered response remains empty.
- The design and REST API documentation state that filtered search does not backfill `image_info` from image OCR/caption child chunks.

### 2. Exact `JSONMap.Scan` numbers

- `JSONMap.Scan` now uses the shared `json.Decoder.UseNumber` path instead of ordinary `json.Unmarshal`.
- The scanner recursively preserves JSON numbers, including integers above `2^53`, as `json.Number` until `Value` marshals them back to JSON.
- Regression coverage scans both real-driver-style `[]byte` and sqlmock-style `string` values and proves `9007199254740993` and a nested `9007199254740995` marshal without rounding.
- Related repository tests include the knowledge-span JSONB fields (`input`, `output`, and `metadata`) that use the same scanner, in addition to PostgreSQL retriever `access_metadata` usage and the CopyIndices mapping tests.

### 3. Setter-owned metadata cleanup

- `SetDocumentMetadata(nil)` removes only `generated_questions` and `generated_questions_revision`.
- `SetFAQMetadata(nil)` removes only the known `FAQChunkMetadata` keys.
- Both setters preserve valid reserved `access_metadata` and unrelated extension keys such as `label`; metadata becomes nil only when no keys remain.
- FAQ cleanup still clears `ContentHash`.
- Existing nil-setter fixtures now use fields owned by the setter under test, so they no longer require the FAQ setter to delete document metadata.

### TDD evidence

The three new regressions were added and observed RED before production changes:

- Service RED: the filtered result contained the restricted child image URL, caption, and OCR payload.
- JSONMap RED: `9007199254740993` scanned as the rounded `9.007199254740992e+15` float.
- Setter RED: both document and FAQ nil setters removed the unrelated `label` field.

After the minimal production changes, the focused regressions passed.

### Files changed in this follow-up

- `internal/application/service/knowledgebase_search_results.go`
- `internal/application/service/knowledgebase_search_results_metadata_filter_test.go`
- `internal/types/json_map.go`
- `internal/types/json_map_test.go`
- `internal/types/chunk_access_metadata.go`
- `internal/types/chunk_access_metadata_test.go`
- `internal/types/faq.go`
- `docs/superpowers/specs/2026-08-07-chunk-metadata-filter-api-design.md`
- `website-docs/04-api/02-api-knowledge.md`
- `.superpowers/sdd/2026-08-07-chunk-metadata-filter-api/final-review-fix-report.md`

### Verification

Focused regressions:

```text
GOTELEMETRY=off go test ./internal/application/service -run 'TestProcessSearchResultsMetadataFilterDoesNotEnrichImageInfoFromChildChunks' -count=1
ok  github.com/Tencent/WeKnora/internal/application/service

GOTELEMETRY=off go test ./internal/types -run 'TestJSONMapScanValuePreservesExactNumbers|TestChunkMetadataSettersNil' -count=1
ok  github.com/Tencent/WeKnora/internal/types
```

Complete related-package tests:

```text
GOTELEMETRY=off go test ./internal/types ./internal/application/service ./internal/application/repository ./internal/application/repository/retriever/postgres ./internal/handler -count=1
ok  github.com/Tencent/WeKnora/internal/types
ok  github.com/Tencent/WeKnora/internal/application/service
ok  github.com/Tencent/WeKnora/internal/application/repository
ok  github.com/Tencent/WeKnora/internal/application/repository/retriever/postgres
ok  github.com/Tencent/WeKnora/internal/handler
```

Static verification:

```text
GOTELEMETRY=off go vet ./...
# exit 0, no output

git diff --check
# exit 0, no output
```

Additional full-suite check:

```text
GOTELEMETRY=off go test ./... -count=1
# FAILED only in internal/datasource/connector/feishu/wiki:
# TestFetchAll_LogsSummaryWithSkipBreakdown captured repeated "json" lines instead of the expected summary text.

GOTELEMETRY=off go test ./internal/datasource/connector/feishu/wiki \
  -run '^TestFetchAll_LogsSummaryWithSkipBreakdown$' -count=1 -v
# FAILED with the same pre-existing log-capture mismatch.
```

No files in that package or its logger path are changed by this follow-up. The failure is recorded rather than expanded into this narrow security fix.

### Unverified external boundaries for this follow-up

- No live PostgreSQL/ParadeDB/pgvector instance was used. Exact scanner/value round-tripping and CopyIndices preservation are local Go tests; PostgreSQL JSONB driver behavior is not re-exercised against a live server.
- No deployed hybrid-search endpoint or protected-data acceptance environment was used. The image-enrichment regression executes the real service and enrichment code with an in-memory repository double that returns a restricted child chunk.
- Agent/session QA and chat-pipeline enrichment remain outside this REST hybrid-search change's documented scope and were not changed.
