# Wiki 页面契约

本 Skill 的产物全部以 WeKnora 知识库 Wiki 页面形态存在。本地 `wiki/` 目录及子目录、`wiki/index.md` 不再生成。每个知识原子、每个实体与视频索引页各对应一个独立 Wiki 页面，通过 **创建/覆盖 Wiki** 工具写入。

## 数据获取

| 工具 | 获取内容 |
|---|---|
| **精读源文档** | 段落原文、说话人、时间戳、证据片段 |
| **查看文档分块** | 文档分块索引与内容（精确定位原文范围） |
| **阅读 Wiki 页面** | 已有知识原子、实体、视频分类 Wiki 页面 |
| **查询知识图谱** | 已有实体关系 |

## frontmatter

每个 Wiki 页面以 YAML frontmatter 开头：

```yaml
---
id: V002-K005              # 知识原子或实体 ID
type: concept              # primary_type 或 entity_type
source_video_id: V002      # 视频 ID（编排器据此归属页面）
title: 从智能到智慧         # 人类可读标题
aliases: []                # 实体的 aliases 字段
tags: [智能, 智慧, AI应用]  # tags 字段
audit_status: aligned       # 审计状态
---
```

字段定义见 [type-frameworks.md](type-frameworks.md) 的知识原子与实体章节。`type` 必须属于 `methodology` / `case` / `concept` / `insight` 或 `person` / `organization` / `product` / `technology` / `industry` / `place` 之一。

## 视频索引页契约（关键）

视频索引页是编排器回写视频状态所依赖的产物，必须满足：

1. **写工具 `page_type` 参数用 `index`**（不是 entity/concept/summary）。
2. frontmatter `type` 固定为 `knowledge_base`。
3. frontmatter 必须含 `source_video_id`。

```yaml
---
type: knowledge_base
source_video_id: V002
title: {视频标题}_知识底座
audit_status: aligned
---
```

## 页面正文顺序

知识原子页面：

1. 一级标题（`title`）
2. 核心内容（`core_content`）
3. 结构维度（按 `primary_type` 展示对应结构字段的子字段，标签使用中文；详见 [type-frameworks.md](type-frameworks.md)）
4. 时间范围（`HH:MM:SS–HH:MM:SS`，从证据 ID 反查分块得到）
5. 证据 ID（`evidence_ids`）
6. 信息性质（`information_nature`）
7. 关联知识（`related_atom_ids`，Wiki 双链）
8. 关联实体（`related_entity_ids`，Wiki 双链）

实体页面：

1. 一级标题（`canonical_name`）
2. 别名（如有）
3. 一句话概述（`description`）
4. 关键信息维度（按实体类型对应子字段，标签使用中文；详见 [type-frameworks.md](type-frameworks.md)）
5. 证据 ID（`evidence_ids`）
6. 关联知识（`source_atom_ids`，Wiki 双链）

视频索引页：视频标题、主类型 / 次类型 / 审计状态、知识对象数量统计、全部实体的 Wiki 双链索引、全部知识原子按类型分组的 Wiki 双链索引、视频概要、必要的上游审计警示。

## 双链规则

使用 WeKnora 原生 Wiki 引用（`[[汪天凡]]`、`[[从智能到智慧]]`），与 [assemble-transcript-page 的链接优先级](../../assemble-transcript-page/references/page-contract.md) 一致。禁止本地文件路径、纯文本 ID、HTML 锚点、外部链接。带显示文本：`[[汪天凡|受访者]]`。

## 命名规则

- 实体 Wiki 页面名：`canonical_name`。
- 知识原子 Wiki 页面名：`title`；非法字符由 **创建/覆盖 Wiki** 工具按平台规则处理。
- 视频索引页 Wiki 页面名：`{视频标题}_知识底座`。

## 其他规则

- 索引页不得宣称局部候选对象已拥有全局身份（全局 ID 由 `$build-video-knowledge-graph` 分配）。
- 不得合并全库同名实体，不得修改其他视频页面。
- 写入前 **阅读 Wiki 页面** 检查目标页面是否已存在；存在时按 SKILL.md 降级规则处理。
- 写入校验项见 [audit-rules.md](audit-rules.md) 的"Wiki 写入校验"小节。