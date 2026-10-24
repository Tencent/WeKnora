# Issue #4599 修复交付说明

Issue #4599 的报告只提供了 `TASK_TIMEOUT` 和“分块阶段长时间无进展”，没有附原始 XLSX、文件大小、工作表规模或阶段耗时。因此修复同时覆盖了已确认的高风险路径，并增加了可以定位真实瓶颈的观测。

## 修改内容

- `docreader/parser/markitdown_parser.py`
  - XLSX/XLSM 首次 MarkItDown 转换使用 `keep_data_uris=False`，避免把嵌入资源扩展成大段 data URI 后再进入 Go 分块。
  - XLSX 转换失败不再尝试第二次 data URI 模式；失败会抛出统一的 `RuntimeError`。
  - 其他格式保留一次有界 fallback（带 data URI → 不带 data URI），不会无限重试。
  - 记录 XLSX 预处理、首次转换和 fallback 的耗时及输入/输出大小；日志不包含文件内容或 data URI。

- `internal/application/service/knowledge_process.go`
  - 记录文本规范化耗时、输入/输出字节数。
  - 记录 Go 分块耗时、输入字节数和生成 chunk 数量。
  - 这些日志可以区分时间消耗在 DocReader、规范化还是 Go 分块，而不改变已有任务重试和取消语义。

- `internal/infrastructure/chunker/splitter.go`
  - `protectedSpans` 的跨模式匹配排序从插入排序改为稳定规则等价的 `sort.Slice`，避免大量表格行、链接或行内代码导致平方级退化。

- `docreader/tests/test_docx_merge.py`
  - 覆盖 XLSX 不重试 data URI、XLSX 失败异常包装，以及其他格式只执行一次有界 fallback。

## 验证

- `docreader/tests/test_docx_merge.py docreader/tests/test_xlsx_styles_repair.py docreader/tests/test_excel_parser.py`
  - **53 passed, 3 skipped, 30 subtests passed**。
- 合成 XLSX 基准：400 行 × 20 列，约 32 KB 输入，约 83 KB Markdown 输出，MarkItDown 解析约 1.05 秒，未生成图片。
- `git diff --check` 通过。

工作区已配置 Go 1.26.0 Windows 工具链、独立模块缓存和构建缓存；Go 文件已执行 `gofmt`。`internal/infrastructure/chunker` 测试通过：

```text
ok github.com/Tencent/WeKnora/internal/infrastructure/chunker 4.911s
```

`go test ./... -count=1` 已执行，结果为 **101 个包通过、29 个包失败**。失败主要来自当前 Windows 测试环境：缺少 `sqlite3.h`，以及 MinGW 8.1.0 的 SJLJ C++ 异常运行时无法链接 DuckDB 预编译静态库，出现 `_Unwind_Resume`、`__gxx_personality_seh0` 等符号错误；这不是本次 Go 源码改动引入的编译错误。部分测试还依赖未启动的外部服务或 Unix 沙箱能力。部署前应在项目 CI、Linux/WSL 或带 SEH MinGW/Clang 的 Windows 环境运行：

```text
go test ./internal/infrastructure/chunker ./internal/application/service
gofmt -w internal/infrastructure/chunker/splitter.go internal/application/service/knowledge_process.go
```

## 兼容边界

没有原始问题 XLSX，无法证明该文件的唯一瓶颈已经被完全复现。修复避免了已知的 data URI 放大和保护片段排序退化，并让下一次运行明确显示各阶段耗时；如果仍超时，日志可以直接判断是 MarkItDown、规范化、分块还是后续 embedding 阶段。
