---
name: generate-weknora-fengshi-advice
description: 定时发现 WeKnora 知识库中新建或更新且已完成 Wiki 联立的文档，将知识映射到锋矢写作系统的相关小节，并把各小节分发为上下文隔离的并行子任务；每个小节自主组合直接写作、启发式写作、检索、评审和修稿，不等待其他小节，在有限评审预算内生成可追溯的优化建议、候选正文或新小节提案。用于 Hermes 的定时写作建议任务、指定知识文档回填测试，以及根据 WeKnora 知识批量改进锋矢文档时。
---

# 生成 WeKnora × 锋矢写作建议

把 Hermes 作为唯一编排器。WeKnora 提供知识发现、Wiki 综合和原文证据；锋矢提供目录、可选的启发式写作和候选正文评审；本地建议目录是唯一输出边界。每次调用只执行一轮定时检查，不在两次定时触发之间休眠。不要预设固定写作流水线，让每个小节代理根据当前稿件和反馈自主选择下一步。

## 接收运行参数

要求调用方提供：

- `kb_id`：WeKnora 知识库 ID。
- `output_dir`：建议文件根目录。
- `mode`：`run`、`bootstrap` 或 `backfill`。
- `knowledge_ids`：仅 `backfill` 使用，指定要重新分析的知识文档 ID。
- `max_sections`：每个知识文档最多处理的小节数，默认 `5`。
- `max_parallel_sections`：同时运行的小节子任务数，默认 `3`；不要无限并发。

若 `mode` 未提供：状态文件存在时使用 `run`；状态文件不存在时使用 `bootstrap`。不要猜测 `kb_id`。

## 检查工具契约

开始前确认以下 MCP 工具可用：

- WeKnora：`list_knowledge`、`get_wiki_build_status`、`wiki_search`、`wiki_read_page`、`hybrid_search`。
- 锋矢：`get_document_outline`、`get_section_content`、`get_section_review`、`start_heuristic_writing`、`continue_heuristic_writing`、`review_subsection_content`、`get_task_status`。

若 `review_subsection_content` 缺失，停止并报告 MCP 工具白名单可能仍配置为旧的 `review_section`。不要把未评审内容标记为已评审。

`review_subsection_content` 是同步长调用，正常耗时约 3–5 分钟。要求 Hermes MCP 客户端及其间所有 HTTP 代理的单工具超时不少于 600 秒。等待期间保持当前调用，不要因为暂时没有输出而取消、重复提交或并行提交同一候选正文。

启发式写作是可选工具，不是必经步骤。选择使用时，Hermes 可以自主完成五轮问答，但必须始终复用 `start_heuristic_writing` 返回的同一个 `session_id`，不得在同一次五轮会话中重复启动。回答只能来自当前正文、写作要点和已核验的 WeKnora 证据；缺少事实时明确回答“现有知识不足，作为待确认项保留”，不得编造业务数据。

## 维护运行状态

在 `output_dir/state.json` 维护状态。记录每个知识文档的：

```json
{
  "knowledge_id": "...",
  "file_hash": "...",
  "updated_at": "...",
  "wiki_version": 0,
  "status": "waiting_wiki|saved|skipped|failed",
  "processed_at": "...",
  "run_id": "..."
}
```

用 `knowledge_id + file_hash + updated_at` 判断新建或更新。用以下组合避免重复生成：

```text
Fengshi document id + node_id + section updated_at/content hash
+ knowledge_id + knowledge file_hash + wiki_version
```

一次只运行一个协调器实例。小节子任务可以并行，但每个子任务只能写自己的临时小节目录，禁止子任务修改 `state.json`、最终建议文件或总 `manifest.json`。每个小节最多评审 3 次；优先由调度器禁止定时任务重叠。成功汇总全部子任务结果后再由协调器更新状态；失败时保留原始错误并标记对应小节，不要丢弃其他成功结果。

## 执行工作流

### 1. 发现知识

调用 `list_knowledge(kb_id)` 并分页读取完整列表。

- `bootstrap`：把现有文档登记为基线，不生成建议。
- `run`：只处理相对状态文件新增或变化的文档，并重新检查 `waiting_wiki` 文档。
- `backfill`：只处理 `knowledge_ids` 指定的文档，不受已处理状态限制。

忽略未启用的文档。不要把 `parse_status=completed` 当成 Wiki 已完成。

### 2. 等待 Wiki 完成

对候选文档调用：

```text
get_wiki_build_status(kb_id, knowledge_id)
```

仅在 `ready=true` 时继续。`pending` 或 `processing` 记为 `waiting_wiki`；`failed`、`cancelled` 或 `disabled` 保留服务端原因并结束该文档本轮处理。

### 3. 由协调器建立小节任务

每轮只调用一次 `get_document_outline`。只选择 `node_type=subsection` 的二级小节；所有 `node_id` 必须来自本次返回的目录。

协调器仅使用知识标题、摘要、Wiki 搜索摘要和小节标题做初筛，选出不超过 `max_sections` 个候选小节。不要在协调器上下文中累计所有小节正文、完整 Wiki 页面、启发式问答和评审全文。

在系统临时目录下为本次运行创建工作目录，并为每个候选小节创建独立的 `work-packet.json`。临时工作目录不属于最终 `output_dir`。任务包只记录：

- `run_id`、`kb_id`、`knowledge_id`、`knowledge_file_hash`、`wiki_version`；
- 锋矢文档 ID、`node_id`、标题；
- 推荐检索问题和 Wiki 页面 slug；
- 该小节专属临时输入、输出目录。

把每个小节分发为独立子任务，最大并发数为 `max_parallel_sections`。子任务只接收临时 `work-packet.json` 路径，不传入其他小节正文，也不继承协调器累计的长文本。每个子任务独立运行到完成或触发停止条件，不等待其他小节。结束时只向协调器返回 `node_id`、状态、最佳评分、最终采用轮次和临时结果文件路径。

