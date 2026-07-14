import assert from 'node:assert/strict'
import test from 'node:test'

import type { RuntimeTask } from '@/api/system'
import { mergeRuntimeTaskPage } from './runtimeTaskPagination.ts'

function task(id: string): RuntimeTask {
  return {
    id,
    queue: 'default',
    type: 'document:process',
    state: 'archived',
    allowed_actions: [],
    retried: 0,
    max_retry: 0,
  }
}

test('appends a cursor page without changing backend order', () => {
  assert.deepEqual(
    mergeRuntimeTaskPage([task('newest'), task('newer')], [task('older'), task('oldest')]).map((item) => item.id),
    ['newest', 'newer', 'older', 'oldest'],
  )
})

test('deduplicates overlap from a surviving cursor anchor', () => {
  assert.deepEqual(
    mergeRuntimeTaskPage([task('a'), task('b'), task('c')], [task('c'), task('d')]).map((item) => item.id),
    ['a', 'b', 'c', 'd'],
  )
})
