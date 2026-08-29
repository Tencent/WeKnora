# 内容大纲模板

## JSON Schema v1

完整 Schema 文件见 [outline-schema-v1.json](outline-schema-v1.json)。

模型必须只返回一个 JSON 对象，不返回 Markdown、代码围栏或解释文字：

```json
{
  "schema_version": 1,
  "chapters": [
    {
      "chapter_index": 1,
      "chapter_title": "短标题",
      "start_seconds": 0,
      "end_seconds": 41,
      "chapter_summary": "本章核心内容",
      "evidence_chunk_ids": ["转写分块ID"],
      "knowledge_points": [
        {
          "title": "短语标题",
          "seconds": 12,
          "evidence_chunk_ids": ["转写分块ID"]
        }
      ]
    }
  ]
}
```

后端负责校验结构、时间范围、证据分块和占位符，再将合规产物写入 Wiki；前端通过接口返回的 `chapters` 渲染，不直接解析模型原文。

```markdown
# 文字稿标题｜内容大纲

## 陈述性章节标题

- 时间：`HH:MM:SS–HH:MM:SS`
- 对齐状态：`verified` / `aligned` / `pending_alignment`
- 章节标题直接描述主题或内容转折，不使用反问、设问或其他疑问句。


### 本章核心内容

一段总结核心观点的概括，提炼核心主张、判断或结论，必须使用具体人名而非泛称，使读者不看原文即可获得实质性信息。

### 关键知识点

- 方法名（00:00）
- 核心结论（00:00）

知识点标题使用短语或结论式短标题，采用“方法名 / 动作 + 对象 / 核心结论”结构；每章默认 1～2 个，只有存在独立信息时才新增，不为覆盖原文而拆分。

### 原文

> **说话人** `00:00:00–00:00:18`
> 原文内容。说话人标签使用人名（如"科技""吴恺"），不用角色+人名组合。
```

原文必须保存在 Wiki 产物中供追溯；章节导航前端不展示原文，仅展示章节标题、本章核心内容、关键知识点及其时间戳，点击时间戳定位视频。
