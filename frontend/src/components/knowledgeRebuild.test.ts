import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'

const timeline = readFileSync(new URL('./knowledge-processing-timeline.vue', import.meta.url), 'utf8')
const api = readFileSync(new URL('../api/knowledge-base/index.ts', import.meta.url), 'utf8')
const knowledgeBase = readFileSync(new URL('../views/knowledge/KnowledgeBase.vue', import.meta.url), 'utf8')
const actionMenu = readFileSync(new URL('../views/knowledge/components/DocumentActionMenu.vue', import.meta.url), 'utf8')
const locales = [
  '../i18n/locales/zh-CN.ts',
  '../i18n/locales/en-US.ts',
  '../i18n/locales/ko-KR.ts',
  '../i18n/locales/ru-RU.ts',
].map(path => readFileSync(new URL(path, import.meta.url), 'utf8'))

test('trace rebuild dialog exposes the reusable backend stages', () => {
  assert.match(timeline, /rebuildMode/)
  assert.match(timeline, /rebuildDialogVisible/)
  for (const stage of ['embedding', 'summary', 'questions', 'graph', 'wiki', 'journal_rank', 'content_type']) {
    assert.match(timeline, new RegExp(`value="${stage}"`))
  }
  assert.match(timeline, /reparseKnowledge\(props\.knowledgeId, stages \? \{ stages \} : undefined\)/)
})

test('reparse request contract carries optional rebuild stages', () => {
  assert.match(api, /export type KnowledgeRebuildStage/)
  assert.match(api, /stages\?: KnowledgeRebuildStage\[\]/)
})

test('card and list rebuild actions open a partial rebuild dialog', () => {
  assert.doesNotMatch(actionMenu, /knowledgeBase\.rebuildConfirm/)
  assert.match(actionMenu, /@click\.stop="emit\('reparse'\)"/)
  assert.match(knowledgeBase, /const rebuildMode = ref<'full' \| 'partial'>\('partial'\)/)
  assert.match(knowledgeBase, /const rebuildStages = ref<KnowledgeRebuildStage\[]>\(\[\]\)/)
  assert.match(knowledgeBase, /@confirm="submitRebuildChoice"/)
  for (const stage of ['embedding', 'summary', 'questions', 'graph', 'wiki', 'journal_rank', 'content_type']) {
    assert.match(knowledgeBase, new RegExp(`value="${stage}"`))
  }
  assert.match(knowledgeBase, /submitReparse\(item\.id, undefined, rebuildStages\.value\)/)
})

test('batch rebuild uses the same partial rebuild stage selector', () => {
  const batchBar = readFileSync(new URL('../views/knowledge/components/DocumentBatchBar.vue', import.meta.url), 'utf8')
  assert.doesNotMatch(batchBar, /confirmBatchReparseDocument/)
  assert.match(knowledgeBase, /rebuildTargetIds = ref<string\[\]>\(\[\]\)/)
  assert.match(knowledgeBase, /reparseKnowledge\(id, \{ stages: rebuildStages\.value \}\)/)
  assert.match(knowledgeBase, /rebuildTargetIds\.value = ids/)
})

test('all supported locales define rebuild controls', () => {
  for (const locale of locales) {
    for (const key of ['rebuildDialogTitle', 'rebuildFull', 'rebuildPartial', 'stageEmbedding', 'stageJournalRank', 'stageContentType']) {
      assert.match(locale, new RegExp(`${key}:`))
    }
  }
})
