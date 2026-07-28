import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const pageSource = readFileSync(new URL('./KnowledgeBase.vue', import.meta.url), 'utf8')
const batchBarSource = readFileSync(new URL('./components/DocumentBatchBar.vue', import.meta.url), 'utf8')

test('filtered batch rebuild stays server-scoped and does not enable batch delete', () => {
  assert.match(pageSource, /batchReparseFilteredKnowledge\(kbId\.value, batchReparseFilter\.value\)/)
  assert.match(pageSource, /hasActiveDocumentFilter/)
  assert.match(pageSource, /batchReparseSelectionLimit = 1000/)
  assert.match(pageSource, /batchReparseLimitExceeded/)
  assert.match(pageSource, /:delete-disabled="selectedAllFiltered"/)
  assert.match(batchBarSource, /@click="emit\('select-all-matching'\)"/)
  assert.match(batchBarSource, /deleteLoading \|\| reparseLoading \|\| deleteDisabled/)
})

test('batch rebuild keeps destructive confirmation and busy-state protection', () => {
  assert.match(batchBarSource, /confirmBatchReparseDocument/)
  assert.match(batchBarSource, /count === 0 \|\| deleteLoading \|\| reparseLoading/)
  assert.match(pageSource, /batchReparseSkippedInFlight/)
  assert.match(pageSource, /batchReparsePartial/)
})
