import assert from 'node:assert/strict'
import test from 'node:test'
import { cleanCollectionFilter, collectionCompletionLabel, collectionDateRange, collectionExportFilename, displayCollectionValue, orderedCollectionFields } from './agentCollectionAdmin.ts'

test('removes empty filters and preserves paging', () => {
  assert.deepEqual(cleanCollectionFilter({ keyword: '', agent_id: undefined, page: 2, page_size: 50 }), { page: 2, page_size: 50 })
})

test('serializes inclusive UTC date filters', () => {
  assert.deepEqual(collectionDateRange('2026-07-01', '2026-07-22'), {
    updated_from: '2026-07-01T00:00:00Z', updated_to: '2026-07-22T23:59:59Z',
  })
})

test('orders configured fields and formats values', () => {
  const fields = [
    { key: 'b', label: 'B', type: 'short_text' as const, required: false, enabled: true, order: 2 },
    { key: 'a', label: 'A', type: 'short_text' as const, required: false, enabled: true, order: 1 },
  ]
  assert.deepEqual(orderedCollectionFields(fields).map((field) => field.key), ['a', 'b'])
  assert.equal(displayCollectionValue(['远程', '弹性']), '远程、弹性')
  assert.equal(collectionCompletionLabel(false), '待补充')
})

test('uses server export filename with a date fallback', () => {
  assert.equal(collectionExportFilename('profiles.csv', 'csv'), 'profiles.csv')
  assert.equal(collectionExportFilename('', 'xlsx', new Date('2026-07-22T12:00:00Z')), 'agent-collection-2026-07-22.xlsx')
})
