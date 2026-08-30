---
name: generate-typed-transcript-summary
description: 根据视频类型和当前完整转写，按人物访谈、培训课程、沙龙分享或通用模板生成完整智能总结。知识原子、实体和关系仅作为可选增强输入。
---

# 生成类型化文字稿总结

该产物服务深入学习，不得用快速概览替代。

## 输入

本 Skill 由后端直接调用 LLM，不读取本地文件，不调用 WeKnora Agent 工具，也不修改输入数据。模板由本 Skill reference 统一定义，不写入 WeKnora。

### 通过后端输入获取

- 段落原文、说话人、时间戳、证据片段
- 语义章节、章节摘要、topic_tags、证据 ID
- 前台聚合章节（可选，用于展示）

### 可选知识增强输入

- 知识原子（用于反向溯源）
- 实体（用于按人名/机构名检索）
- 关系（可选，用于关联扩展）

知识增强输入缺失时，仍必须基于完整转写生成初版总结。

生成前完整阅读 [references/summary-frameworks.md](references/summary-frameworks.md) 和 [references/entity-atom-structures.md](references/entity-atom-structures.md)。

## 工作流程

### 一、类型确定

1. 综合视频类型、完整转写和可选知识增强输入，分析内容的主类型、次类型、分类理由和置信度。
2. 主类型置信度不足时采用 `general`，同时保留可能的候选类型；不得强套专属模板。

### 二、内容组织

3. 使用可选知识增强输入补充结构维度，不得因其缺失阻塞初版总结。
4. 按对应框架的标准二级标题和顺序组织知识；标题文案不得自由改写，只可在标准标题下增加三级小节。利用知识原子的结构维度组织正文：方法论的 `steps`/`criteria` 直接支撑方法章节，案例的 `context`/`outcome` 支撑案例章节，概念的 `definition`/`distinction` 支撑概念解释，洞察的 `claim`/`qualifications` 支撑观点呈现和适用边界。
5. 明确区分原文观点、忠实概括、跨段归纳和分析推断。

### 三、输出

6. LLM 只能返回结构化 JSON：`schemaVersion`、`videoType`、`sections`。`sections` 必须严格遵循 `references/summary-frameworks.md` 的标题和顺序；每个 block 必须是纯文本，并提供 `evidenceChunkIds`。
7. 后端根据真实转写分块回填原文、起止时间和时间戳，校验通过后将带 `evidence` 的规范化 JSON 保存为 WeKnora 内容页。页面 frontmatter 必须含 `type: typed_summary`、`source_video_id` 与 `transcript_generation`。

### 四、审计

8. 校验总结：确认 JSON Schema、主类型模板、证据分块和转写代次完整；校验失败时不得将总结标记为完成。
9. 审计发现事实缺失、来源不足或模板偏差时，通过 **标记 Wiki 问题** 在总结页面标记问题详情。
10. 审计通过后，通过 **更新 Wiki 问题** 将上游遗留问题状态更新为已解决。

## 强制规则

- 总结长度由实质知识量决定，不设统一字数下限来鼓励注水。
- 不得把个人经验改写为普遍建议。
- 模板字段没有来源支持时应省略或说明缺失，不得补齐套话。
- 标准二级标题是输出契约，不是选题建议。某章证据不足时保留标题并简明说明缺失，不得删除、合并或改名该章。
- 主类型模板决定二级章节；次类型内容只能作为相关章节下的三级小节，不能改写主骨架。
- 不得将审计结果作为输出内容
