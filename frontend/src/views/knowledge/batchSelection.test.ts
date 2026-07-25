import assert from 'node:assert/strict'
import test from 'node:test'

import { summarizeBatchSelection } from './batchSelection.ts'

test('summarizes only selected documents for batch recovery actions', () => {
  const summary = summarizeBatchSelection([
    { id: 'failed-1', parse_status: 'failed' },
    { id: 'failed-2', parse_status: 'failed' },
    { id: 'processing', parse_status: 'processing' },
    { id: 'completed', parse_status: 'completed' },
    { id: 'unselected', parse_status: 'cancelled' },
  ], new Set(['failed-1', 'failed-2', 'processing', 'completed']))

  assert.deepEqual(summary, {
    total: 4,
    inFlight: 1,
    rebuildable: 3,
    completed: 1,
    failed: 2,
    cancelled: 0,
    draft: 0,
    other: 0,
  })
})

test('treats pending and finalizing documents as stoppable', () => {
  const summary = summarizeBatchSelection([
    { id: 'pending', parse_status: 'pending' },
    { id: 'finalizing', parse_status: 'finalizing' },
    { id: 'cancelled', parse_status: 'cancelled' },
    { id: 'draft', parse_status: 'draft' },
    { id: 'unknown', parse_status: 'custom' },
  ], new Set(['pending', 'finalizing', 'cancelled', 'draft', 'unknown']))

  assert.equal(summary.inFlight, 2)
  assert.equal(summary.rebuildable, 2)
  assert.equal(summary.cancelled, 1)
  assert.equal(summary.draft, 1)
  assert.equal(summary.other, 1)
})
