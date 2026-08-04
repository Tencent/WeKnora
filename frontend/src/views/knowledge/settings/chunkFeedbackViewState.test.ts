import assert from 'node:assert/strict'
import test from 'node:test'

import { createLatestRequestGuard, getLastPage } from './chunkFeedbackViewState'

test('latest request guard rejects earlier responses and supports invalidation', () => {
  const guard = createLatestRequestGuard()
  const first = guard.begin()
  const second = guard.begin()

  assert.equal(guard.isLatest(first), false)
  assert.equal(guard.isLatest(second), true)

  guard.invalidate()
  assert.equal(guard.isLatest(second), false)
})

test('last page clamps empty and reduced result sets', () => {
  assert.equal(getLastPage(0, 10), 1)
  assert.equal(getLastPage(10, 10), 1)
  assert.equal(getLastPage(11, 10), 2)
  assert.equal(getLastPage(Number.NaN, 0), 1)
})
