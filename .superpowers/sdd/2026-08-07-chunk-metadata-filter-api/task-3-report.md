# Task 3 report: propagate metadata filter through retrieval

## Status

DONE. Common retrieval types and service plumbing now carry one validated metadata-filter pointer through every vector and keyword retrieval parameter. A non-nil filter fails closed with typed error `2202` before any store-group retrieval starts when the resolved composite cannot report metadata-filter support.

## RED evidence

Command (the local default Go proxy timed out, so this command used the reachable per-process proxy without changing repository or global configuration):

```bash
GOTELEMETRY=off GOPROXY=https://goproxy.cn,direct go test ./internal/application/service -run 'Test.*MetadataFilter|Test.*ParamsWithTopK' -count=1
```

Result: `FAIL [build failed]` as expected. The new tests reported `unknown field MetadataFilter in struct literal of type types.RetrieveParams`, `RetrieveParams has no field or method MetadataFilter`, and undefined `apperrors.ErrMetadataFilterUnsupported`.

## GREEN evidence

Focused command:

```bash
GOTELEMETRY=off GOPROXY=https://goproxy.cn,direct go test ./internal/application/service -run 'Test.*MetadataFilter|Test.*ParamsWithTopK' -count=1
```

Result: PASS (`ok github.com/Tencent/WeKnora/internal/application/service 1.357s`).

Relevant package command:

```bash
GOTELEMETRY=off GOPROXY=https://goproxy.cn,direct go test ./internal/application/service ./internal/types ./internal/errors -count=1
```

Result: PASS (`service 2.976s`, `types 1.410s`; `errors` has no test files).

## Files

- `internal/types/retriever.go`: adds `RetrieveParams.MetadataFilter`.
- `internal/errors/errors.go`: adds generic HTTP-400 `ErrMetadataFilterUnsupported` (`2202`) without store or backend detail.
- `internal/application/service/knowledgebase_search.go`: revalidates non-HTTP inputs and copies the original pointer to vector and keyword params.
- `internal/application/service/knowledgebase_search_storegroup.go`: checks the future `SupportsMetadataFilter() bool` composite capability by narrow interface assertion; missing or unknown capability is unsupported.
- `internal/application/service/knowledgebase_search_fanout_test.go`: verifies fan-out copies retain the identical filter pointer and expression.
- `internal/application/service/knowledgebase_search_metadata_filter_test.go`: verifies vector/keyword propagation and multi-KB fail-closed behavior with zero retrieval calls.

## Self-review

- The filter is checked after each composite resolution and before `retrieveFromStores`, so no multi-KB search can mix filtered and unfiltered results.
- Omitted filters preserve the pre-existing path; the all-or-nothing retrieve fan-out implementation is untouched.
- The public 2202 message has no store UUID, engine type, endpoint, or backend error detail.
- Scope contains no PostgreSQL SQL, chunk access-metadata extraction, or response-enrichment filtering.

## Concerns

- `CompositeRetrieveEngine.SupportsMetadataFilter` is intentionally supplied by Task 7. Until then, this task's narrow interface assertion returns false and rejects every filtered scope, which is the required fail-closed behavior. Task 7 must implement the fixed PostgreSQL-only capability contract.
