---
name: assemble-transcript-page
description: 将已审计的文字稿概览、内容大纲、类型化智能总结、相关知识、相关文字稿和证据说明组装为一个前台可见的单篇文字稿页面。适用于发布文字稿详情页、在上游总结或图谱更新后局部重组页面，或校验前台阅读顺序和降级状态时使用。
---

# 组装单篇文字稿前台页面

只组装已有产物，不在本 Skill 中生成新结论或新关系。

## 输入

可用工具：**阅读 Wiki 页面**、**搜索 Wiki**、**查询知识图谱**、**查看 Wiki 问题**、**创建/覆盖 Wiki**

组装前完整阅读 [references/page-contract.md](references/page-contract.md)。

## 组装顺序

按 [references/page-contract.md](references/page-contract.md) 模板顺序组装。

## 工作流程

### 一、输入校验

1. 通过 **阅读 Wiki 页面** 核对所有输入 Wiki 页面的文字稿标识和版本一致性，确认时间轴与大纲章节数一致。

### 二、页面组装

2. 保留上游产物的字段意义和审计警示，只做展示层排版。
3. 只展示图谱中 `confirmed` 的关系，并给出一句关系理由；链接优先级见 [references/page-contract.md](references/page-contract.md)。

### 三、检查与输出

4. 通过 **创建/覆盖 Wiki** 工具将组装后的完整页面写入知识库的 Wiki 页面。写入契约：工具 `page_type` 用 `index`；页面 frontmatter 必须含 `type: transcript_page`、`source_video_id: {视频ID}` 与 `transcript_generation: {转写代次}`。由 vidsage 内容流水线触发时，页面 slug 固定为 `transcript-page/{视频ID}`，不得使用视频标题或其他产物 slug；读取上游页面必须使用 Wiki 工具返回的实际 slug，不得自行推导。
5. 检查链接、标题层级、时间范围和降级提示。

## 降级规则

- 无图谱：显示"尚未建立跨文字稿关联"。
- 图谱节点尚无独立页：链接其已审计的来源片段，不回退到无定位的整篇详情。
- 无原文定位链接：显示时间范围，不宣称可跳转。
- 上游审计为 `conditional`：在页首显示简明警示并链接完整审计。
- 上游审计为 `failed`：不得发布为可信页面，只能交付带明显标识的修复预览。
