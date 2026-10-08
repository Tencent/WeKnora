# AnyDoc 0.2.4 升级核查与压测

核查日期：2026-09-29。项目基线：`9114e4e4f905be71976a77c684d3f92731e6f9a6`。

## 结论

从 0.1.9 跟进到 0.2.4，并继续维护 vendored Go binding。升级重点是文档完整性：公式、复选框及混合 PDF 的扫描页不再被静默丢弃。

- [Go binding PR #30](https://github.com/firecrawl/anydoc/pull/30) 仍为 open，`merged=false`、`mergeable=false`，head 为 `1a7a6c04ff8f2689bf8ebbd1e0d9d61d2d109164`，最后更新时间为 2026-08-08。没有 `go/v*` 发布 tag。
- 最新 release tag 为 [v0.2.4](https://github.com/firecrawl/anydoc/tree/v0.2.4)，2026-08-27 发布，commit `42bf1c5ecdde9eb0d96d6bd75a9e6698cf93b14c`。main 在该 tag 后的三次提交只改 README。
- 已逐文件确认下载的 crate 源码与 v0.2.4 tag 一致，仅保留原有 renderer re-export 补丁。
- 检查了 [v0.1.9…v0.2.4 完整源码差异](https://github.com/firecrawl/anydoc/compare/v0.1.9...v0.2.4)，包括模型、序列化、PDF 路径、Excel 解析器和 bindings；不是仅根据版本说明判断。

## 核心变化与本项目处理

| 上游变化 | 影响与处理 |
| --- | --- |
| 0.2.0：移除 calamine，自行解析 XLS/XLSX/XLSB；改进数值显示、合并单元格与隐藏内容处理 | 保留已有格式路由，回归各容器。上游 XLSB 样例在旧版失败，新版成功；这不代表应用新增了 XLSB 上传入口。 |
| 0.2.0：DOCX run 边缘空格、重复 EPUB spine、Markdown 分隔符及表格代码中的竖线修复 | 使用上游 66 个快照，经实际 Go ABI 比较 Markdown 与错误详情。 |
| 0.2.1–0.2.2：OMML/MathML/RTF 公式转 LaTeX，美元符号转义修正 | 新增 block/inline math ABI tag、编码及解码；图片路径接受公式节点。回归 DOCX/PPTX/ODT/EPUB/RTF。 |
| 0.2.3：复选框成为 inline 节点，移除 Rust `ListItem.checked` | 新增 checkbox tag 与 bool 字段。旧 wire slot 保留为 nil；Go 的旧字段标为 deprecated。实测有标题的选中、未选中控件。 |
| 0.2.4：混合或扫描 PDF 返回 `NeedsOcr`，错误标出页码 | 新增独立错误码及 `NeedsOCR()`，通过 `errors.As` 识别包装错误，实际 Reader 测试验证回退与无 OCR 时的失败行为。页码保留在 Detail，未新增结构化页码 API。 |
| Node/Python/CLI 可选择 Firecrawl hosted OCR | Rust 不含该功能；本项目继续使用原有 builtin/docreader 回退。 |

本地线程绑定、panic 捕获、解码分配上限和图片链接 API 均保留。
本次还消除了图片提取路径的重复解析：新的 `ToDocumentWithAssetLinks` 一次解析返回原始文档树和图片 Markdown，复用单一 buffer 的分配/释放协议。先编码原始 asset ID，再重写图片链接，避免丢失图片位置与章节信息。上游语料和内置 13 种非 PDF 样例均验证合并接口与两个独立接口的输出一致。不能在 PR 合入后直接删除本地版本：还需确认上游补齐这些能力。详细差异见 [README](README.md)。

依赖锁保持 `pdf-inspector=1.14.2`、`lopdf=0.42.0`，移除 calamine 及五个专属依赖。审计发现原有 `chacha20=0.10.1` 已撤回，按[上游修复 #580](https://github.com/RustCrypto/stream-ciphers/pull/580) 更新到 0.10.2，修复 SSE2 后端误用 SSE4.1 指令的问题；其他传递依赖版本未更新。

## 已完成验证

- `scripts/build-anydoc-lib.sh`：release archive 构建成功，使用 `--locked`；C header 已同步生成。
- `ANYDOC_UPSTREAM_DIR=... go test -tags anydoc -count=1 ./internal/infrastructure/docparser/...`：通过，包括 66 个上游快照、正常文档的 Go 模型解码及图片 Markdown 路径。空输入保留 Go binding 原有的前置错误，单独断言。
- `go test -race -tags anydoc -count=1 ./internal/infrastructure/docparser/...`：通过。Go race 检测不覆盖 Rust 内存访问。
- `go test -count=1 ./internal/infrastructure/docparser/...`：未链接引擎路径通过。
- `go vet -tags anydoc ./internal/infrastructure/docparser/...`：通过。
- `go build -tags anydoc -o /tmp/weknora-anydoc-server ./cmd/server`：完整服务编译通过。
- 既有 4,000 次并发错误详情、恶意 PDF 深层嵌套和 TJ lookback 回归：通过。
- `cargo audit --file third_party/anydoc-go/Cargo.lock`：无已知漏洞；保留 `ttf-parser 0.25.1` 的停止维护警告 `RUSTSEC-2026-0192`。

## 性能测量方法

Apple M4 Pro，64 GiB RAM，macOS arm64；Go 1.26.3、Rust 1.97.1，Rust release + thin LTO。
使用相同的 Go benchmark 和上游固定样例分别链接 0.1.9 与 0.2.4，排除文件读取和编译时间。
`WithAssets=true` 覆盖生产 Reader 默认路径：Rust 解析、Go 文档解码、图片收集与官方 Markdown 输出。旧版/直接升级版解析两次，最终版解析一次。PDF 仍走文本路径。
每项 500 ms × 3 次，`GOMAXPROCS=1,4`；表中给出三次中位数。四并发的 ns/op 是总耗时除以总次数，表示吞吐能力，不是单个请求延迟。`B/op` 只统计 Go 堆，不能代表 Rust 内存。
编译和其他测试结束后顺序测量；这是本机小样本比较，不声称统计显著，也不等价于生产端到端性能。

### 同机对比：单并发

单位为 µs/op，越低越好；变化率相对 0.1.9。

| 格式 | 0.1.9 | 0.2.4 直接升级 | 0.2.4 最终版 | 最终变化 |
| --- | ---: | ---: | ---: | ---: |
| doc | 228.7 | 237.7 | 147.2 | -35.6% |
| docx | 1040.3 | 1082.4 | 559.8 | -46.2% |
| ppt | 60.0 | 62.7 | 37.0 | -38.4% |
| pptx | 887.2 | 895.4 | 450.3 | -49.2% |
| odt | 719.5 | 727.5 | 390.7 | -45.7% |
| ods | 478.2 | 483.2 | 259.3 | -45.8% |
| odp | 1157.2 | 1167.6 | 588.2 | -49.2% |
| rtf | 742.8 | 757.4 | 407.2 | -45.2% |
| epub | 313.4 | 314.5 | 174.9 | -44.2% |
| xls | 69.8 | 127.9 | 76.1 | +9.0% |
| xlsx | 217.7 | 500.3 | 261.2 | +20.0% |
| xlsb | 转换失败 | 84.3 | 48.7 | 新增可用样例 |
| csv | 155.9 | 161.2 | 90.1 | -42.2% |
| pdf | 1941.6 | 3605.4 | 3631.9 | +87.1% |

### 四并发吞吐

单位为 µs/op（每次转换分摊的墙钟时间，非请求延迟）。

| 格式 | 0.1.9 | 最终版 | 变化 |
| --- | ---: | ---: | ---: |
| doc | 53.8 | 36.6 | -32.0% |
| docx | 268.3 | 142.5 | -46.9% |
| ppt | 15.9 | 9.8 | -38.6% |
| pptx | 226.5 | 115.0 | -49.2% |
| odt | 179.6 | 99.8 | -44.4% |
| ods | 120.5 | 64.1 | -46.8% |
| odp | 297.9 | 150.2 | -49.6% |
| rtf | 187.9 | 104.3 | -44.5% |
| epub | 78.7 | 43.6 | -44.6% |
| xls | 19.1 | 19.8 | +3.5% |
| xlsx | 52.3 | 65.6 | +25.4% |
| xlsb | 转换失败 | 12.5 | 新增可用样例 |
| csv | 38.3 | 22.6 | -41.0% |
| pdf | 577.5 | 1060.4 | +83.6% |

### 性能判断

- 消除重复解析后，DOC/DOCX/PPT/PPTX/ODT/ODS/ODP/RTF/EPUB 的单并发样例耗时降低约 36%–49%，CSV 降低约 42%。
- XLS 从 69.8 µs 到 76.1 µs（约 +9%），XLSX 从 217.7 µs 到 261.2 µs（约 +20%）。新解析器的数值/布局/控件处理仍有额外代价；不能宣称所有格式加速。
- PDF 从 1.94 ms 到 3.63 ms（约 +87%）。源码新增对被标记扫描页的二次提取确认，以避免丢页和检测误报。这个路径不使用文档模型，合并解析优化不作用于它。保留完整性检查；应用默认 PDF 引擎仍是 builtin，只有选择 AnyDoc 的 PDF 路径受影响。
- Go 模型新增一个 checkbox 指针字段，Go 分配字节略有增加；原始输出保留了 B/op 和 allocs/op。

### 持续混合负载

8 workers、14 种格式轮换、30 秒，逐次检查 Markdown 与图片字节/名称一致。

| 指标 | 直接升级 | 最终版 |
| --- | ---: | ---: |
| 完成转换 | 231,291 | 306,252 |
| 吞吐 | 7,708.5 docs/s | 10,207.1 docs/s |
| p50 | 0.644 ms | 0.359 ms |
| p95 | 5.320 ms | 5.491 ms |
| p99 | 6.684 ms | 6.843 ms |
| 进程峰值 RSS（含 Rust） | 61.1 MiB | 63.3 MiB |

两轮均零错误、无输出或图片不一致。最终吞吐比直接升级提高约 32%；尾延迟没有改善，PDF 占据较长执行时间。RSS 包含测试保存的延迟样本，因此不能简单归因于解析器。该测试均匀轮换小文档，不能外推为大文档生产容量。

### 输入规模测试

| 输入 | 中位耗时 | Go B/op |
| --- | ---: | ---: |
| csv_100_rows | 0.398 ms | 129,360 |
| csv_10000_rows | 32.133 ms | 12,581,816 |
| pdf_40000_operators | 1.074 ms | 152 |
| pdf_200000_operators | 4.346 ms | 152 |
| xlsx_100_rows | 0.592 ms | 82,624 |
| xlsx_10000_rows | 46.186 ms | 7,946,442 |

CSV 为三列，XLSX 为两列；每种格式内按相同行结构扩展，生成及压缩输入不计入转换耗时。万行 XLSX 约 46.2 ms，校验最后一行未丢失。PDF 的裸 TJ token 从 4 万增到 20 万（5 倍输入），耗时约增至 4 倍，没有重现历史平方级退化。Go 分配不包括 Rust 原生堆。XLSX 原始结果见 [规模压测](benchmarks/2026-09-29/xlsx-scaling.txt)。

原始数据：[0.1.9](benchmarks/2026-09-29/baseline.txt)、[直接升级](benchmarks/2026-09-29/upgrade.txt)、[最终版](benchmarks/2026-09-29/optimized.txt)、[直接升级压力](benchmarks/2026-09-29/upgrade-stress.txt)、[最终版压力](benchmarks/2026-09-29/optimized-stress.txt)、[规模测试](benchmarks/2026-09-29/scaling.txt)、[审计](benchmarks/2026-09-29/audit.txt)。

## 复现

在仓库根目录执行功能核查：

```bash
scripts/build-anydoc-lib.sh
git clone --branch v0.2.4 --depth 1 https://github.com/firecrawl/anydoc.git /tmp/anydoc-v0.2.4
ANYDOC_UPSTREAM_DIR=/tmp/anydoc-v0.2.4 go test -tags anydoc -count=1 ./internal/infrastructure/docparser/...
go test -race -tags anydoc -count=1 ./internal/infrastructure/docparser/...
go vet -tags anydoc ./internal/infrastructure/docparser/...
cargo audit --file third_party/anydoc-go/Cargo.lock
```

性能与混合负载：

```bash
go test -tags anydoc -run '^$' -bench '^BenchmarkConvert$' -benchmem -benchtime=500ms -count=3 -cpu=1,4 ./internal/infrastructure/docparser/anydoc
go test -tags anydoc -run '^$' -bench '^BenchmarkConvertScaling$' -benchmem -benchtime=500ms -count=3 -cpu=1 ./internal/infrastructure/docparser/anydoc
go test -tags anydoc -c -o /tmp/anydoc.test ./internal/infrastructure/docparser/anydoc
cd internal/infrastructure/docparser/anydoc
ANYDOC_STRESS_DURATION=30s /usr/bin/time -l /tmp/anydoc.test -test.run '^TestConcurrentConversionStress$' -test.v
```

Linux 用 `/usr/bin/time -v` 记录 RSS。测试二进制需从包目录运行以读取样例。基线比较需要在旧版本单独编译同一 benchmark 和样例，再顺序运行两个二进制；旧版 XLSB 样例转换失败，基线应排除该项，不能把失败计入吞吐。

## 验证边界

本次运行平台为 macOS arm64，Linux glibc/musl、Windows 交叉构建未在本机执行。混合 PDF 测试使用 stub 验证回退请求和结果；未调用外部 OCR 服务、存储或检索索引。压测覆盖解析层，不含上传排队、网络与 OCR 耗时。峰值 RSS 和短时压力不能证明长期不存在原生内存泄漏。
