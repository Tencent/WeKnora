---
name: video-content-skills-index
description: 视频内容生产 Skill 索引。先读取本文件，再按任务加载对应 Skill 和必要的 reference。
---

# 视频内容 Skill 索引

## 公共契约

所有 Skill 使用当前激活的转写版本：

- `source_video_id`
- `transcript_generation`
- `video_type`
- `transcript_chunk_count`
- `transcript_chunks[]`

每个分块至少包含：

- `chunk_id`
- `chunk_index`
- `start_seconds`
- `end_seconds`
- `speaker`
- `text`

统一约束：

- 所有分块属于同一视频和同一转写代次。
- 分块索引连续且数量完整。
- 转写正文存储在 WeKnora。
- 不使用本地文件、SRT URL 或单个首块 ID 代替完整转写。
- 发现输入缺失或代次不一致时停止处理。

## Skill 索引

| Skill | 作用 | 执行方式 | 输出 |
|---|---|---|---|
| `extract-video-knowledge` | 提取实体、方法、案例、概念和洞察 | WeKnora Agent + 工具 | Wiki 知识页面 |
| `generate-transcript-outline` | 生成章节导航 | 后端 + LLM | 结构化大纲 |
| `summarize-transcript-content` | 生成快速概览 | 后端 + LLM | 结构化概览 |
| `generate-typed-transcript-summary` | 生成类型化总结 | 后端 + LLM | 结构化总结 |

## 渐进式读取

1. 先读取本索引。
2. 根据任务只读取一个对应 Skill。
3. 只读取该 Skill 列出的 reference。
4. 不加载其他 Skill 的完整内容。
5. 基础内容 Skill 不调用 WeKnora Agent。
6. 知识提取 Skill 保留 WeKnora Agent 和工具调用。
