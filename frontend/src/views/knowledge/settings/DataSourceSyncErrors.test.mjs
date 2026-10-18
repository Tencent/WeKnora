import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const logs = readFileSync(new URL('./DataSourceSyncLogs.vue', import.meta.url), 'utf8')
const settings = readFileSync(new URL('./DataSourceSettings.vue', import.meta.url), 'utf8')

function styleBlock(source, selector) {
  return source.match(new RegExp(`${selector}\\s*\\{([\\s\\S]*?)\\n\\}`))?.[1] ?? ''
}

test('failed document rows wrap long IDs instead of clipping them', () => {
  const block = styleBlock(logs, '\\.tl-failed-item')
  assert.ok(block, '.tl-failed-item style block exists')
  assert.doesNotMatch(block, /white-space:\s*nowrap/)
  assert.doesNotMatch(block, /text-overflow:\s*ellipsis/)
  assert.doesNotMatch(block, /overflow:\s*hidden/)
  assert.match(block, /overflow-wrap:\s*(?:anywhere|break-word)/)
})

test('failed document rows keep the full localised error in the native tooltip', () => {
  assert.match(logs, /class="tl-failed-item"\s+:title="formatSyncError\(e, t\)"/)
  assert.match(logs, /\{\{\s*formatSyncError\(e, t\)\s*\}\}/)
})

test('the card renders localised sync-log samples with error_message as fallback', () => {
  assert.match(settings, /import \{ formatSyncErrorSummary \} from '@\/utils\/syncError'/)
  assert.match(settings, /ds\.latest_sync_log\?\.result\?\.errors/)
  assert.match(
    settings,
    /formatSyncErrorSummary\(\s*ds\.latest_sync_log\?\.result\?\.errors,\s*ds\.error_message,\s*t,?\s*\)/,
  )
  assert.match(settings, /v-if="cardSyncError\(ds\)"/)
  assert.match(settings, /:title="cardSyncError\(ds\)"/)
  assert.match(settings, /\{\{ cardSyncError\(ds\) \}\}/)
  assert.doesNotMatch(settings, /\{\{ ds\.error_message \}\}/, 'raw backend English must not render directly')
})
