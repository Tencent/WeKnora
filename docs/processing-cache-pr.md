# feat(cache): reuse knowledge processing artifacts across reparses

## Description

重解析会重复调用解析器、VLM 和文本模型。本 PR 为知识处理增加持久化内容缓存，使输入和有效配置相同的重解析、任务续跑复用已完成产物。

- 覆盖文件解析、图片预处理、OCR/caption/observation、文本与图片向量、摘要、问题、Graph 抽取和 Wiki map；文本向量支持同租户跨文档复用。
- 缓存键包含实际输入、模型或解析器身份、提示和配置。主动重新生成问题或摘要绕过缓存，Wiki reduce 和页面协调继续读取当前状态。
- 保留随机 chunk UUID；命中后绑定当前 UUID、图片地址与 Wiki 引用。仍按现有流程重写 chunk 和索引，不引入差异索引或原子发布。Wiki 活页面候选变化可使相关 map 阶段失效。
- 默认关闭，存储故障回退计算。提供 TTL、大小限制、并发处理、租户隔离及文档物理删除级联清理。
- 新增 PostgreSQL `000137_processing_cache`、SQLite `000053_processing_cache` 及回滚脚本。远程解析缓存需要显式配置 `DOCREADER_CACHE_VERSION`。

HTML 图片归一化保留 title、alt 和属性边界，支持无引号地址及 `<image url>`；回归测试验证地址变化复用向量、标题变化重新计算。

配置与行为边界见 [知识处理结果缓存](processing-cache.md)。

## Type of Change

- [x] New feature
- [x] Performance improvement
- [x] Test
- [x] Documentation update
- [x] Configuration / Build / CI

## Related Issue

Fixes #1679

## Testing

验证环境：Windows，使用 `duckdb_use_lib` 和动态 DuckDB 库。目标基线为 Tencent/WeKnora `main`（`e3fb2335`）；本地 `upstream/main` 指向目标仓库，`origin` 指向 fork。

以下检查在当前代码上通过：

```sh
go test -tags=duckdb_use_lib -count=1 ./internal/processingcache/... ./internal/config ./internal/models/embedding ./internal/models/chat ./internal/models/vlm ./internal/infrastructure/docparser ./internal/application/service/retriever ./internal/application/service/chat_pipeline ./internal/container
go test -tags=duckdb_use_lib -race -count=1 ./internal/processingcache/... ./internal/application/service/retriever ./internal/models/vlm
go test -tags=duckdb_use_lib -race -count=1 ./internal/application/service -run 'ProcessingCache|SharedCache|WikiMarkdownDestination|WikiImageRestoreInsideJSON|WikiHTMLImage|QuestionModel|QuestionGeneration|Wiki.*Manual|Wiki.*Auto|RegenerateChunkQuestions|ResolveQuestionGeneration'
go build -tags=duckdb_use_lib -o .work-cache/tools/pr-readiness-current.exe ./cmd/server
golangci-lint run --build-tags=duckdb_use_lib --new-from-merge-base=upstream/main --timeout=10m --max-issues-per-linter=0 --max-same-issues=0 ./...
git diff --check upstream/main...HEAD
```

增量 lint 为 0 issues，保留上游既有的未知 nolint 名称警告。HTML 属性边界的六个回归用例在修复前失败，修复后通过。

组合验收使用实际 SQLite 迁移并关闭重开连接，调用 ReparseKnowledge、排队后的 ProcessDocument 和图片 handler，验证解析及 OCR/caption 复用、新 UUID、当前图片地址和 VLM 身份变化失效。知识、chunk、对象存储和队列使用 fixture；其他阶段由入口测试覆盖。

本次提交前复验未运行 Linux CI、service 整包或真实 PostgreSQL。PostgreSQL 测试需要显式设置 PROCESSING_CACHE_TEST_POSTGRES_DSN，未配置时跳过。历史上针对当前迁移编号的真实 PostgreSQL 生命周期测试已通过，但不计作本次复验。没有调用真实付费模型或真实外部向量数据库。

旧基线 `40192ace` 上执行 `go test -tags=duckdb_use_lib -count=1 -json ./internal/application/service` 曾有 28 项失败，涉及数据源调度、协作历史、shell、sandbox/skills 和 checkpoint；这些失败在同一未修改基线上复现。该历史对照不能证明当前基线的 service 整包通过。

## Checklist

- [x] 目标上游基线的 diff 检查通过（本地使用 upstream/main）
- [x] 修改的 Go 源文件已格式化
- [x] 相关包定向测试通过
- [x] 增量 lint 通过
- [x] 已说明全仓验证及环境限制
- [x] 已自审并完成独立代码复审
- [x] 已补充回归测试
- [x] 已更新配置、迁移与行为文档
- [x] 默认关闭，无新增 API 破坏性变更

## Screenshots / Recordings

无 UI 改动。
