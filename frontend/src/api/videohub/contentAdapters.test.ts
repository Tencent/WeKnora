import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { buildVideoContentState, classifyContentError, contentModuleForStage } from './contentState'
import { mapRelatedKnowledgeResponse, parseOutlineResponse, parseOutlineWikiPage, parseOverviewWikiPage, parseStructuredSummary, parseSubtitleFile, parseTimestamp, parseTranscriptPageWikiPage } from './contentParsing'
import { getNewlyCompletedStages } from '../../components/videohub/processingStatusState'

test('parses cross-hour outline timestamps and knowledge evidence', () => {
  const chapters = parseOutlineWikiPage(`---
type: outline
---
# 长视频｜内容大纲

## 关键决策
- 时间：\`00:58:30–01:02:15\`
- 对齐状态：\`verified\`

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
  assert.equal(chapters[0].alignment_status, 'verified')
  assert.deepEqual(chapters[0].source_content, [{
    speaker: '讲者',
    start_time: '59:10',
    start_seconds: 3550,
    end_time: '59:30',
    end_seconds: 3570,
    content: '这里是关键证据。',
  }])
})

test('summary accepts the typed JSON contract and preserves block evidence', () => {
  const sections = parseStructuredSummary({
    schemaVersion: 1,
    videoType: 'interview',
    sections: [
      ...['一、人物背景', '二、经历与决策', '三、核心观点', '四、原则与思维模型', '五、案例与证据', '六、反思与边界'].map((title, index) => ({
        id: `section-${index + 1}`,
        title,
        blocks: [{
          id: `block-${index + 1}`,
          kind: 'paragraph',
          text: `第 ${index + 1} 节内容`,
          evidenceChunkIds: [`chunk-${index + 1}`],
          evidence: [{ chunkId: `chunk-${index + 1}`, startSeconds: index, endSeconds: index + 1, timestamp: `00:0${index}`, transcriptSnippet: '真实原文' }],
        }],
      })),
    ],
  }, 'interview')

  assert.equal(sections[0].title, '一、人物背景')
  assert.equal(sections[0].blocks[0].evidence[0].transcriptSnippet, '真实原文')
})

test('summary renders every category using the backend wire contract', () => {
  const titles: Record<string, string[]> = {
    interview: ['一、人物背景', '二、经历与决策', '三、核心观点', '四、原则与思维模型', '五、案例与证据', '六、反思与边界'],
    training: ['一、目标与受众', '二、知识地图', '三、核心概念', '四、方法与步骤', '五、示例与异常', '六、练习与应用'],
    salon: ['一、活动与参与者', '二、议题与观点', '三、观点交锋', '四、案例与问答', '五、共识与分歧', '六、探索方向'],
    general: ['一、定位与问题', '二、主张与论证', '三、证据与案例', '四、限定与反方', '五、影响与建议'],
  }

  for (const [category, categoryTitles] of Object.entries(titles)) {
    const sections = parseStructuredSummary({
      schemaVersion: 1,
      videoType: category,
      sections: categoryTitles.map((title, index) => ({
        id: `${category}-section-${index + 1}`,
        title,
        blocks: [{
          id: `${category}-block-${index + 1}`,
          kind: index % 2 === 0 ? 'paragraph' : 'bullet',
          text: `第 ${index + 1} 节内容\n保留换行`,
          evidenceChunkIds: [`chunk-${index + 1}`],
          evidence: [{
            chunkId: `chunk-${index + 1}`,
            startSeconds: index + 1,
            endSeconds: index + 2,
            timestamp: `00:0${index + 1}–00:0${index + 2}`,
            transcriptSnippet: '真实原文',
          }],
        }],
      })),
    }, category)

    assert.equal(sections.length, categoryTitles.length)
    assert.equal(sections[1]?.blocks[0]?.kind, categoryTitles.length > 1 ? 'bullet' : 'paragraph')
    assert.equal(sections[0]?.blocks[0]?.text, '第 1 节内容\n保留换行')
    assert.equal(sections.at(-1)?.blocks[0]?.evidence[0]?.startSeconds, categoryTitles.length)
  }
})

test('summary rejects Markdown content and template deviations', () => {
  assert.throws(() => parseStructuredSummary({
    schemaVersion: 1,
    videoType: 'interview',
    sections: [{ id: 'section-1', title: '自定义标题', blocks: [] }],
  }, 'interview'))
})

test('parses real SRT subtitles into seekable cues', () => {
  const cues = parseSubtitleFile('\uFEFF1\r\n00:00:01,200 --> 00:00:03,500\r\n第一句\r\n\r\n2\r\n00:01:02,000 --> 00:01:04,000\r\n第二句')
  assert.deepEqual(cues, [
    { start_seconds: 1.2, end_seconds: 3.5, text: '第一句' },
    { start_seconds: 62, end_seconds: 64, text: '第二句' },
  ])
})

test('extracts overview text without frontmatter or heading', () => {
  assert.equal(parseOverviewWikiPage('---\ntype: overview\n---\n\n## 快速概览\n\n视频介绍核心问题和解决方法。'), '视频介绍核心问题和解决方法。')
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

test('keeps cross-video-only responses visible', () => {
  const payload = mapRelatedKnowledgeResponse('video-1', {
    anchors: [],
    cross_video: [{
      id: 'relation-1',
      anchor_id: 'anchor-1',
      title: '关联知识',
      type: 'concept',
      video_id: 'video-2',
      video_title: '另一个视频',
      timestamp: '01:05',
    }],
  })

  assert.equal(payload.crossVideoItems.length, 1)
  assert.equal(payload.crossVideoItems[0].seconds, 65)
  assert.equal(payload.overview?.relation_count, 1)
})

test('real content adapters do not import MOCK_VIDEOS', () => {
  for (const filename of ['outline.ts', 'summary.ts', 'relatedKnowledge.ts', 'transcriptPage.ts']) {
    const source = readFileSync(new URL(filename, import.meta.url), 'utf8')
    assert.equal(source.includes('MOCK_VIDEOS'), false, `${filename} still reads mock data`)
  }
})

test('content loader isolates failed content requests', () => {
  const state = buildVideoContentState(
    { status: 'fulfilled', value: [{ id: 'chapter-1' }] as any },
    { status: 'rejected', reason: { status: 500, message: 'summary unavailable' } },
    { status: 'fulfilled', value: { videoId: 'video-1', overview: null, anchors: [], crossVideoItems: [] } },
  )

  assert.equal(state.outline.status, 'ready')
  assert.equal(state.summary.status, 'error')
  assert.equal(state.relatedKnowledge.status, 'empty')
  assert.equal(state.summary.error, 'summary unavailable')
})

test('content loader distinguishes not generated artifacts from failures', () => {
  assert.equal(classifyContentError({ status: 404, error_code: 'not_generated' }), 'not_generated')
  assert.equal(classifyContentError({ status: 404, error_code: 'artifact_missing' }), 'error')
  assert.equal(classifyContentError({ status: 404, code: 'CONTENT_NOT_READY' }), 'not_generated')
  assert.equal(classifyContentError({ status: 404, code: 'CONTENT_NOT_FOUND' }), 'error')
  assert.equal(classifyContentError({ status: 502, error_code: 'weknora_read_failed' }), 'error')
})

test('maps completed processing stages to local content modules', () => {
  assert.equal(contentModuleForStage('outline'), 'outline')
  assert.equal(contentModuleForStage('overview'), null)
  assert.equal(contentModuleForStage('summary'), 'summary')
  assert.equal(contentModuleForStage('graph'), 'relatedKnowledge')
  assert.equal(contentModuleForStage('assemble'), 'all')
  assert.equal(contentModuleForStage('transcription'), null)
})

test('keeps transcript page markdown available for the detail reader', () => {
  assert.equal(parseTranscriptPageWikiPage('---\ntype: transcript_page\n---\n\n# 文字稿\n\n## 语义时间轴\n内容'), '# 文字稿\n\n## 语义时间轴\n内容')
})

test('emits a processing stage only when it newly succeeds', () => {
  const previous = new Map([['job-outline', 'running'], ['job-summary', 'succeeded']])
  const jobs = [
    { job_id: 'job-outline', job_type: 'outline', status: 'succeeded' },
    { job_id: 'job-summary', job_type: 'summary', status: 'succeeded' },
  ] as any
  assert.deepEqual(getNewlyCompletedStages(previous, jobs), ['outline'])
})

test('outline parser clamps an overlong final chapter to video duration', () => {
  const chapters = parseOutlineWikiPage('## 最后一章\n时间：58:00–1:20:00\n\n### 本章核心内容\n内容', 3600)
  assert.equal(chapters[0].end_seconds, 3600)
})

test('outline parser supports legacy title ranges and defaults missing alignment to pending', () => {
  const chapters = parseOutlineWikiPage('## 第一章：视频引入（00:00 - 00:41）\n\n### 本章核心内容\n内容\n\n### 关键知识点\n- 关键观察（00:12）')
  assert.equal(chapters[0].chapter_title, '第一章：视频引入')
  assert.equal(chapters[0].start_time, '00:00')
  assert.equal(chapters[0].end_time, '00:41')
  assert.equal(chapters[0].alignment_status, 'pending_alignment')
})

test('outline response prefers canonical JSON Schema v1 chapters', () => {
  const chapters = parseOutlineResponse({
    schema_version: 1,
    chapters: [{
      chapter_index: 1,
      chapter_title: '视频引入',
      start_seconds: 0,
      end_seconds: 41,
      chapter_summary: '本章介绍视频主题。',
      knowledge_points: [{ title: '观察场景', seconds: 12 }],
    }],
  }, 535)
  assert.equal(chapters.length, 1)
  assert.equal(chapters[0].chapter_title, '视频引入')
  assert.equal(chapters[0].end_time, '00:41')
  assert.equal(chapters[0].knowledge_points[0].timestamp, '00:12')
})

test('outline parsers reject overlapping chapter ranges', () => {
  assert.throws(() => parseOutlineResponse({
    schema_version: 1,
    chapters: [
      { chapter_index: 1, chapter_title: '第一章', start_seconds: 0, end_seconds: 60, chapter_summary: '第一章内容', knowledge_points: [{ title: '观察', seconds: 12 }] },
      { chapter_index: 2, chapter_title: '第二章', start_seconds: 30, end_seconds: 90, chapter_summary: '第二章内容', knowledge_points: [{ title: '延伸', seconds: 45 }] },
    ],
  }, 90), /章节 2 时间顺序无效/)

  const legacyContent = [
    '## 第一章',
    '时间：00:00–01:00',
    '',
    '### 本章核心内容',
    '内容',
    '',
    '### 关键知识点',
    '- 观察（00:12）',
    '',
    '## 第二章',
    '时间：00:30–01:30',
    '',
    '### 本章核心内容',
    '内容',
    '',
    '### 关键知识点',
    '- 延伸（00:45)',
  ].join('\n')
  assert.throws(() => parseOutlineWikiPage(legacyContent), /章节“第二章”时间顺序无效/)
})

test('outline response rejects an invalid canonical payload instead of showing empty', () => {
  assert.throws(() => parseOutlineResponse({ schema_version: 1, chapters: [] }), /JSON Schema v1 内容无效/)
  assert.throws(() => parseOutlineResponse({
    schema_version: 1,
    chapters: [{ chapter_index: 1, chapter_title: '第一章', start_seconds: -1, end_seconds: 10, chapter_summary: '内容', knowledge_points: [{ title: '观察', seconds: 1 }] }],
  }), /章节 1 时间范围无效/)
  assert.throws(() => parseOutlineWikiPage('只有正文，没有章节标题'), /章节内容缺少章节标题/)
})

test('timestamp parser rejects invalid minute and second fields', () => {
  assert.throws(() => parseTimestamp('01:60'), /无效时间戳/)
  assert.throws(() => parseTimestamp('01:02:60'), /无效时间戳/)
  assert.equal(parseTimestamp('90:00'), 5400)
})
