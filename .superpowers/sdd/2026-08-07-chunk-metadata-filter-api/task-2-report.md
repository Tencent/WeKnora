# Task 2 Report: REST `metadata_filter` boundary

## Status

Implemented on branch `codex/chunk-metadata-filter-api`. The REST handler now validates a bound `metadata_filter` after JSON binding, returns the existing validation-style error for malformed filters, and passes valid filters unchanged through `types.SearchParams.MetadataFilter`. Omitted or `null` filters retain existing behavior.

## Files changed

- `internal/handler/knowledgebase.go`: validate the optional filter before query validation and before calling the service.
- `internal/handler/knowledgebase_hybrid_search_test.go`: nested-filter propagation and malformed-filter rejection/service-not-called tests.
- `website-docs/04-api/02-api-knowledge.md`: request field table, recursive `or`/`and` example, document/chunk narrowing, identity boundary, reserved metadata source, and write-support boundary.

## TDD evidence

RED test command:

```text
go test ./internal/handler -run 'TestHybridSearch.*MetadataFilter' -count=1
```

Output:

```text
--- FAIL: TestHybridSearchRejectsMalformedMetadataFilterBeforeService (0.00s)
    knowledgebase_hybrid_search_test.go:129: expected 400, got 200 body={"data":[],"success":true}
FAIL
FAIL    github.com/Tencent/WeKnora/internal/handler  1.750s
```

The failure was the intended missing-boundary behavior: the malformed request returned 200 and reached the capture service.

GREEN focused command:

```text
go test ./internal/handler -run 'TestHybridSearch' -count=1
```

Output:

```text
ok    github.com/Tencent/WeKnora/internal/handler  1.051s
```

## Verification

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
ok    github.com/Tencent/WeKnora/internal/types  0.699s

go test ./internal/handler -run 'TestHybridSearch.*MetadataFilter' -count=1
ok    github.com/Tencent/WeKnora/internal/handler  1.130s

go test ./internal/handler -count=1
ok    github.com/Tencent/WeKnora/internal/handler  0.862s

git diff --check
clean; no output
```

## Self-review

- Validation is after `ShouldBindJSON` and before the existing query-text/precomputed-vector checks and service call.
- Valid nested `or`/`and` structure is asserted at the service capture boundary; no retrieval, repository, or storage code was changed.
- Invalid operator returns HTTP 400 with `ErrValidation` (`1010`) and zero service calls.
- Existing POST flow and GET compatibility route behavior were not altered; this test router remains POST-only like the existing focused tests.
- Documentation does not imply identity resolution or a metadata write API; it distinguishes document-level `knowledge_ids` narrowing from chunk-level access-metadata filtering.

## Concerns / out of scope

- This task verifies REST binding and validation only. Live retrieval-engine enforcement and population/backfill of reserved `access_metadata` are outside Task 2 and were not re-tested here.
- The report is included in the focused task commit.
