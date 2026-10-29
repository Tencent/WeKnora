# 知识处理结果缓存

针对 [Issue #1679](https://github.com/Tencent/WeKnora/issues/1679)，本实现复用输入和配置相同的昂贵处理产物。普通重解析保留缓存；命中后继续创建当前 chunk、绑定当前引用并写索引。Wiki reduce、页面协调及任务最终化仍读取当前状态。

## 覆盖阶段

| 阶段 | 缓存内容与失效输入 |
| --- | --- |
| 文件解析 | 文件字节、文件名/类型/标题、实际 parser 身份和有效覆盖配置；保存 Markdown、结构数据及可移植图片/音频字节 |
| 图片预处理 | 实际图片字节、预处理参数、reader 身份；保存处理后的图片字节 |
| OCR、caption、observation | 实际图片字节、动作、完整提示、thinking 参数及有效 VLM 身份 |
| 文本向量 | 实际清洗后的文本、有效模型配置和维度；支持同租户跨文档复用 |
| 图像向量 | 经过 imageprep 后的实际图片字节、MIME、有效模型配置和维度 |
| 文档/表格摘要、问题 | 实际渲染后的完整消息、生成参数和模型身份；问题数量也显式进入键 |
| Graph 抽取 | 有效提示及模型输入；保存未绑定 chunk 的图谱，再绑定当前 UUID |
| Wiki map | 候选抽取、去重、摘要、引用分类分别缓存；引用保存调用内句柄并映射到当前 UUID |

UUID 更换不会使模型产物失效，因此保留现有随机 chunk UUID。这里没有引入稳定主键、差异向量写入或原子发布；重建仍按现有流程重写 chunk 和索引。

启用缓存时，Wiki 候选抽取使用稳定文档内容，历史 slug 的协调仍查询当前页面。去重输入包含当前候选，候选或最终页面身份变化可触发相关阶段重新计算。相同文档和稳定页面状态下，第一次重解析即可复用 map 模型结果；其他文档改变页面状态后，不承诺只运行 reduce。

文本模型使用稳定的图片占位符，并在输出后恢复本轮图片地址。文本向量忽略图片存储地址、保留 alt、标题、caption 和 OCR 文本；实际送往模型的文本与缓存键一致。图像向量按像素输入单独缓存。query embedding 不在本功能范围。

## 配置

默认关闭。配置后按现有应用流程重启：

```yaml
processing_cache:
  read_enabled: true
  write_enabled: true
  max_bytes: 1048576
  parsing_max_bytes: 67108864
  image_max_bytes: 16777216
  retention_days: 30
  epoch: "1"
```

普通产物默认上限 1 MiB，解析产物默认 64 MiB，图片预处理默认 16 MiB，大小按序列化后的 payload 计算。解析和预处理可配置上限分别为 256 MiB、64 MiB；非正值使用默认配置。保留期默认 30 天，最大 3650 天。只开读取不会写入新产物。

远程解析服务还需设置 `DOCREADER_CACHE_VERSION` 为实际部署版本，并在服务实现或相关配置变化时更新。缺少可靠版本的远程解析器会绕过解析缓存。内置解析器使用实现版本和有效配置。动态 URL 解析不按 URL 字符串缓存，后续内容阶段仍可复用。

模型同名别名背后的权重发生变化时，更新 `epoch` 强制失效。配置指纹包含实际 endpoint、provider、模型、协议选项和认证/路由配置；凭证仅参与摘要，不明文持久化。无法取得有效模型身份、租户或文档作用域时绕过缓存。

用户主动重新生成问题、刷新摘要会绕过相应缓存；普通重解析和续跑使用缓存。

## 持久化、删除和并发

- PostgreSQL 使用 `000134_processing_cache`，SQLite 使用 `000051_processing_cache`，均提供回滚脚本和共享查找索引。
- 文档物理删除通过外键级联清理自身缓存，拒绝删除后的迟到写入。软删除保留至过期清理；重解析不清理缓存。
- 文本向量在同租户内查找相同输入，命中后保存当前文档副本。删除来源文档不会删除其他文档已经保存的副本。不同租户不复用。该方案节约模型计算，不去重各文档缓存占用的磁盘空间。
- 过期记录不命中。有写入时，每个 store 实例至多每分钟触发一次有界清理，最多 1000 条；只读或空闲时不会主动回收磁盘数据。
- 相同完整批次在同一 runtime 内合并；不同进程、不同文档同时 miss 仍可能重复调用。数据库唯一键决定每个文档键的最终赢家，调用者使用有效赢家。
- 缓存读取/写入失败回退计算；损坏 JSON、校验和错误、不完整解析/模型响应及无效向量不复用。合法空输出由各阶段规则单独处理。
- 缓存命中不跳过现有任务取消、attempt、文档/当前 chunk 存活检查和最终化流程。执行者取消会向同批次等待者返回错误，等待者也可单独取消。

## 验证

`processing_cache_acceptance_test.go` 使用真实 `ReparseKnowledge`、排队的 `ProcessDocument`、图片 handler 和后处理 handler，缓存使用实际 SQLite 迁移并关闭重开连接：

| 场景 | Parser 累计调用 | OCR 累计调用 | Caption 累计调用 |
| --- | --- | --- | --- |
| 冷启动 | 1 | 1 | 1 |
| DB/runtime 重建后，同文件重解析 | 1 | 1 | 1 |
| 只修改 VLM 身份 | 1 | 2 | 2 |

测试同时断言当前图片地址、新 chunk UUID 和 attempt 传递。知识/chunk/对象存储/队列是边界 fixture，未验证真实 Redis 或资源 catalog 回收。Summary、Questions、Graph、Wiki、文本和图像向量由各自实际入口测试覆盖，不能将这些测试合称所有生产组件的端到端测试。

PostgreSQL 集成测试使用独立临时 schema，执行实际迁移，验证跨文档复用、删除级联、迟到写入拒绝、并发赢家及回滚：

```sh
PROCESSING_CACHE_TEST_POSTGRES_DSN='host=127.0.0.1 port=5432 user=postgres dbname=postgres sslmode=disable' go test ./internal/processingcache/gormstore -run TestPostgresProcessingCacheMigrationLifecycle -count=1
```

常规验证命令：

```sh
go test ./internal/processingcache/... ./internal/config ./internal/models/embedding ./internal/models/chat ./internal/models/vlm ./internal/infrastructure/docparser ./internal/application/service/retriever
go test ./internal/application/service ./internal/application/service/chat_pipeline ./internal/container
go test -race ./internal/processingcache/... ./internal/application/service/retriever
```

Windows 环境使用 `-tags=duckdb_use_lib` 及动态 DuckDB 库避开既有静态库链接故障。service 整包的既有 Windows 失败已与未修改 HEAD 对照；具体验证结果见本次 PR 的验证说明。没有调用真实付费模型或真实外部向量数据库。
