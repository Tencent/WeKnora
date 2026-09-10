import assert from 'node:assert/strict'
import test from 'node:test'

import { getCancellableKnowledgeIds } from './batchParseActions.ts'

test('selects only in-flight documents and preserves selection order', () => {
  const items = [
    { id: 'completed', parse_status: 'completed' },
    { id: 'processing', parse_status: 'processing' },
    { id: 'finalizing', parse_status: 'finalizing' },
  ]

  assert.deepEqual(
    getCancellableKnowledgeIds(
      items,
      ['finalizing', 'completed', 'processing', 'stale'],
      (status) => status === 'processing' || status === 'finalizing',
    ),
    ['finalizing', 'processing'],
  )
})

test('does not cancel a selected row that disappeared from the current page', () => {
  assert.deepEqual(
    getCancellableKnowledgeIds(
      [{ id: 'visible', parse_status: 'processing' }],
      ['stale'],
      () => true,
    ),
    [],
  )
})
