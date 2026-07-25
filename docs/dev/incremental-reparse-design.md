# 增量重构知识库：原理解析与架构文档

## 目录

1. [问题背景](#1-问题背景)
2. [核心设计思想](#2-核心设计思想)
3. [系统架构总览](#3-系统架构总览)
4. [模块详解](#4-模块详解)
   - 4.1 [内容寻址的稳定 Chunk ID](#41-内容寻址的稳定-chunk-id)
   - 4.2 [增量 Diff 引擎](#42-增量-diff-引擎)
   - 4.3 [增量重构流程改造](#43-增量重构流程改造)
   - 4.4 [缓存统计收集器](#44-缓存统计收集器)
5. [数据流详解](#5-数据流详解)
6. [数据库变更](#6-数据库变更)
7. [验证结果](#7-验证结果)
8. [后续缓存策略规划](#8-后续缓存策略规划)

---

## 1. 问题背景

### 原始重构流程的痛点

当用户在 WeKnora 中对已有知识文档执行「重构知识」操作时，原始流程如下：

```
用户点击重构 → 删除所有 chunk → 删除所有向量 → 重新解析文档 → 重新分块 → 重新生成向量 → 完成
```

这存在三个严重问题：

| 问题 | 影响 |
|------|------|
| **全量删除重建** | 即使文档只改了一个字，所有 chunk 都被删除后重新创建，浪费大量计算 |
| **Chunk ID 不稳定** | 使用 `uuid.New()` 生成 ID，同一内容每次重建得到不同 ID，无法跨重建复用 |
| **下游引用断裂** | Wiki 页面、GraphRAG 图谱边等引用了 chunk ID，ID 变化导致引用全部失效 |

### 实际场景

假设一个 100 页 PDF 文档，解析后产生 200 个 chunk。用户修正了第 50 页的一个错别字后重新上传：

- **原始方案**：删除 200 个 chunk + 200 个向量 → 重新生成 200 个 chunk + 200 个向量
- **增量方案**：识别出第 99-100 号 chunk 有变化 → 只重建这 2 个 chunk + 2 个向量，其余 198 个原样保留

---

## 2. 核心设计思想

### 三层设计

```
┌─────────────────────────────────────────────────────────┐
│  第一层：内容寻址 (Content-Addressable)                   │
│  chunk ID = hash(doc_id + 归一化内容 + 序号)              │
│  → 内容相同 ⇒ ID 相同 ⇒ 跨重建凭引用存活                   │
├─────────────────────────────────────────────────────────┤
│  第二层：增量 Diff                                       │
│  对比新旧 chunk 的 content_hash，分类为：                  │
│  unchanged / changed / added / removed                   │
│  → 只对变化的 chunk 执行删除+重建                         │
├─────────────────────────────────────────────────────────┤
│  第三层：流程改造                                         │
│  ReparseKnowledge 不再提前删除所有 chunk                   │
│  processChunks 内部执行 diff，跳过未变化的 chunk           │
│  → 避免主键冲突，保证原子性                               │
└─────────────────────────────────────────────────────────┘
```

### 为什么这样设计？

1. **内容寻址是地基**：只有当"相同内容 → 相同 ID"时，下游系统（向量库、Wiki、图谱）才能通过 ID 引用跨重建存活。这类似于 Git 的内容寻址设计——相同内容产生相同 hash，天然去重。

2. **Diff 引擎是核心**：通过对比 `content_hash` 字段，可以在 O(n) 时间内确定哪些 chunk 发生了变化，避免全量重建。

3. **流程改造是保障**：原始的 `ReparseKnowledge` 在处理开始前就删除了所有 chunk，导致 diff 逻辑无旧数据可比。必须改为"先 diff，再按需删除"。

---

## 3. 系统架构总览

```
  用户触发「重构知识」
         │
         ▼
┌─────────────────────────────┐
│  ReparseKnowledge()         │
│  ┌───────────────────────┐  │
│  │ 增量清理（不再删chunk） │  │  ← 只删除图谱数据 + 图片
│  │ 保留 chunk 供 diff     │  │
│  └───────────┬───────────┘  │
│              ▼              │
│  ┌───────────────────────┐  │
│  │ 重新解析文档 → 分块    │  │  ← 获取新的 chunk 列表
│  └───────────┬───────────┘  │
│              ▼              │
│  ┌───────────────────────┐  │
│  │  processChunks()      │  │
│  │  ┌─────────────────┐  │  │
│  │  │ 1. 查询旧 chunks │  │  │  ← ListChunksByKnowledgeID
│  │  ├─────────────────┤  │  │
│  │  │ 2. 计算新hash    │  │  │  ← StableChunkID + ComputeContentHash
│  │  ├─────────────────┤  │  │
│  │  │ 3. Diff 新旧     │  │  │  ← 对比 content_hash
│  │  ├─────────────────┤  │  │
│  │  │ 4. 跳过 unchanged │  │  │  ← 不插入、不删除
│  │  ├─────────────────┤  │  │
│  │  │ 5. 删除 changed  │  │  │  ← 软删除旧 chunk + 向量
│  │  ├─────────────────┤  │  │
│  │  │ 6. 插入 new      │  │  │  ← 创建新 chunk + 向量
│  │  └─────────────────┘  │  │
│  └───────────────────────┘  │
└─────────────────────────────┘
```

---

## 4. 模块详解

### 4.1 内容寻址的稳定 Chunk ID

**文件**: `internal/application/service/content_hash.go`

#### 原理

传统方案使用 `uuid.New()` 生成 chunk ID，每次运行结果不同。我们改用内容寻址方案：

```go
// 归一化：去除首尾空白
func NormalizeChunkContent(content string) string {
    return strings.TrimSpace(content)
}

// 计算 content_hash
func ComputeContentHash(knowledgeID string, chunkIndex int, content string) string {
    normalized := NormalizeChunkContent(content)
    h := sha256.New()
    h.Write([]byte("v1:" + knowledgeID + ":" + strconv.Itoa(chunkIndex) + ":" + normalized))
    return hex.EncodeToString(h.Sum(nil))
}

// 生成稳定的 32 字符 chunk ID
func StableChunkID(knowledgeID string, chunkIndex int, content string) string {
    hash := ComputeContentHash(knowledgeID, chunkIndex, content)
    return hash[:32]  // 取前 32 字符作为 ID
}
```

#### Hash 键组成

| 组成部分 | 作用 |
|---------|------|
| `v1` | 版本号，便于未来 hash 算法升级时整体失效 |
| `knowledgeID` | 文档隔离，不同文档的相同内容不会冲突 |
| `chunkIndex` | 位置隔离，同一文档内不同位置的相同内容不会冲突 |
| `normalized content` | 实际内容，归一化后参与计算 |

#### 关键特性

- **确定性**：同一文档、同一位置、同一内容 → 永远生成相同 ID
- **内容敏感**：内容任何变化（即使一个字）→ ID 完全不同
- **版本可控**：修改 `ContentHashVersion` 可强制全部失效

#### 数据库字段

`chunks` 表新增 `content_hash` 列，存储完整的 64 字符 SHA-256 hash，用于 diff 比较：

```sql
-- chunks 表中的关键字段
id            VARCHAR(32)   -- StableChunkID 生成的前 32 字符
content_hash  VARCHAR(64)   -- 完整 SHA-256 hash
chunk_index   INT           -- chunk 在文档中的序号
```

---

### 4.2 增量 Diff 引擎

**文件**: `internal/application/service/content_hash.go` — `DiffChunks` 函数

#### 数据结构

```go
type ChunkDiffResult struct {
    Unchanged    []*types.Chunk    // 内容未变化的 chunk（保留旧 ID）
    Changed      []*types.Chunk    // 内容变化的 chunk（需删除旧的、插入新的）
    Added        []*types.Chunk    // 新增的 chunk
    RemovedIDs   []string          // 被删除的旧 chunk ID
    UnchangedIDs map[string]bool   // 未变化 chunk 的 ID 集合（用于快速查找）
}
```

#### Diff 算法

```
输入：oldChunks[]（数据库现有）、newChunks[]（重新解析得到）

1. 构建 oldMap: chunkIndex → (chunkID, contentHash)
2. 构建 newMap: chunkIndex → chunk

3. 遍历 newChunks：
   ├─ chunkIndex 在 oldMap 中不存在 → Added
   ├─ contentHash 相同 → Unchanged（复用旧 ID）
   └─ contentHash 不同 → Changed（旧 ID 加入 RemovedIDs）

4. 遍历 oldMap：
   └─ chunkIndex 在 newMap 中不存在 → RemovedIDs
```

#### 复杂度

- 时间复杂度：O(n + m)，n = 旧 chunk 数，m = 新 chunk 数
- 空间复杂度：O(n + m)

#### 在 processChunks 中的实际使用

在 `knowledge_process.go` 中，diff 逻辑直接内联实现（而非调用 `DiffChunks` 函数），以便更精细地控制日志和统计：

```go
// 查询旧 chunks
oldChunks, _ := s.chunkService.ListChunksByKnowledgeID(ctx, knowledge.ID)

// 构建旧 chunk 的索引映射
oldHashByIndex := make(map[int]string)
oldIDByIndex   := make(map[int]string)
for _, oc := range oldChunks {
    oldHashByIndex[oc.ChunkIndex] = oc.ContentHash
    oldIDByIndex[oc.ChunkIndex]   = oc.ID
}

// 计算新 chunk 的 hash 并 diff
unchangedIndexSet := make(map[int]bool)
for idx, chunkData := range chunks {
    h := StableChunkID(knowledge.ID, idx, chunkData.Content)
    if oldHash, exists := oldHashByIndex[idx]; exists && oldHash == h {
        unchangedIndexSet[idx] = true  // 标记为未变化
    }
}
```

---

### 4.3 增量重构流程改造

**文件**: `internal/application/service/knowledge_process.go`

#### 改造前 vs 改造后

| 步骤 | 改造前 | 改造后 |
|------|--------|--------|
| ReparseKnowledge 清理 | 调用 `cleanupKnowledgeResources`，删除所有 chunk + 向量 | 只删除图谱数据 + 图片，**保留 chunk** |
| processChunks 入口 | 直接创建所有新 chunk | 先 diff，跳过 unchanged |
| chunk 创建 | `uuid.New()` | `StableChunkID()` |
| 向量生成 | 全部重新生成 | 只为 changed/added 生成 |

#### ReparseKnowledge 改造

```go
// === Incremental reparse: do NOT delete chunks/vectors here ===
// processChunks will diff old vs new and only delete what changed.
logger.Infof(ctx, "[Reparse] Incremental cleanup for knowledge: %s (preserving chunks for diff)", knowledgeID)
{
    // 只删除图谱数据（图谱增量删除暂不支持）
    namespace := types.NameSpace{KnowledgeBase: existing.KnowledgeBaseID, Knowledge: existing.ID}
    s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace})

    // 重置存储用量（重新处理后会重新计算）
    if existing.StorageSize > 0 {
        s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, -existing.StorageSize)
        existing.StorageSize = 0
    }

    // 清理图片（但保留 chunk 记录用于 diff）
    deleteExtractedImages(ctx, fileSvc, imageURLs)
}
```

#### processChunks 中的跳过逻辑

```go
// 遍历新 chunks，跳过未变化的
for idx, chunkData := range chunks {
    chunkIdx := int(chunkData.Seq)

    // Skip unchanged chunks — they already exist in DB with the same stable ID
    if unchangedIndexSet[chunkIdx] {
        oldID := oldIDByIndex[chunkIdx]
        chunks[idx].ChunkID = oldID
        logger.Infof(ctx, "[Incremental] Skipping unchanged chunk #%d (id=%s)", chunkIdx, oldID)
        continue
    }

    // 为变化的 chunk 创建新记录
    textChunk := &types.Chunk{
        ID:              StableChunkID(knowledge.ID, chunkIdx, chunkData.Content),
        // ... 其他字段
    }
    // ... 插入数据库、生成向量等
}
```

#### 图谱数据的增量处理

```go
// 只有 chunk 发生变化时才删除图谱数据
if len(removedIDs) > 0 {
    logger.Infof(ctx, "[Incremental] %d chunks changed, deleting graph data for re-extraction", len(removedIDs))
    s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace})
} else {
    logger.Infof(ctx, "[Incremental] No chunks changed, skipping graph deletion")
}
```

---

### 4.4 缓存统计收集器

**文件**: `internal/application/service/reparse_cache_stats.go`

#### 设计目标

为增量重构提供可观测性，记录每次重构的：
- chunk 级别统计（旧/新/未变化/已变化/新增/已删除）
- 缓存层级统计（各缓存层的命中/未命中/命中率/耗时）

#### 核心结构

```go
type ReparseStats struct {
    KnowledgeID    string
    Attempt        int
    StartedAt      time.Time
    Events         []CacheEvent       // 所有缓存事件
    OldChunkCount  int
    NewChunkCount  int
    UnchangedCount int
    ChangedCount   int
    AddedCount     int
    RemovedCount   int
}

type CacheEvent struct {
    Layer     CacheLayer  // vlm / embedding / summary / question / wiki_map / graph_entity
    Hit       bool
    Key       string
    Duration  time.Duration
    Timestamp time.Time
}
```

#### 支持的缓存层级

```go
const (
    CacheLayerVLM         CacheLayer = "vlm"          // VLM OCR/Caption
    CacheLayerEmbedding   CacheLayer = "embedding"     // Embedding 向量
    CacheLayerSummary     CacheLayer = "summary"       // 文档摘要
    CacheLayerQuestion    CacheLayer = "question"      // 问题生成
    CacheLayerWikiMap     CacheLayer = "wiki_map"      // Wiki per-doc map
    CacheLayerGraphEntity CacheLayer = "graph_entity"  // GraphRAG 实体抽取
)
```

#### 输出示例

```
=== Reparse Cache Stats ===
Knowledge: 90336441-...  Attempt: 0  Elapsed: 2.3s
Chunks: old=4 new=4 unchanged=3 changed=1 added=0 removed=1

Cache Layer       Hits  Misses  Hit Rate
-----------       ----  ------  --------
embedding            3       1     75.0%
graph_entity         0       1      0.0%
```

---

## 5. 数据流详解

### 完整时序图

```
用户          前端          API Server         processChunks        数据库         向量库
 │              │               │                   │                 │              │
 │──点击重构──→│               │                   │                 │              │
 │              │──POST reparse→│                   │                 │              │
 │              │               │──查图谱删除────→│                 │              │
 │              │               │──查图片删除────→│                 │              │
 │              │               │──保留chunk────→│                 │              │
 │              │               │──设status=pending                  │              │
 │              │               │──enqueue task──→│                 │              │
 │              │               │                   │                 │              │
 │              │               │     ┌─重新解析文档─┐                │              │
 │              │               │     │  重新分块    │                │              │
 │              │               │     └──────┬──────┘                │              │
 │              │               │            │──查旧chunks────────→│              │
 │              │               │            │←─返回旧chunks────────│              │
 │              │               │            │──计算新hash           │              │
 │              │               │            │──diff                  │              │
 │              │               │            │  ├─unchanged: 跳过     │              │
 │              │               │            │  ├─changed: 删旧+插新  │              │
 │              │               │            │  └─added: 插新         │              │
 │              │               │            │──生成向量(仅changed)─────────────→│
 │              │               │            │──更新status=completed─→│              │
 │              │←─轮询status──│            │                        │              │
 │←─显示完成──│               │                   │                 │              │
```

### 关键决策点

| 决策点 | 条件 | 动作 |
|--------|------|------|
| 是否删除图谱 | `len(removedIDs) > 0` | 是→删除全部图谱数据重新抽取；否→跳过 |
| 是否跳过 chunk 插入 | `unchangedIndexSet[chunkIdx] == true` | 是→复用旧 ID，不插入；否→创建新 chunk |
| 是否生成向量 | chunk 未被跳过 | 是→调用 embedding API；否→跳过 |
| 是否重新抽取图谱实体 | chunk 内容变化 | 是→重新抽取；否→跳过 |

---

## 6. 数据库变更

### 新增列

```sql
-- chunks 表已有 content_hash 列（历史迁移已包含）
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS content_hash VARCHAR(64);
```

### 关键不变量

1. **ID 唯一性**：同一 `knowledge_id` 下，同一 `chunk_index` 只有一个 active chunk（`deleted_at IS NULL`）
2. **Hash 一致性**：`id` 的前 32 字符必须等于 `content_hash` 的前 32 字符
3. **软删除语义**：变化的旧 chunk 通过 `deleted_at` 软删除，不物理删除（保留审计追踪）

### 数据库状态示例

重构后 `test_partial.txt`（chunk 0 内容变化，chunk 1 未变化）：

```
 id                 | chunk_index | content_hash                  | alive | 说明
--------------------+-------------+-------------------------------+-------+------------------
 5ed40e91...        |           0 | 5ed40e91...44a166c1...        | t     | 新 chunk 0（内容变化后重新生成）
 fb039ef2...        |           0 | fb039ef2...1dd7358e...        | f     | 旧 chunk 0（软删除）
 ec1e43c9...        |           1 | ec1e43c9...7357b1ce...        | t     | chunk 1（未变化，原样保留）
```

---

## 7. 验证结果

### 测试场景

| 文档 | 操作 | 预期 | 实际 | 结果 |
|------|------|------|------|------|
| `test_partial.txt` | 修改 chunk 0 内容后重构 | chunk 0 重建，chunk 1 保留 | chunk 0 软删除旧+创建新，chunk 1 原样保留 | ✅ 通过 |
| `Docker 基础.pdf` | 内容未变，执行重构 | 全部 chunk 保留 | 4 个 chunk 全部 unchanged，零删除零新建 | ✅ 通过 |

### 日志验证

```
[Reparse] Incremental cleanup for knowledge: 0c082623... (preserving chunks for diff)
[Incremental] knowledge=0c082623... old=4 new=4 unchanged=4 changed=0 added=0
[Incremental] No chunks changed, skipping graph deletion
[Incremental] Skipping unchanged chunk #0 (id=d840a650...)
[Incremental] Skipping unchanged chunk #1 (id=2e2cd5ba...)
[Incremental] Skipping unchanged chunk #2 (id=b7615a2d...)
[Incremental] Skipping unchanged chunk #3 (id=fe53be4d...)
```

### 性能对比

| 指标 | 原始方案 | 增量方案（无变化） | 增量方案（1/4 变化） |
|------|---------|-------------------|---------------------|
| chunk 删除 | 4 | 0 | 1 |
| chunk 创建 | 4 | 0 | 1 |
| 向量生成 | 4 | 0 | 1 |
| 图谱删除 | 1 | 0 | 1 |
| 图谱抽取 | 4 | 0 | 1 |
| 总 API 调用 | ~12+ | 0 | ~3 |

---

## 8. 后续缓存策略规划

增量重构的地基（稳定 ID + diff 引擎）已经就绪，后续缓存策略将在此基础上逐层构建：

### 缓存层级与优先级

```
优先级    缓存层          缓存键                                    失效条件
──────    ────────        ────────                                  ────────
P0        Embedding       hash(归一化块文本) + embedding_model_id    换模型/改内容
P1        GraphRAG        hash(chunk内容) + chat_model + prompt_ver  换模型/改内容/改prompt
P2        问题生成        hash(chunk内容) + chat_model + prompt_ver  换模型/改内容/改prompt
P2        文档摘要        hash(全部chunk内容) + chat_model + prompt  换模型/改内容/改prompt
P3        Wiki per-doc    hash(冻结后文档内容) + 粒度 + model + ver   换模型/改内容/改prompt
P4        VLM OCR/Caption hash(图片字节) + vlm_model + prompt_ver     换模型/图片变化
```

### 分层失效策略

```
换 embedding 模型     → 仅 Embedding 缓存失效，其他存活
改 chunk_size         → chunk 全部重建，但 VLM/Wiki map 按内容键存活
改 GraphRAG prompt    → 仅 GraphRAG 缓存失效
换 VLM 模型           → 仅 VLM 缓存失效
文档内容变化          → 变化 chunk 的所有下游缓存失效，未变化的存活
```

### 当前进度

- [x] 内容寻址的稳定 Chunk ID
- [x] 增量 Diff 引擎
- [x] processChunks 增量处理（跳过 unchanged）
- [x] ReparseKnowledge 增量清理（保留 chunk 供 diff）
- [x] 缓存统计收集器框架
- [x] Embedding 缓存
- [x] LLM 结果缓存（问题生成 + 文档摘要）
- [x] GraphRAG 实体抽取缓存（复用 LLM Cache，在 extract_entity.go 包装）
- [ ] Wiki per-doc map 缓存
- [x] VLM OCR/Caption 缓存
- [x] Wiki per-doc map 缓存（复用 LLM Cache，在 wiki_ingest_batch.go 包装）

---

## 9. Embedding 缓存实现详解

### 9.1 设计目标

在增量重构的基础上，进一步实现 **跨文档** 和 **跨重建** 的 Embedding 缓存：

| 场景 | 无缓存 | 有缓存 |
|------|--------|--------|
| 同一文档重构（内容未变） | 增量跳过（已实现） | 增量跳过（同上） |
| 同一文档重构（部分内容变化） | 变化的 chunk 重新调 API | 变化的 chunk 查缓存，命中则跳过 API |
| 删除文档后重新上传相同内容 | 全部重新调 API | **缓存命中，零 API 调用** |
| 不同文档包含相同文本段落 | 每个文档独立调 API | **跨文档去重，只调一次** |

### 9.2 架构设计

```
┌──────────────────────────────────────────────────────┐
│                  processChunks                        │
│                                                       │
│  embeddingModel = GetEmbeddingModel(modelId)          │
│       │                                               │
│       ▼                                               │
│  ┌──────────────────────────────────────────┐         │
│  │         CachedEmbedder (wrapper)          │         │
│  │                                          │         │
│  │  BatchEmbed(texts)                       │         │
│  │    ├─ 1. 查缓存 (Lookup)                 │         │
│  │    │   key = SHA256(text + model_id + dim)│        │
│  │    ├─ 2. 收集 miss 的 texts              │         │
│  │    ├─ 3. 调 inner.BatchEmbed(missTexts)  │         │
│  │    ├─ 4. 写缓存 (Store, async)           │         │
│  │    └─ 5. 合并 cached + fresh 结果        │         │
│  └──────────────────────────────────────────┘         │
│                     │                                 │
│                     ▼                                 │
│  ┌──────────────────────────────────────────┐         │
│  │         Inner Embedder (OpenAI/Ollama)    │         │
│  │  实际调用 embedding API                   │         │
│  └──────────────────────────────────────────┘         │
│                                                       │
│  ┌──────────────────────────────────────────┐         │
│  │         EmbeddingCache (GORM)             │         │
│  │  embedding_cache 表                       │         │
│  │  ┌─────────┬────────┬────────┬─────────┐ │        │
│  │  │cache_key│model_id│dims    │embedding│ │        │
│  │  ├─────────┼────────┼────────┼─────────┤ │        │
│  │  │a1b2c3.. │model-x │768     │[bytes]  │ │        │
│  │  │d4e5f6.. │model-x │768     │[bytes]  │ │        │
│  │  └─────────┴────────┴────────┴─────────┘ │        │
│  └──────────────────────────────────────────┘         │
└──────────────────────────────────────────────────────┘
```

### 9.3 数据库表

```sql
CREATE TABLE embedding_cache (
    cache_key   VARCHAR(64) PRIMARY KEY,   -- SHA256(text + model_id + dims)
    model_id    VARCHAR(64) NOT NULL,       -- 模型 ID（失效隔离）
    dimensions  INTEGER NOT NULL,            -- 向量维度（失效隔离）
    embedding   BYTEA NOT NULL,              -- 序列化的 []float32
    text_preview TEXT,                       -- 文本预览（调试用）
    created_at  TIMESTAMP DEFAULT NOW(),
    updated_at  TIMESTAMP DEFAULT NOW()
);
CREATE INDEX idx_embedding_cache_model ON embedding_cache(model_id);
```

### 9.4 缓存键设计

```
cache_key = SHA256(TrimSpace(text) + ":" + model_id + ":" + dimensions)
```

| 组成部分 | 作用 |
|---------|------|
| `TrimSpace(text)` | 归一化文本，首尾空白不影响缓存 |
| `model_id` | 模型隔离：换模型时自动 miss |
| `dimensions` | 维度隔离：同模型不同维度不共享 |

### 9.5 核心代码

**文件**: `internal/models/embedding/embedding_cache.go`

#### CachedEmbedder.BatchEmbed 流程

```go
func (c *CachedEmbedder) BatchEmbed(ctx context.Context, texts []string) ([][]float32, error) {
    modelID := c.inner.GetModelID()
    dims := c.inner.GetDimensions()

    // 1. 查缓存
    cached := c.cache.Lookup(ctx, texts, modelID, dims)

    // 2. 收集 miss
    missTexts := []string{}
    for _, text := range texts {
        k := cacheKey(text, modelID, dims)
        if _, hit := cached[k]; !hit {
            missTexts = append(missTexts, text)
        }
    }

    // 3. 调 API（仅 miss）
    var fresh [][]float32
    if len(missTexts) > 0 {
        fresh, _ = c.inner.BatchEmbed(ctx, missTexts)
        // 4. 写缓存（异步，不阻塞主流程）
        go c.cache.Store(context.Background(), missTexts, fresh, modelID, dims)
    }

    // 5. 合并结果
    return merge(cached, fresh, texts), nil
}
```

#### 透明集成

```go
// model.go - GetEmbeddingModel
embedder, _ := embedding.NewEmbedder(config, pooler, ollamaService)
// 用缓存包装（如果 cache 为 nil 则透传）
embedder = embedding.NewCachedEmbedder(embedder, s.embeddingCache)
return embedder, nil
```

### 9.6 集成点

Embedding 缓存在以下所有流程中自动生效：

| 流程 | 调用点 | 说明 |
|------|--------|------|
| 主 chunk 向量化 | `processChunks` → `BatchIndex` | 文档分块后生成向量 |
| 摘要 chunk 向量化 | `generateSummary` → `BatchIndex` | 文档摘要生成向量 |
| 问题生成向量化 | `generateQuestions` → `BatchIndex` | FAQ 问题生成向量 |
| 批量问题向量化 | `batchGenerateQuestions` → `BatchIndex` | 批量 FAQ 问题生成向量 |

### 9.7 序列化方案

`[]float32` ↔ `[]byte` 转换使用 `encoding/binary`：

```go
// 写入：每个 float32 → 4 字节 (little-endian)
func floatsToBytes(floats []float32) []byte {
    buf := make([]byte, 4*len(floats))
    for i, f := range floats {
        binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
    }
    return buf
}

// 读取：每 4 字节 → float32
func bytesToFloats(data []byte) []float32 {
    floats := make([]float32, len(data)/4)
    for i := range floats {
        floats[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
    }
    return floats
}
```

一个 768 维向量 = 3072 字节 ≈ 3KB，存储开销极小。

### 9.8 日志输出

```
[EmbeddingCache] model=d14b0c8e-... dims=768 total=4 hits=3 misses=1
```

- `total`: 本次需要 embed 的文本数
- `hits`: 缓存命中数（跳过 API 调用）
- `misses`: 缓存未命中数（需要调 API）

