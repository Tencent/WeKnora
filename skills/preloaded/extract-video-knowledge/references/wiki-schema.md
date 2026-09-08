# Wiki 候选与规范页面契约

本文件定义 V2 Skill 的候选提交、证据贡献、规范页面、关系和写入顺序。五类字段见 [type-frameworks.md](type-frameworks.md)，质量状态见 [audit-rules.md](audit-rules.md)。

第一阶段沿用写入工具当前的 `p3-wiki-object/v1` 入参外壳，但语义已经收敛为“Skill 提交候选，服务端确定规范身份”。入参中的对象 ID、标题和 slug 都是候选建议；工具返回值才是最终身份。

## 1. 一对象一规范页

一个账户空间和知识库内，一个语义对象只对应一个规范知识对象 ID 和一张规范 Wiki 页面。视频不是对象身份的一部分；不同视频讲述同一对象时，只增加证据贡献。

规范页面保存规范标题、类型、结构内容、关系和证据贡献引用。证据正文、原文和检索分块只保存在证据知识库，Wiki 只保存 ID 与时间范围。

Skill 每次只形成当前视频当前转写代次的一组贡献，不读取或拼装其他视频的贡献，也不决定合并目标。

## 2. 候选提交

每个 `passed` 候选通过 `wiki_write_page` 提交完整页面提案。写入参数 `page_type` 使用 `index`，业务类型写入 `type` 和同值的 `primary_type`。

| 分组 | 字段 |
|---|---|
| 候选身份 | `id`、`knowledge_object_id`、`title`；可选 `aliases`、`canonical_name` |
| 类型 | `type`、`primary_type`、实体所需的 `entity_sub_type`、`information_nature` |
| 当前贡献兼容字段 | `source_video_id`、`source_document_id`、`transcript_generation`、`evidence_ids`、`source_refs`、`chunk_refs`、`time_range`、`field_evidence` |
| 质量与内容 | `audit_status`、`classification_confidence`、`core_content`、`structure_fields` |
| 规范写入输入 | `evidence_contribution`、`related_content`、`relations` |

约束：

- `id` 与 `knowledge_object_id` 首次提交时都填写候选 ID，只为兼容当前工具入参；不得把它当作最终规范对象 ID。
- `title` 是规范标题建议。标题、唯一 H1 和实体 `canonical_name` 在提交时同值，且不含类型装饰。
- `primary_type` 与 `type` 同值；`page_type` 固定为 `index`。
- `classification_confidence` 是 YAML 数值，取值为 `0 < x <= 1`。
- `core_content` 是 frontmatter 顶层非空字符串，并与正文一句话概述同义。
- `structure_fields` 只使用 [type-frameworks.md](type-frameworks.md) 对应类型的键；每个非空字段都有同名 `field_evidence`。
- 当前贡献兼容字段与 `evidence_contribution` 必须表达同一视频、源文档、代次和证据集合。Skill 每次只提交当前视频当前代次的一组单数 `evidence_contribution`；服务端写入规范页时统一转换为 `evidence_contributions` 列表，并按“视频 + 转写代次”覆盖或追加。兼容字段不再作为新页面的事实来源。
- `related_content` 和 `relations` 首次提交为空；关系目标必须等所有对象返回规范身份并回读后再补写。

## 3. 证据贡献

`evidence_contribution` 是当前视频当前转写代次对候选对象的版本化引用。它是写入请求的单数组件；规范 Wiki 页面落盘后使用同结构的 `evidence_contributions` 列表：

```yaml
evidence_contribution:
  source_video_id: V002
  source_document_id: doc-V002
  transcript_generation: generation-20260907
  evidence_ids: [ev-004]
  chunk_refs: [chunk-004]
  time_range: 00:01:02-00:01:10
  field_evidence:
    definition: [ev-004]
  quality_status: passed
```

规则：

