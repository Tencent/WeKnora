# Task 1 report: typed metadata filter AST and pure evaluator

## Status

DONE. The task adds the typed recursive metadata-filter contract and a pure evaluator for `types.JSONMap`. No metadata-write API or identity-authorization behavior was added.

## Implementation

- Added `MetadataFilterOperator` with the fixed `eq` and `in` values.
- Added `MetadataFilter` with the fixed `and`, `or`, `field`, `op`, `value`, and `values` JSON shape.
- Added `(*MetadataFilter).Validate()` with exact group/predicate node validation, scalar-only values, trimmed/control-safe field validation, empty-group/list rejection, and depth/node limits of 8/64.
- Added `(*MetadataFilter).Matches()` with fail-closed missing-field behavior, boolean `and`/`or`, scalar equality/membership, stored-array element equality, and stored/requested-array intersection.
- Added `SearchParams.MetadataFilter *MetadataFilter` with `json:"metadata_filter,omitempty"`; existing defaults were not changed.

## Files

- `internal/types/metadata_filter.go`
- `internal/types/metadata_filter_test.go`
- `internal/types/search.go`
- `.superpowers/sdd/2026-08-07-chunk-metadata-filter-api/task-1-report.md`

## RED evidence

After adding the tests but before adding production code:

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
...
undefined: MetadataFilter
undefined: MetadataFilterOpEqual
FAIL
```

The failure was the expected missing-public-contract failure.

## GREEN evidence

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.655s

go test ./internal/types -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.605s

git diff --check
```

The tests cover the required nested policy, malformed mixed nodes, empty groups and `in`, operator/value-shape errors, invalid fields, depth and node limits, JSON scalar decoding, scalar/array matching, missing fields, and the correlated OR-of-AND anti-overgrant case.

## Self-review

- Validation is recursive and counts every group and predicate node before descending.
- A node cannot combine a group with predicate fields, and empty groups cannot become vacuous matches.
- `Matches` revalidates before evaluation, so malformed values fail closed even when called directly.
- Numeric JSON values are compared by numeric value, including `json.Number` and the default `float64` decoder representation.
- The filter is only a narrowing retrieval expression; no caller identity, ACL derivation, or write path was introduced.

## Concerns

No known concerns for Task 1. Retrieval propagation, backend compilation/capability checks, and context-enrichment enforcement remain intentionally deferred to later tasks.

## Review fixes

### Findings addressed

- Changed `MetadataFilter.Value` to `json:"value"` so equality filters explicitly preserve `false`, `0`, and `""` in the public JSON contract. Added round-trip regression coverage.
- Added custom JSON unmarshalling presence tracking for `and` and `or`. Explicit `null` group fields now remain distinguishable from omitted fields and are rejected, including malformed mixed nodes.
- Tightened numeric scalar validation to reject malformed `json.Number` values, NaN, and positive/negative infinity for both filter values and membership lists.

### Fix RED evidence

After adding the regression tests and before the fix:

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
--- FAIL: TestMetadataFilterValidateRejectsExplicitNullAndMixedGroups
    malformed explicit group accepted: {"and":null,"field":"x","op":"eq","value":true}
--- FAIL: TestMetadataFilterValidateRejectsInvalidJSONNumbers
    invalid JSON number "not-a-number" was accepted
FAIL
```

The zero-value JSON regression test was already exposing the desired behavior with the prior interface-typed field, but it remains as an explicit guard for the fixed contract.

### Fix GREEN evidence

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.725s

go test ./internal/types -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.618s

git diff --check
```

The fixes are limited to Task 1 AST JSON/validation behavior and regression tests. The existing Task 1 commit was not rewritten.

## Review round 1 follow-up fix

### Finding addressed

Added strict JSON-number lexical validation for `json.Number` before the existing `Float64()` parsing and finiteness checks. The accepted grammar is equivalent to `-?(0|[1-9][0-9]*)(\\.[0-9]+)?([eE][+-]?[0-9]+)?`, so leading zeroes, incomplete decimals, and surrounding whitespace are rejected while decimal and exponent forms remain valid. Existing scalar equality and membership semantics are unchanged.

### Follow-up RED evidence

After adding regression tests and before the production fix:

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
--- FAIL: TestMetadataFilterValidateRejectsInvalidJSONNumbers
    invalid JSON number "01" was accepted
FAIL
```

### Follow-up GREEN evidence

```text
go test ./internal/types -run 'TestMetadataFilter' -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.666s

go test ./internal/types -count=1
ok   github.com/Tencent/WeKnora/internal/types  0.628s

git diff --check
```

Regression coverage includes malformed `01`, `1.`, whitespace-wrapped numbers, malformed/non-finite values, and valid `0`, `-0`, decimal, and exponent forms.
