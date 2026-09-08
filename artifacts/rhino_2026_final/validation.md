# Deterministic Validation

## Go

Command:

```text
env GOCACHE=/tmp/weknora-go-cache go test -p 1 -count=1 ./internal/application/service ./internal/regression ./internal/models/embedding ./internal/models/chat ./internal/application/repository ./internal/handler -run 'TestBenchmarkV1Integrity|TestWikiPromptCache|TestWikiPromptPurposeMapping|TestCompareRegression|TestEmbeddingCacheDisabledAndNilRedisPreserveProvider|TestEmbeddingCacheStatsSharedAcrossDecorators|TestBuildOutbound_WikiExplicitPromptCacheKey|TestModelUsageAnalytics|TestAggregateAnalytics'
```

Result: PASS in all six packages.

## Frontend analytics contract

Command:

```text
npm test -- src/api/modelUsageAnalytics.test.ts src/views/settings/components/modelUsageAnalyticsHelpers.test.ts
```

Result: PASS, 14/14 tests.

## Artifact and Git checks

- every JSON below `artifacts/rhino_2026_final/` parses successfully
- secret-pattern scan found no API key, Authorization header, or `api_key` field
- `git diff --check`: PASS
- production code changed during acceptance: NO