### 4. 并行执行独立小节任务

每个小节子任务只需满足以下目标，不规定完成顺序：

- 调用 `get_section_content` 取得本小节写作要点、当前正文和更新时间；需要时读取历史评审，但不要把它当成本轮评审。
- 使用 `wiki_search`、`wiki_read_page` 和 `hybrid_search` 获得足够且可追溯的证据，并完成最终相关性判断。
- 根据材料形成比当前正文更好的候选内容。
- 按需使用直接写作、启发式写作、补充检索和评审反馈改稿。
- 在停止前保留评分最高且证据可靠的版本。

知识库可能混合多个主题。Wiki 只用于组织和综合；每个确定性事实都必须用 `hybrid_search` 回到原始片段，并检查 `knowledge_id` 或来源引用是否指向本轮触发文档。无法落到可靠来源的内容改写为待验证假设。子任务确认不相关时写出 `skipped` 结果，不强行生成。

各小节之间没有轮次同步。任一小节可以按自己的判断写作、送审、修订或再次调用启发式写作，不受其他小节进度和工具选择影响。协调器只在所有小节最终结束后汇总结果。

### 5. 按需使用启发式写作

由小节代理自行判断是否使用启发式写作。可以完全根据 WeKnora 素材直接写作，也可以在初稿前、形成初稿后、收到某轮评审后或局部修订后调用启发式写作。允许直接修改和启发式重构交替出现，不要求固定顺序。

每次调用 `start_heuristic_writing(node_id, content, instruction)` 时，`content` 必须是当时希望处理的完整正文快照，不要只传检索片段。一次五轮会话结束后，如后续评审显示确有必要，可以基于最新完整正文开启新的启发式会话。

启发式写作返回 `status=ask` 时：

1. 理解 `text` 中的问题；
2. 优先从本小节已收集证据回答，必要时继续调用 WeKnora 检索；
3. 使用同一个 `session_id` 调用 `continue_heuristic_writing`；
4. 连续完成全部五轮；
5. 只有 `status=draft` 且 `workflow_complete=true` 时才接受 `text` 为候选正文。

若问题需要知识库中不存在的客户访谈、成本或性能数据，明确说明未知并要求草稿保留占位项。不要为了完成五轮而补造数值。若返回异步 `task_id`，只对该启发式任务使用 `get_task_status`，取得结果后继续同一 `session_id`。启发式写作不会写回锋矢正文。

若知识揭示目录缺口，只生成新小节提案，注明建议父章节和插入位置。锋矢 MCP 当前没有创建目录节点的工具，不要声称已经创建章节。

### 6. 把评审作为反馈工具

评审是帮助代理判断稿件问题的反馈工具，不是固定流程控制器。小节代理可以在认为候选正文已值得评估时调用 `review_subsection_content`；收到反馈后，可以直接修稿、补充检索、调用启发式写作重构，或判断继续修改没有价值而停止。

每个小节最多调用 3 次正文评审。记录每次送审正文、评分和建议，并始终保留最高分版本。评分相同或下降、没有可执行修改、修改必须依赖不存在的业务事实，或已经使用 3 次评审时停止。不要用降分的最后版本覆盖最佳版本。

同一轮内并行不同小节，同一个小节不得并发评审。单次评审正常耗时约 3–5 分钟；不要用 `get_task_status` 轮询同步评审。达到 600 秒仍超时时，保存原始错误并停止该小节本轮处理，不要立即重复提交。

### 7. 保存建议

使用以下结构：

```text
output_dir/
  state.json
  <fengshi-document-id>/
    <run-id>__<knowledge-id>/
      manifest.json
      node-1.1.md
      node-1.3.md
      new-section-proposals.md
```

`run-id` 使用不含冒号的本地时间。每个小节最终只输出一个 `node-<id>.md`：

```markdown
---
document_id: 2232
node_id: "1.1"
knowledge_id: "..."
knowledge_file_hash: "..."
wiki_version: 0
section_updated_at: "..."
status: pending
---

# 小节标题

## 新知识摘要
## 与本小节的关联
## 当前内容问题
## 优化建议
## 候选正文
## 证据与来源
## 待人工确认的数据
## 启发式写作记录
## 锋矢评审迭代记录
```

每个子任务只写自己的临时工作目录，并通过 `result.json` 返回紧凑结果。协调器不要把完整正文和评审重新载入主上下文，只读取状态、评分、最佳轮次和路径；最后把最高分版本整理为对应的 `node-<id>.md`。

由协调器在所有小节子任务最终结束后写总 `manifest.json`，记录运行参数、处理的小节、跳过原因、来源索引、启发式写作 `session_id`、每轮正文哈希与评分、最终采用轮次和幂等键。成功发布最终文件后删除本次临时工作目录；失败时可保留临时目录用于诊断，但不要复制进最终输出目录。只写入 `output_dir`，不修改锋矢正文，不上传新知识，不改写 WeKnora Wiki。

## 返回运行摘要

简短返回：发现的新建/变化文档数、等待 Wiki 数、已分析文档数、生成建议数、跳过数、失败数及输出路径。没有变化时明确返回“没有发现可处理的新知识”，不要重新生成旧建议。

## 定时调用示例

```text
使用 $generate-weknora-fengshi-advice 执行一轮自动写作建议检查。
mode=run
kb_id=5a529583-53d9-4cd7-84a5-be21be4ccbb4
output_dir=D:\fengshi-suggestions
max_sections=5
max_parallel_sections=3
```

首次验证指定静电卡盘资料时使用 `mode=backfill` 并传入对应 `knowledge_ids`。
