import assert from 'node:assert/strict'
import test from 'node:test'
import { changedRange } from './pageReveal.ts'

test('changedRange is the span of the new text that differs', () => {
  assert.equal(changedRange('same', 'same'), null)
  assert.deepEqual(changedRange('# A\nold line\nend', '# A\nnew text here\nend'), { start: 4, end: 16 },
    'the shared "e" before the newline stays outside')
  assert.deepEqual(changedRange('abc', 'abXc'), { start: 2, end: 3 }, 'an insertion')
  assert.deepEqual(changedRange('abXc', 'abc'), { start: 2, end: 2 }, 'a deletion is an empty span where it was')
  assert.deepEqual(changedRange('aaa', 'aaaa'), { start: 3, end: 4 }, 'repeats do not overlap prefix and suffix')
  assert.deepEqual(changedRange('', 'new'), { start: 0, end: 3 })
})
