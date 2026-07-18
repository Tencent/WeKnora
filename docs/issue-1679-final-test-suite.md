# Issue #1679 Final Test Suite

用于验证“重建/重解析知识时复用 OCR、Embedding、Wiki Map 等缓存，避免全量重算”的最终完成状态。

## 运行范围

最终 PR 合并前至少运行：

```powershell
go test ./internal/types ./internal/application/repository ./internal/models/embedding ./internal/application/service/retriever ./internal/application/service
```

如果验收测试采用 opt-in build tag，运行：

```powershell
go test -tags issue1679 ./internal/types ./internal/application/repository ./internal/models/embedding ./internal/application/service/retriever ./internal/application/service
```

## A. 稳定 Chunk 身份

1. 相同知识、相同 chunk 类型、相同归一化内容、相同出现序号、相同 chunking 配置，生成完全相同的 UUID 字符串。
2. 仅空白差异不改变 chunk ID，例如 `" hello\nworld "` 与 `"hello world"` 命中同一稳定 ID。
3. 同一文档内重复内容必须通过稳定出现序号区分，两个相同内容 chunk 不得生成同一个 ID。
4. 修改 chunking 配置时，文本 chunk ID 应失效；VLM 图片缓存 key 不应随 chunking 配置变化失效。
5. 父子 chunk 模式下，父 chunk 和子 chunk 都必须稳定；子 chunk 的 `ParentChunkID` 在未变内容重解析后保持一致。

## B. Chunk Diff 与保留

1. 初次解析写入 `A, B, C`，未变内容重解析仍为 `A, B, C` 时：
   - 不调用整知识 `DeleteChunksByKnowledgeID`。
   - 不软删并重插已有 chunk。
   - `id / seq_id / content_hash / chunk_index / pre_chunk_id / next_chunk_id` 保持稳定。
2. 重解析从 `A, B, C` 变为 `A, B2, C, D` 时：
   - `A` 和 `C` 保留原 ID。
   - 只新增 `B2` 和 `D`。
   - 只删除旧 `B`。
   - 前后链更新为 `A -> B2 -> C -> D`。
3. 如果旧版本中同稳定 ID 的 chunk 被软删，重解析命中时应恢复或更新该行，不得因主键冲突失败。
4. 图片 OCR / Caption chunk 也参与 diff；图片未变时 OCR / Caption chunk ID 保持稳定。

## C. Embedding 缓存

1. 首次索引文本 `A, B, A` 时，底层 embedding API 只应按唯一归一化文本 miss 调用，缓存写入 `A` 和 `B`。
2. 相同文本重解析时：
   - 仍写入当前 chunk 对应的向量索引记录。
   - 不再次调用底层 embedding API。
3. embedding cache key 至少包含：
   - tenant scope
   - embedding model ID 或有效模型名
   - dimension
   - 归一化 embedding 输入 hash
4. 换 embedding model 或 dimension 时，仅 embedding cache miss；OCR / Wiki map 不应因为 embedding 配置变化失效。
5. keyword-only 或 wiki-only KB 不应触发 embedding API 调用。
6. 部分 cache hit、部分 miss 时，只对 miss 文本调用底层 API，并保持返回向量顺序与输入 index 顺序一致。

## D. VLM OCR / Caption 缓存

1. 相同图片字节、相同 VLM 模型、相同 prompt kind、相同 prompt version 重解析时：
   - OCR 不再次调用 VLM。
   - Caption 不再次调用 VLM。
   - 仍能创建或保留 OCR / Caption chunk。
2. OCR 和 Caption 必须使用不同 cache key，避免两类输出互相污染。
3. 图片字节变化时，只重算变化图片的 OCR / Caption。
4. VLM 输出命中缓存后，应作为冻结正典内容传给下游 chunking / embedding / wiki，避免模型随机抖动污染下游 hash。
5. 同一文档中同一图片多处引用时，只应保存一次缓存结果，但引用关系仍指向正确父 chunk。

## E. Wiki Map 缓存

1. 相同冻结文档内容、相同 extraction granularity、相同 chat model、相同 prompt version、相同旧 slug 上下文时：
   - 跳过 `postprocess.wiki.extract`。
   - 跳过 `postprocess.wiki.summary`。
   - 跳过 `postprocess.wiki.classify`。
   - 直接恢复等价的 `SlugUpdate` 列表进入 reduce。
