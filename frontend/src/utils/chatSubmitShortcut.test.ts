import assert from 'node:assert/strict'
import test from 'node:test'
import { chatSubmitShortcut } from './chatSubmitShortcut'

const enter = { key: 'Enter', keyCode: 13, isComposing: false, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false }
test('Enter queues during a run and Alt+Enter injects only while steering is available', () => {
  assert.equal(chatSubmitShortcut(enter, true), 'after')
  assert.equal(chatSubmitShortcut(enter, false), 'inject')
  assert.equal(chatSubmitShortcut({ ...enter, altKey: true }, true), 'inject')
  assert.equal(chatSubmitShortcut({ ...enter, altKey: true }, false), undefined)
})
test('Cmd+Enter injects during a run and sends normally when idle', () => {
  const cmdEnter = { ...enter, metaKey: true }
  assert.equal(chatSubmitShortcut(cmdEnter, true), 'inject')
  assert.equal(chatSubmitShortcut(cmdEnter, false), 'inject')
  for (const modifiers of [{ shiftKey: true }, { ctrlKey: true }, { isComposing: true }, { keyCode: 229 }]) {
    assert.equal(chatSubmitShortcut({ ...cmdEnter, ...modifiers }, true), undefined)
  }
})
test('newlines and IME confirmation never submit', () => {
  for (const modifiers of [{ shiftKey: true }, { ctrlKey: true }, { isComposing: true }, { keyCode: 229 }]) {
    assert.equal(chatSubmitShortcut({ ...enter, ...modifiers }, true), undefined)
  }
  assert.equal(chatSubmitShortcut({ ...enter, key: 'a', keyCode: 65 }, true), undefined)
})
