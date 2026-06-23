import assert from 'node:assert/strict'
import test from 'node:test'

import { nextTypewriterReveal } from './useTypewriter.ts'

test('reveals Chinese text in compact phrase groups', () => {
  const text = '这是一个自然流畅的回答。'
  assert.equal(nextTypewriterReveal(text, 0, 1), 0)
  assert.equal(nextTypewriterReveal(text, 0, 2), 2)
  assert.equal(nextTypewriterReveal(text, 0, 20), 4)
})

test('waits for a complete English word instead of revealing letter by letter', () => {
  const text = 'Hello world'
  assert.equal(nextTypewriterReveal(text, 0, 3), 0)
  assert.equal(nextTypewriterReveal(text, 0, 6), 6)
  assert.equal(text.slice(0, nextTypewriterReveal(text, 0, 6)), 'Hello ')
})

test('uses punctuation as a natural reveal boundary', () => {
  const text = '你好，接下来继续。'
  assert.equal(nextTypewriterReveal(text, 0, 3), 3)
})

test('never splits a surrogate pair', () => {
  const text = '🙂 hello'
  assert.equal(nextTypewriterReveal(text, 0, 1), 2)
})
