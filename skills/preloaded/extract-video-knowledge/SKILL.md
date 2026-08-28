---
name: extract-video-knowledge
description: 从单个视频的源文档中，按统一知识本体提取实体、方法论、案例、概念和洞察，绑定原文证据并写入知识库 Wiki 页面作为该视频的局部知识底座。适用于人物访谈、培训课程、沙龙分享或通用视频的单视频知识底座生成和内容审计；不用于全库实体归并、跨视频图谱或用户问答。
---

# 提取单视频知识

将单个视频的源文档按统一知识本体转换为知识库 Wiki 页面。本 Skill 不读本地文件、不生成本地 Markdown。

## 输入

可用工具：**精读源文档**、**查看文档分块**、**阅读 Wiki 页面**、**搜索 Wiki**、**查询知识图谱**、**查看 Wiki 问题**、**标记 Wiki 问题**、**更新 Wiki 问题**、**创建/覆盖 Wiki**

生成前完整阅读 [references/type-frameworks.md](references/type-frameworks.md)、[references/wiki-schema.md](references/wiki-schema.md)、[references/audit-rules.md](references/audit-rules.md)。

## 工作流程

### 一、输入校验

1. **精读源文档** + **查看文档分块** 确认源文档就绪：段落原文、时间戳、章节边界、分块索引。
   - 调用「查看文档分块 / list_knowledge_chunks」时，`limit` 必须 ≤ 100，工具默认 20；**不要一次请求全部 chunk**。
   - 文档分块超过一页时，必须用 `offset` 翻页，每一页继续读取直到覆盖所有分块。
   - 其他工具在参数说明里标注了 `maximum` 的字段（如搜索上限、分块上限），一律严格按上限调用，超出部分靠翻页完成，不得试图一次拉全量。
2. **阅读 Wiki 页面** 检查视频是否已有提取产物，避免重复提取；已存在时按降级规则处理。
3. **阅读 Wiki 页面** 获取视频分类 Wiki 页面（主类型、次类型、分类理由、置信度），不读本地 `video-profile.json`。
4. **查询知识图谱** 获取已有实体关系，避免重复建边。

### 二、知识提取

5. 覆盖所有实质话题、转折、论点、案例、方法论、限制和未决问题；纯寒暄、广告、结束语排除并记录章节 ID 与排除原因。
6. 按统一知识本体提取四类知识原子与六类实体，遵循 [type-frameworks.md](references/type-frameworks.md) 的原子化测试与边界判定规则。
7. 原子包含多个独立命题时拆分，拆分后用 `contradicts` / `complements` / `explains` / `example_of` / `part_of` 等关系连接。
8. 每个关键知识单元通过 **精读源文档** 关联 1～3 个最小充分证据，标记 `information_nature`。
9. 建立视频内部类型化关系。

### 三、写入 Wiki

10. 每个知识原子 → **创建/覆盖 Wiki** 写入独立 Wiki 页面。
11. 每个实体 → **创建/覆盖 Wiki** 写入独立 Wiki 页面（页面名 = `canonical_name`）。
12. 视频索引页 → **创建/覆盖 Wiki** 写入知识库索引页（写工具 `page_type` 参数必须用 `index`、frontmatter `type` 必须为 `knowledge_base`），承担原 `wiki/index.md` 的导航职责。
13. 页面之间引用使用 WeKnora 原生 Wiki 引用（`[[xxx]]`），页面契约见 [wiki-schema.md](references/wiki-schema.md)。

### 四、审计

14. 按 [audit-rules.md](references/audit-rules.md) 完成提取审计与内容质量审计。
15. 审计未通过时 **标记 Wiki 问题** 在对应页面记录详情，不得将产物标记为完成。
16. 审计通过后 **更新 Wiki 问题** 关闭上游遗留问题。

## 边界

- 全库实体归并、跨视频关系、全局 Wiki 由 `$build-video-knowledge-graph` 负责。
- 概览、前台大纲、类型化智能总结是下游视图，不得在本 Skill 重复生成。

## 降级规则

- WeKnora 服务不可用：报告失败并保留草稿，不得伪造已落库。
- **创建/覆盖 Wiki** 覆盖语义前必须确认上游产物稳定；版本混乱时通过 **阅读 Wiki 页面** 比对。
- 已存在提取产物：按版本回滚或覆盖规则处理，不得无声覆盖。
- 上游审计为 `conditional`：索引页页首显示警示并链接完整审计。
- 上游审计为 `failed`：不发布为可信页面，仅交付带明显标识的修复预览。

## 不可违反的规则

- 一个知识原子只表达一个主要命题；多命题拆开后必须通过关系连接。
- 每个知识原子和每个实体必须生成独立 Wiki 页面。
- 所有产物必须通过 **创建/覆盖 Wiki** 写入知识库，不得落本地文件。
- 关键词相邻、归纳语言、外部事实核验、失败状态记录等规则见 [audit-rules.md](references/audit-rules.md)。