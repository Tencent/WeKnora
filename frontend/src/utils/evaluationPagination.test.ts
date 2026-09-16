import assert from 'node:assert/strict'
import test from 'node:test'
import { clampPage, pageCount, pageItems } from './evaluationPagination'

test('repeat summaries show two items per page', () => {
  assert.deepEqual(pageItems(['a', 'b', 'c', 'd', 'e'], 1, 2), ['a', 'b'])
  assert.deepEqual(pageItems(['a', 'b', 'c', 'd', 'e'], 3, 2), ['e'])
  assert.equal(pageCount(5, 2), 3)
})

test('evaluation history shows five items per page', () => {
  assert.deepEqual(pageItems([1, 2, 3, 4, 5, 6], 1, 5), [1, 2, 3, 4, 5])
  assert.deepEqual(pageItems([1, 2, 3, 4, 5, 6], 2, 5), [6])
})

test('pages clamp after refresh reduces the number of results', () => {
  assert.equal(clampPage(4, 3, 2), 2)
  assert.equal(clampPage(0, 0, 5), 1)
  assert.deepEqual(pageItems(['only'], 99, 5), ['only'])
})
