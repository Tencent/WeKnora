# PR #1977 verification evidence

This folder contains PR-local visual evidence for the final validation pass of
`codex/issue-1679-content-cache`.

## Assets

- `status.png` — final status snapshot for the audited implementation commit.
- `verification-matrix.png` — command/result matrix for the validation run.
- `cache-flow.gif` — short visual summary of the cache hit/miss and invalidation flow.
- `cache-flow-final.png` — final frame of the GIF for static renderers.

## Validation environment

All Go build/test cache, module cache, temporary files, and CGO artifacts were kept
on `D:\agent` because the default `C:` drive was space-constrained.

Key local settings used for the successful validation pass:

- Go: `D:\agent\tools\go`
- GCC: `D:\agent\tools\winlibs-gcc-16.1.0-ucrt\bin`
- GOPATH: `D:\agent\go-1977`
- GOMODCACHE: `D:\agent\cache\gomod-1977`
- GOCACHE: `D:\agent\cache\go-build-1977-ucrt`
- GOTELEMETRYDIR: `D:\agent\cache\go-telemetry-1977`
- TEMP/TMP/GOTMPDIR: `D:\agent\temp`
- CGO: `CGO_ENABLED=1`, `CC=gcc`, `CXX=g++`
- SQLite headers: `D:\agent\tools\sqlite-headers-1977`

## Commands represented by the evidence

- `go test ./internal/application/service/...`
- `go test ./internal/models/... ./internal/contentcache ./internal/types`
- `go test -race ./internal/models/embedding ./internal/contentcache`
- `go test -race ./internal/application/service -run "TestReparseKnowledgeContentCache|TestReparseKnowledgeCache|TestEmbeddingCache"`
- `go test ./cmd/server`
- `go test ./internal/container`
- `go test ./internal/application/repository/retriever/sqlite`
- `go test ./internal/agent/tools`
- `git diff --check`

The wider `go test ./internal/...` sweep was attempted after the D-drive CGO fix
but exceeded the local execution window. The remaining isolated failures were in
pre-existing Windows/local-environment paths outside the PR #1977 content-cache
production path: Feishu/Notion connector fixtures and SSRF-localhost behavior,
docparser localhost-image SSRF behavior, and sandbox Python execution on Windows.
