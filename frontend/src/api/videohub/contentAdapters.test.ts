import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { mapRelatedKnowledgeResponse, parseOutlineWikiPage, parseSummaryWikiPage } from './contentParsing'

test('parses cross-hour outline timestamps and knowledge evidence', () => {
  const chapters = parseOutlineWikiPage(`---
type: outline
---
# 长视频｜内容大纲

## 关键决策
- 时间：\`00:58:30–01:02:15\`

### 本章核心内容
讲者解释跨小时决策过程。

### 关键知识点
- 决策转折（\`00:59:10\`）
- **关键词**：决策、复盘

### 原文
> **讲者** \`00:59:10–00:59:30\`
> 这里是关键证据。
`, 4000)

  assert.equal(chapters.length, 1)
  assert.equal(chapters[0].start_seconds, 3510)
  assert.equal(chapters[0].end_seconds, 3735)
  assert.equal(chapters[0].knowledge_points[0].seconds, 3550)
  assert.equal(chapters[0].knowledge_points[0].transcriptSnippet, '这里是关键证据。')
})

test('summary uses only real headings without filling template placeholders', () => {
  const sections = parseSummaryWikiPage(`---
type: typed_summary
---
# 总结

## 一、人物背景
创始人经历了产品转型。

> \`00:10:05–00:10:20\`
> 我们当时决定停止旧产品。

## 三、核心观点
- 聚焦真实用户问题。
`)

  assert.deepEqual(sections.map(section => section.title), ['一、人物背景', '三、核心观点'])
  assert.equal(sections[0].evidenceSeconds, 605)
  assert.equal(sections[0].transcriptSnippet, '我们当时决定停止旧产品。')
})

test('maps grouped backend anchors without mock video data', () => {
  const payload = mapRelatedKnowledgeResponse('video-1', {
    anchors: {
      entity: [{ id: 'entity-1', title: '张三', type: 'entity', related_video_ids: ['video-2'] }],
      method: [{ id: 'method-1', title: '复盘法', type: 'method' }],
    },
    cross_video: [],
  })

  assert.equal(payload.anchors.length, 2)
  assert.equal(payload.overview?.related_video_count, 1)
  assert.deepEqual(payload.overview?.top_topics, ['张三', '复盘法'])
})

test('real content adapters do not import MOCK_VIDEOS', () => {
  for (const filename of ['summary.ts', 'relatedKnowledge.ts']) {
    const source = readFileSync(new URL(filename, import.meta.url), 'utf8')
    assert.equal(source.includes('MOCK_VIDEOS'), false, `${filename} still reads mock data`)
  }
})
