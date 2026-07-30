# PR #1977 verification evidence

Final HEAD: `HEAD (this commit)`

Latest official main: `f7ef782d585f149613b0e443e8575689bb2d3ab9`

Merge-base after merge: `f7ef782d585f149613b0e443e8575689bb2d3ab9`

Branch: `codex/issue-1679-content-cache`

## Assets

- `status.png` - final status snapshot for the current implementation.
- `verification-matrix.png` - current command/result matrix for this validation pass.
- `cache-flow.gif` - visual summary of cache reuse, invalidation, stale cleanup, and attempt fences.
- `cache-flow-final.png` - final GIF frame for static renderers.

## Validation environment

All Go build/test cache, module cache, temporary files, and CGO artifacts were kept on `D:\agent`.

- Go: `D:\agent\tools\go`
- GCC: `D:\agent\tools\winlibs-gcc-16.1.0-ucrt\bin`
- GOPATH: `D:\agent\go-1977`
- GOMODCACHE: `D:\agent\cache\gomod-1977`
- GOCACHE: `D:\agent\cache\go-build-1977-ucrt`
- GOTELEMETRYDIR: `D:\agent\cache\go-telemetry-1977`
- TEMP/TMP/GOTMPDIR: `D:\agent\temp`
- CGO: `CGO_ENABLED=1`, `CC=gcc`, `CXX=g++`
- SQLite headers: `D:\agent\tools\sqlite-headers-1977`

## Current verification

- `go test ./internal/contentcache ./internal/application/repository/retriever/sqlite ./internal/application/repository/retriever/postgres -run 'Test(StableChunkID|CacheKeys|SQLite|Postgres)' -count=1` - PASS
- `go test ./internal/application/service -run 'Test(NextStableChunkID|MultimodalPendingKey|StableGeneratedQuestionID|GraphExtractCache|WikiMapCache|UpsertStableChunks|CollectReparse|DeleteReparse|KnowledgePostProcessSkipsSupersededAttempt|EnqueueImageMultimodalTasks|ImageMultimodalFinalizeFallbacks)' -count=1` - PASS
- `go test ./internal/models/embedding -run 'TestCachedEmbedder(ReusesRedisCacheAcrossWrapperRebuild|InvalidatesOnConcreteModelMetadata|SingleflightsConcurrentMissSet|SingleflightsConcurrentBatchMissSet)' -count=1` - PASS
- `go test ./internal/contentcache ./internal/models/embedding ./internal/application/repository/retriever/sqlite ./internal/application/repository/retriever/postgres ./internal/application/service -run '^$' -count=1` - PASS
- `git diff --check` - PASS
- `git diff --cached --check` - PASS

## Not run

Per task scope, known unrelated Feishu fixture, Notion/docparser localhost SSRF, and Windows sandbox/Python failure paths were not rerun.