- `source_document_id` 与 `source_refs` 的唯一值相同；证据、分块和 Wiki 页面 ID 不能代填源文档 ID。
- `evidence_ids` 包含 1–3 个当前代次的最小充分证据；`field_evidence` 只能引用本组 `evidence_ids`。
- `chunk_refs` 与证据一一可回查，`time_range` 能定位视频播放位置。
- 同一视频同一代次重跑时，服务端覆盖该组贡献；跨视频提交时，服务端新增贡献。
- Skill 不提交证据正文，不汇总 `evidence_contributions`，也不删除其他来源贡献。
- 服务端必须保留规范页的对象 ID、规范标题和页面 ID；合并时不得把当前候选正文覆盖为另一视频的证据正文。

## 4. 写入工具返回

服务端对候选执行召回、语义判定和规则冲突检查，返回以下结果之一：

| 结果 | 含义 | Skill 后续动作 |
|---|---|---|
| `created` | 创建新的规范身份和规范页 | 回读并使用返回身份 |
| `reused` | 复用已有规范身份和规范页，更新当前贡献 | 回读并使用返回身份 |
| `review_required` | 同名异义、类型冲突、多目标命中、事实冲突或语义不确定 | 标记问题并停止该候选 |

成功结果必须包含 `knowledge_object_id`、`canonical_wiki_page_id`、规范 `title` 和 `slug`。若当前工具返回字段名仍为 `wiki_page_id`，将其视为 `canonical_wiki_page_id` 的兼容名称。任何返回字段为空或回读不一致都视为写入失败。

Skill 不根据搜索结果直接覆盖页面，不从标题推测页面 ID 或 slug，不在 `review_required` 后换一个 slug 新建页面。

## 5. 页面正文

规范页面正文按以下顺序展示：唯一 H1、一句话核心内容、实际有值的结构维度、全部有效来源的视频名称与可点击时间戳、中文类型、正式关系、阅读关联。

普通用户页面不展示视频 ID、源文档 ID、证据 ID、分块 ID、转写代次、原始 JSON 或模型过程。需要展示原文时，按证据 ID 从证据知识库读取。

Skill 提交的正文只包含当前候选有证据支持的内容；已有规范页的内容合并、字段证据重算和其他贡献保留由服务端完成。

## 6. 正式关系

| 中文 | `relation_type` | 方向 | 含义 |
|---|---|---|---|
| 包含于 | `part_of` | A → B | A 是 B 的组成部分或子知识 |
| 解释 | `explains` | A → B | A 说明 B 的原理、原因或含义 |
| 是……的案例 | `example_of` | A → B | A 是概念或方法 B 的实例 |
| 应用于 | `applies_to` | A → B | 方法或概念 A 被用于对象或场景 B |
| 支持 | `supports` | A → B | A 为判断 B 提供依据 |
| 反驳 | `contradicts` | 双向 | A 与 B 对同一问题的判断冲突 |
| 补充 | `complements` | 双向 | A 与 B 共同形成更完整解释 |
| 涉及 | `involves` | A → B | 案例、方法或洞察 A 涉及实体 B |

每条关系包含 `relation_id`、`relation_type`、`target_object_id`、`target_wiki_page_id`、`evidence_ids`、`time_range` 和 `confidence`。两端都是 `passed` 对象，目标只使用写入工具返回并回读确认的规范身份。相邻出现、标题相似、关键词重合和普通双链不构成正式关系。

## 7. 两阶段写入

1. 提交不带关系的完整候选和当前 `evidence_contribution`。
2. 记录工具返回的结果、判定依据、冲突字段和规范身份。
3. 对 `created` 或 `reused` 结果，按返回页面 ID 与 slug 回读。
4. 全部关系目标可读后，使用返回的规范身份补写正式关系和阅读关联，再次回读。
5. `review_required` 或目标失败时跳过对应写入并标记问题。
6. 最后写视频索引页，且只引用已确认的规范页面。

## 8. 视频索引页

索引页 slug 为 `video/{视频ID}`，写入参数使用 `page_type: index`：

```yaml
---
page_type: index
type: knowledge_base
source_video_id: V002
source_document_id: doc-V002
transcript_generation: generation-20260907
title: "{视频标题}_知识底座"
audit_status: aligned
source_refs: [doc-V002]
---
```

索引页包含视频分类、五类数量、规范页面索引、视频概要和审计警示。它不设置 `primary_type`，也不进入五类列表或 Graph。索引只能使用工具返回的规范页面身份，同一规范页面不得因多个候选或贡献重复出现。
