import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const processingStatus = readFileSync(new URL('./ProcessingStatus.vue', import.meta.url), 'utf8')
const summary = readFileSync(new URL('./SmartSummary.vue', import.meta.url), 'utf8')
const chapters = readFileSync(new URL('./ChapterNavigation.vue', import.meta.url), 'utf8')
const related = readFileSync(new URL('./RelatedKnowledge.vue', import.meta.url), 'utf8')
const detail = readFileSync(new URL('../../views/videohub/VideoDetail.vue', import.meta.url), 'utf8')
const state = readFileSync(new URL('./processingStatusState.ts', import.meta.url), 'utf8')

test('detail processing status is aggregated without a stage timeline', () => {
  assert.match(processingStatus, /'视频转写中'/)
  assert.match(processingStatus, /'AI内容生成中'/)
  assert.doesNotMatch(processingStatus, /<ol|processing-status__stages|stageOrder/)
})

test('Tencent MPS progress is shown only for a valid running MPS task', () => {
  assert.match(state, /provider === 'tencent_mps'/)
  assert.match(state, /phase === 'mps_running'/)
  assert.match(state, /progress >= 1 && progress <= 100/)
  assert.match(processingStatus, /视频转写中 \$\{transcriptionProgress\.value\}%/)
})

test('each AI content module exposes an in-place generation retry', () => {
  assert.match(summary, /message="智能总结生成失败"[\s\S]*emit\('retry'\)/)
  assert.match(chapters, /message="章节导航生成失败"[\s\S]*emit\('retry'\)/)
  assert.match(related, /message="关联知识生成失败"[\s\S]*emit\('retry'\)/)
  assert.match(detail, /retryContentModule\('summary'\)/)
  assert.match(detail, /retryContentModule\('outline'\)/)
  assert.match(detail, /retryContentModule\('relatedKnowledge'\)/)
})

test('module retries preserve existing content instead of replacing it with a skeleton', () => {
  assert.match(summary, /sections\.value\.length === 0/)
  assert.match(chapters, /chapters\.value\.length === 0/)
  assert.match(related, /!hasContent\.value/)
  assert.match(detail, /\[module\]: \{ \.\.\.current, status: 'loading' \}/)
  assert.doesNotMatch(detail, /catch \{[\s\S]*?reloadContentModule\(module\)[\s\S]*?\} finally/)
})

test('active generation keeps empty modules in skeleton state even after a content request error', () => {
  assert.match(summary, /sections\.value\.length === 0 && \(loading\.value \|\| props\.isGenerating\)/)
  assert.match(chapters, /chapters\.value\.length === 0 && \(loading\.value \|\| props\.isGenerating\)/)
  assert.match(related, /!hasContent\.value && \(loading\.value \|\| props\.isGenerating\)/)
})

test('related knowledge retries the graph generation job', () => {
  assert.match(detail, /relatedRetryStage = 'graph'/)
  assert.doesNotMatch(detail, /\['related_knowledge', 'graph'\]/)
})

test('summary retry restores the foundation result before enhancement', () => {
  assert.match(detail, /\? \['summary', 'summary_enhance'\]/)
  assert.match(detail, /processingFailures\.value\.has\('summary'\) \? 'summary' : 'summary_enhance'/)
})
