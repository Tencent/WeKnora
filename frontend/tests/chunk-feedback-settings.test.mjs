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
  assert.doesNotMatch(source, /<t-tag-group/)
})

test('chunk feedback i18n contains keys used by the settings page', () => {
  const zh = read('src/i18n/locales/zh-CN.ts')
  const en = read('src/i18n/locales/en-US.ts')

  for (const source of [zh, en]) {
    assert.match(source, /allRatedChunks:/)
    assert.match(source, /resetConfirm:/)
    assert.match(source, /operator:/)
  }
})

test('agent answers expose the same feedback controls as normal chat answers', () => {
  const source = read('src/views/chat/components/botmsg.vue')

  assert.match(source, /session\.isAgentMode && showFeedbackActions/)
  assert.match(source, /handleFeedback\(true\)/)
  assert.match(source, /handleDislike/)
  assert.match(source, /getUserFeedback\(props\.session\.id\)/)
})
