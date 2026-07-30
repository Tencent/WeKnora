import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const here = dirname(fileURLToPath(import.meta.url))
const root = resolve(here, '..')

const read = (path) => readFileSync(resolve(root, path), 'utf8')

test('chunk feedback settings keeps guarded reset and scoped list behavior', () => {
  const source = read('src/views/knowledge/settings/ChunkFeedbackSettings.vue')

  assert.match(source, /<t-popconfirm[\s\S]+resetConfirm/)
  assert.match(source, /:loading="listLoading"/)
  assert.match(source, /knowledge_base_id:\s*props\.kbId \|\| undefined/)
  assert.match(source, /getFeedbackOverview\(\{\s*knowledge_base_id:\s*props\.kbId \|\| undefined\s*\}\)/)
  assert.match(source, /if \(!listRequestGuard\.isLatest\(requestId\)\) return false/)
  assert.match(source, /lowQualityChunks\.value = \[\]\s+totalCount\.value = 0/)
  assert.match(source, /getLastPage\(totalCount\.value, pageSize\.value\)/)
  assert.match(source, /const rateOptions = computed/)
  assert.match(source, /const columns = computed/)
  assert.match(source, /const weightLogColumns = computed/)
  assert.doesNotMatch(source, /<t-tag-group/)
})

test('chunk detail loads stats and logs independently with latest-request guards', () => {
  const source = read('src/views/knowledge/settings/ChunkFeedbackSettings.vue')

  assert.match(source, /const requestId = detailRequestGuard\.begin\(\)/)
  assert.match(source, /const statsRequest = \(async \(\) =>/)
  assert.match(source, /const logsRequest = \(async \(\) =>/)
  assert.match(source, /detailStatsError\.value = t\('knowledgeEditor\.feedback\.messages\.chunkStatsLoadFailed'\)/)
  assert.match(source, /detailLogsError\.value = t\('knowledgeEditor\.feedback\.messages\.weightLogsLoadFailed'\)/)
  assert.match(source, /weightLogTotal\.value = logsRes\.data\.total/)
  assert.match(source, /weightLogsSummary/)
  assert.match(source, /date\.toLocaleString\(locale\.value\)/)
})

test('chunk feedback i18n contains keys used by the settings page', () => {
  const sources = ['zh-CN', 'en-US', 'ko-KR', 'ru-RU'].map((locale) =>
    read(`src/i18n/locales/${locale}.ts`),
  )

  for (const source of sources) {
    assert.match(source, /allRatedChunks:/)
    assert.match(source, /resetConfirm:/)
    assert.match(source, /operator:/)
    assert.match(source, /weightLogsSummary:/)
    assert.match(source, /overviewLoadFailed:/)
    assert.match(source, /chunkStatsLoadFailed:/)
    assert.match(source, /weightLogsLoadFailed:/)
    assert.match(source, /feedbackSubmitted:/)
    assert.match(source, /feedbackCanceled:/)
  }
})

test('overview labels total_chunks as all chunks in every locale', () => {
  const expected = new Map([
    ['zh-CN', /totalChunks:\s*"全部片段"/],
    ['en-US', /totalChunks:\s*'Total chunks'/],
    ['ko-KR', /totalChunks:\s*"전체 청크"/],
    ['ru-RU', /totalChunks:\s*'Всего фрагментов'/],
  ])

  for (const [locale, pattern] of expected) {
    assert.match(read(`src/i18n/locales/${locale}.ts`), pattern)
  }
})

test('agent answers expose the same feedback controls as normal chat answers', () => {
  const source = read('src/views/chat/components/botmsg.vue')

  assert.match(source, /session\.isAgentMode && showFeedbackActions/)
  assert.match(source, /handleFeedback\(true\)/)
  assert.match(source, /handleDislike/)
  assert.match(source, /createChatFeedbackStateController/)
  assert.equal(source.match(/:disabled="feedbackMutationPending"/g)?.length, 8)
  assert.equal(source.match(/:loading="feedbackMutationPending"/g)?.length, 2)
  assert.equal(source.match(/:maxlength="255"/g)?.length, 2)
})

test('feedback API types dislike reasons using the response envelope', () => {
  const source = read('src/api/feedback.ts')

  assert.match(
    source,
    /getDislikeReasons\(\)[\s\S]*?get<\{ success: boolean; data: string\[\] \}>\('\/api\/v1\/feedback\/dislike-reasons'\)/,
  )
})

test('feedback navigation is admin-only and outside the FAQ type branch', () => {
  const source = read('src/views/knowledge/KnowledgeBaseEditorModal.vue')

  assert.match(source, /v-if="canManageFeedback && currentSection === 'feedback'"/)
  assert.match(
    source,
    /Number\(kbTenantId\.value \|\| 0\) === Number\(authStore\.effectiveTenantId \|\| 0\)/,
  )
  assert.doesNotMatch(
    source,
    /Number\(kbTenantId\.value \|\| 0\) === Number\(authStore\.currentTenantId \|\| 0\)/,
  )
  assert.match(
    source,
    /\n  }\n  if \(canManageFeedback\.value\) \{\n    items\.push\(\{ key: 'feedback'/,
  )
})
