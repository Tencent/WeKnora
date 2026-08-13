---
name: discover-weknora-tree-from-document-guide
description: 只使用 writing_system.get_document_outline 返回的文档级信息、全文写作指导和目录，不读取任何小节写作要点或正文，从 WeKnora 素材库发现、核验并归纳与整体写作任务相关的知识，生成独立可读的主题知识树。用于素材发现上下文对照实验的全文要点组；不进行章节映射、写作规划或正文生成。
---

# 仅依据全文写作要点发现 WeKnora 素材知识树

只完成素材发现：理解整体任务范围，从素材库发现相关知识，核验原始证据，并按知识主题组织结果。

把知识树作为独立中间产物。不要规划文章结构，不要说明知识应如何写入文章，不要生成或修改正文。

## 输入

要求调用方提供：

```yaml
kb_id: WeKnora 知识库 ID
output_dir: 本次运行的空输出目录
max_search_queries: 30
hybrid_match_count: 10
```

`max_search_queries` 是本次素材发现允许使用的搜索查询总数，默认 `30`；不统计素材列表、Wiki 状态检查和页面读取。`hybrid_match_count` 是每次 `hybrid_search` 最多返回的原始片段数，默认 `10`。

只在输入边界检查 `kb_id` 和 `output_dir`。不要猜测缺失值。如果输出目录已包含历史结果，停止并报告，避免实验污染。

## 写作上下文边界

只调用一次 `writing_system.get_document_outline`。

使用其返回的文档标题、产品构想、行业、全文写作指导和目录，形成整体任务视角。禁止调用 `get_section_brief`、`get_section_content` 或任何其他小节工具。

不要按章节或小节拆分检索任务。目录只用于理解整体任务边界，不用于组织知识树，也不建立知识到章节的映射。

把实际使用的 writing_system 字段和值保存到 `context.json`，标记：

```json
{"context_group":"document_guide","section_briefs_used":false,"section_content_used":false}
```

## WeKnora 工具边界

只使用：

- `list_knowledge`
- `get_knowledge`
- `get_wiki_build_status`
- `wiki_index_view`
- `wiki_search`
- `wiki_read_page`
- `hybrid_search`

不得上传、删除或修改 WeKnora 内容，也不得读取其他实验组的输出。

## 1. 建立整体任务视角

从允许的 writing_system 上下文归纳：

- 核心研究对象；
- 任务边界和明确排除项；
- 需要理解的主要知识领域；
- 需要确认的事实、数据、案例和证据；
- 可能存在争议或需要反证的问题。

只形成一个去重后的整体任务视角，不生成章节级知识需求，不记录章节映射。

## 2. 冻结素材快照

分页调用 `list_knowledge`，直到取得全部素材。每份素材都进入台账，包括未启用、解析失败、Wiki 未完成和最终判定无关的素材。

记录 `knowledge_id`、标题、文件名、文件类型、`file_hash`、`updated_at`、启用状态、解析状态、Wiki 状态和摘要。

对可处理素材调用 `get_wiki_build_status`。只有 `ready=true` 时才使用其 Wiki；不得把解析完成当成 Wiki 完成。调用一次 `wiki_index_view` 记录导航快照。运行中发现素材哈希、更新时间或 Wiki 状态变化时，停止并报告快照失效。

## 3. 发现候选知识

围绕整体任务视角执行知识发现，不围绕写作章节执行。把 `max_search_queries` 作为 `wiki_search` 与 `hybrid_search` 的合计查询预算，并在 `audit.json` 中逐次计数。

先进行宽检索，覆盖研究对象、同义词、核心技术、材料、工艺、应用场景、产业链、市场、竞争、风险和限制条件。使用 `wiki_search` 定位候选页，再用 `wiki_read_page` 阅读相关页面。

再从真实素材内容扩展主题，识别组成、因果、约束、替代、上下游、技术路线、性能边界和来源矛盾。扩展前确认新主题仍与整体任务相关。

最后主动搜索候选结论的反证、不同统计口径、成立条件和当前知识簇的缺口。不要因为某项结论符合预期就停止搜索反证。

## 4. 核验原始证据

Wiki 只用于发现和导航，不作为最终事实证据。每个准备收录的确定性知识点都必须调用 `hybrid_search` 回到原始片段，并传入 `match_count=hybrid_match_count`。

记录知识结论、来源素材、原文片段、定位、能够证明的内容、不能证明的内容、限制和冲突。无法定位原始片段时标记为 `unverified`，不得进入知识树主体。Wiki 与原始片段不一致时，以原始片段为准。

对每份素材标记：

- `metadata_only`：只读取元数据或摘要；
- `wiki_examined`：检查过相关 Wiki；
- `source_hit`：命中过原始片段；
- `unassessed`：因解析、Wiki 或工具限制尚未判断。

不得把没有检索命中判定为无关，也不得声称完整阅读了素材全文。

## 5. 归并并组织知识

区分 `fact`、`inference`、`assumption` 和 `unverified`。合并表达同一事实的多个片段，把不同来源共同挂在一个知识点下。

为素材分配 `D-001` 形式的文档 ID，为知识点分配 `K-001` 形式的知识 ID。所有输出保持 ID 一致。

先从实际素材中归纳主题，再组织为：

```text
主题
├─ 子主题
│  ├─ 知识点
│  │  ├─ 来源文档
│  │  └─ 来源文档
│  └─ 知识点
└─ 子主题
```

通常使用 `4–8` 个一级主题，树深不超过三层。不要按素材文件、检索顺序或写作目录组织。每个主题说明它是什么以及为何与整体任务相关；每个知识点说明知识结论、任务关联、来源依据、适用边界和冲突。不得出现写入某章、补强某节等写作规划表述。

## 6. 输出

只在 `output_dir` 写入：

```text
knowledge-tree.md
knowledge-tree.json
materials-index.md
evidence.jsonl
context.json
audit.json
```

`knowledge-tree.md` 是唯一主要阅读入口，依次包含发现概览、知识树总览、详细知识树、冲突与待确认事项、素材来源表。禁止展示查询日志、MCP 调用过程、内部评分、Prompt 或大段 JSON。

`knowledge-tree.json` 保持与 Markdown 相同的主题、文档和知识 ID，供后续 Agent 使用。

`materials-index.md` 列出全部素材、关联程度、贡献主题、覆盖状态和未采用原因，避免静默遗漏。

`evidence.jsonl` 保存规范化知识点和原始证据；`context.json` 保存实际使用的写作上下文、整体任务视角、实验组标记和素材快照；`audit.json` 保存查询计数、候选、去重、核验和取舍记录。

## 完成条件

- 全部素材均进入台账并具有覆盖状态；
- 知识树主体中的每个知识点都有原始证据；
- `knowledge-tree.md` 不依赖审计文件即可独立阅读；
- Markdown、JSON 和 JSONL 使用一致 ID；
- 未调用 `get_section_brief` 或读取任何小节写作要点；
- 未按章节或小节拆分检索；
- 未进行章节映射或写作规划；
- 未生成或修改正文。

