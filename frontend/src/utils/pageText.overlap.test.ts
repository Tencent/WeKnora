import assert from 'node:assert/strict'
import test from 'node:test'
import { applyTextEdits, PageFillError } from './pageText.ts'

for (const [value, needle, count] of [
  ['banana', 'ana', 2],
  ['aaaa', 'aaa', 2],
  ['哈哈哈', '哈哈', 2],
  ['😀😀😀', '😀😀', 2],
  ['row\nrow\nrow', 'row\nrow', 2],
] as const) {
  for (const op of ['replace', 'insert_before', 'insert_after'] as const) {
    test(`${op} rejects overlapping ${JSON.stringify(needle)}`, () => {
      assert.throws(() => applyTextEdits(value, [{
        op, old_text: needle, anchor: needle, text: 'changed',
      }]), (error: Error) => error instanceof PageFillError
        && error.message.includes(`occurs ${count} times`))
    })
  }
}

test('reports all overlapping occurrences', () => {
  assert.throws(() => applyTextEdits('aaaaa', [
    { op: 'replace', old_text: 'aa', text: 'b' },
  ]), /occurs 4 times/)
})
test('batch identifies the ambiguous edit after an earlier transform', () => {
  assert.throws(() => applyTextEdits('ban', [
    { op: 'append', text: 'ana' },
    { op: 'replace', old_text: 'ana', text: 'x' },
  ]), /edit 2: old_text "ana" occurs 2 times/)
})
test('a surrounding context disambiguates an overlapping match', () => {
  assert.equal(applyTextEdits('banana', [{ op: 'replace', old_text: 'bana', text: 'x' }]), 'xna')
  assert.equal(applyTextEdits('banana', [{ op: 'insert_before', anchor: 'bana', text: 'x' }]), 'xbanana')
  assert.equal(applyTextEdits('banana', [{ op: 'insert_after', anchor: 'bana', text: 'x' }]), 'banaxna')
})
