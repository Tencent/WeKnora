# Confluence 页面表格转换优化分析

## 背景与范围

Confluence 连接器从页面 API 取得 HTML 正文后，将其转换为 Markdown，再作为 `.md` 文件交给知识库解析流程。原先直接调用 `html-to-markdown/v2` 的包级转换函数，表格单元格会变成普通文本，行列关系随之丢失。

本次只处理 Confluence 页面正文中的表格；图片、宏、页面链接等其他内容不在范围内。对应的功能请求为 [Tencent/WeKnora#4794](https://github.com/Tencent/WeKnora/issues/4794)，已有的综合问题见 [Tencent/WeKnora#3856](https://github.com/Tencent/WeKnora/issues/3856)。

## 转换方案

1. 在转换整页 HTML 之前，按原文位置识别完整的 `<table>` 区块，并以唯一占位符暂时保护它们。
2. 使用现有转换器处理表格以外的正文，保留标题、段落等既有行为。
3. 对非嵌套表格复用 `docparser.NormalizeHTMLTables`：普通表格转换为 GFM Markdown；合并单元格或无法安全表示为 GFM 的表格保留 HTML 结构，并将各行分隔到独立行。嵌套表格保留完整原始 HTML，同样按行插入换行，避免通用规范化器截断外层表格。
4. 按原顺序回填表格，避免表格与相邻正文错位。

该方案不改变 Confluence 抓取、增量游标或知识库入库接口。历史文档需要从 Confluence **全量同步**才能重新生成源 Markdown；仅重新解析已保存的旧 `.md` 不会重新执行页面 HTML 转换。

## 验收与限制

- 普通表格在原始 Markdown 中包含表头、分隔行和单元格内容；复杂表格保留行列与合并单元格属性。
- 多个表格与表格前后的正文保持原有顺序；无表格页面的转换结果不变。
- 同步完成后还需等待异步解析结束，再检查知识分块中的表格内容。
- 这次优化不承诺将所有复杂 HTML 表格转为纯 Markdown；保留 HTML 是避免结构丢失的回退行为。