2. Wiki reduce 不缓存；每次 ingest/reparse 仍需调用 reduce，以便反映其它文档当前贡献状态。
3. 修改 extraction granularity、chat model 或 prompt version 时，Wiki map cache miss。
4. 仅换 embedding model 时，Wiki map cache 应继续命中。
5. 重解析时旧页面集合参与 cache key 或缓存输入；否则 stale slug / reparse overlap 语义会错误。
6. 缓存恢复的 `SourceChunks` / `ChunkRefs` 必须引用当前稳定 chunk ID，并能通过 chunk repo 查到真实 chunk 内容。

## F. GraphRAG Per-Chunk 缓存

1. 相同 chunk 稳定 ID、相同 chunk 内容、相同 graph prompt version、相同 chat model 时，per-chunk entity/relation extraction 命中缓存。
2. 只有变更 chunk 触发新的 graph extraction。
3. graph merge / 全局关系汇总可以重跑，但不得重复调用未变 chunk 的 LLM 抽取。
4. 删除 chunk 后，graph 中对应 chunk 引用必须被移除，不得残留死引用。

## G. 崩溃续跑

1. 模拟 OCR 已完成、embedding 未完成后任务失败；重跑时 OCR cache hit，embedding 只补缺失项。
2. 模拟 embedding 已完成、wiki map 未完成后任务失败；重跑时 embedding cache hit，wiki map 执行一次。
3. 模拟 wiki map 已完成、reduce 失败；重跑时 wiki map cache hit，reduce 重新执行。
4. 所有续跑路径不得重复消耗已完成层的 LLM / VLM / embedding 调用。

## H. 配置失效矩阵

1. 改 embedding model：只重算 embedding。
2. 改 VLM model 或 VLM prompt version：只重算 VLM 及其下游依赖内容。
3. 改 chunk size / overlap：重算 chunk 与 embedding；VLM 图片缓存继续命中。
4. 改 Wiki extraction granularity / wiki prompt version：重算 Wiki map；embedding / VLM 不受影响。
5. 改 Wiki reduce prompt version：只重跑 reduce，不重跑 per-document map。
6. 文件字节不变、配置不变：除 Wiki reduce 外，LLM / VLM / embedding API 调用数应接近 0。

## I. 指标与断言

最终验收测试应记录并断言以下计数：

- `chunk_created_count`
- `chunk_deleted_count`
- `embedding_api_call_count`
- `embedding_cache_hit_count`
- `vlm_ocr_api_call_count`
- `vlm_caption_api_call_count`
- `wiki_extract_call_count`
- `wiki_summary_call_count`
- `wiki_classify_call_count`
- `wiki_reduce_call_count`
- `graph_chunk_extract_call_count`

未变内容重解析的期望：

- `chunk_deleted_count = 0`
- `embedding_api_call_count = 0`
- `vlm_ocr_api_call_count = 0`
- `vlm_caption_api_call_count = 0`
- `wiki_extract_call_count = 0`
- `wiki_summary_call_count = 0`
- `wiki_classify_call_count = 0`
- `wiki_reduce_call_count >= 1`
- `graph_chunk_extract_call_count = 0`

## J. 最小验收场景

准备一个包含以下内容的测试知识：

- 3 个文本 chunk，其中第 1 和第 3 个内容相同，用于验证重复内容稳定序号。
- 1 张图片，启用 OCR 和 Caption。
- Wiki enabled。
- Graph enabled。
- Question generation enabled。

执行顺序：

1. 首次解析，记录 chunk IDs 和各层 API 调用计数。
2. 内容不变重解析，断言可缓存层 API 调用为 0，chunk IDs 保持稳定。
3. 只修改第二个文本 chunk，断言只重算第二个 chunk 相关 embedding / graph / wiki map 输入。
4. 只修改 embedding model，断言只重算 embedding。
5. 只修改 wiki granularity，断言只重算 wiki map + reduce。
6. 只修改图片字节，断言只重算 VLM 及依赖该图片内容的下游缓存。
