---
name: summarize-transcript-content
description: 根据单篇文档，生成一段 150～300 个汉字的快速概览。适用于帮助用户快速理解内容并判断是否值得深入学习；不用于代替完整的类型化智能总结。
---

# 生成文字稿内容概览

由后端直接调用 LLM，基于当前完整转写生成概览，方便用户快速了解内容。

## 输入

后端提供当前转写代次、完整分块清单和分块正文。该 Skill 不调用 WeKnora Agent 工具。

## 工作流程

### 一、读取与识别

1. 读取当前转写代次的完整分块，确认视频 ID、分块顺序和时间范围一致。
2. 识别文字稿的核心问题、内容推进方式、最高价值结论、目标读者和适合继续探索的方向。

### 二、筛选与生成

3. 只使用转写中有明确证据的关键结论。
4. 按 [references/overview-template.md](references/overview-template.md) 生成"快速概览"模块：先写 150～300 个汉字的自然中文总结。

## 强制规则

- 不得引入不存在的事实。
- 标记问题的 Wiki 页面和知识原子不得被引用。

## 输出

后端将快速概览保存为 WeKnora 内容页。结构见 [references/overview-template.md](references/overview-template.md)。

写入契约：工具 `page_type` 用 `index`；页面 frontmatter 必须含 `type: overview`、`source_video_id: {视频ID}` 与 `transcript_generation: {转写代次}`。由 vidsage 内容流水线触发时，页面 slug 固定为 `overview/{视频ID}`，不得使用视频标题或其他产物 slug。

禁止输出工作执行结果、筛选来源和字数要求；概览内容不得落本地文件或仅以文字回复。
