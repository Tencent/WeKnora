import assert from 'node:assert/strict'
import test from 'node:test'
import { continueListOnEnter, countContent, indentOnTab } from './markdownEditing'

const caret = (value: string, at: number) => ({ value, start: at, end: at })

test('Enter continues a bullet, a task item and a numbered list', () => {
  const bullet = continueListOnEnter(caret('- first', 7))
  assert.deepEqual(bullet, { value: '- first\n- ', start: 10, end: 10 })

  const task = continueListOnEnter(caret('- [x] done', 10))
  assert.deepEqual(task, { value: '- [x] done\n- [ ] ', start: 17, end: 17 })

  const ordered = continueListOnEnter(caret('  9. nine', 9))
  assert.deepEqual(ordered, { value: '  9. nine\n  10. ', start: 16, end: 16 })
})

test('Enter on an empty item ends the list instead of stacking markers', () => {
  const patch = continueListOnEnter(caret('- first\n- ', 10))
  assert.deepEqual(patch, { value: '- first\n', start: 8, end: 8 })
})

test('Enter outside a list and over a selection is left to the browser', () => {
  assert.equal(continueListOnEnter(caret('plain text', 10)), null)
  assert.equal(continueListOnEnter({ value: '- first', start: 2, end: 7 }), null)
})

test('Tab indents a list line and Shift+Tab takes it back out', () => {
  const indented = indentOnTab(caret('- item', 6), false)
  assert.deepEqual(indented, { value: '  - item', start: 8, end: 8 })

  const outdented = indentOnTab(caret('  - item', 8), true)
  assert.equal(outdented?.value, '- item')
})

test('Tab indents every line of a multi-line selection but skips blank ones', () => {
  const patch = indentOnTab({ value: 'a\n\nb', start: 0, end: 4 }, false)
  assert.equal(patch?.value, '  a\n\n  b')
  assert.deepEqual([patch?.start, patch?.end], [0, 8])
})

test('Tab stays a focus move when it would not indent anything', () => {
  assert.equal(indentOnTab(caret('plain text', 4), false), null)
})

test('countContent reports characters and lines', () => {
  assert.deepEqual(countContent('one\ntwo'), { characters: 7, lines: 2 })
  assert.deepEqual(countContent(''), { characters: 0, lines: 0 })
})
