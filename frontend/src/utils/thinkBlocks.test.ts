import assert from 'node:assert/strict'
import test from 'node:test'
import { hasThinkMarkup, parseThinkBlocks } from './thinkBlocks.ts'

test('content without any think markup passes through untouched', () => {
  const r = parseThinkBlocks('plain answer')
  assert.equal(r.think, '')
  assert.equal(r.answer, 'plain answer')
  assert.equal(r.thinking, false)
  assert.equal(hasThinkMarkup('plain answer'), false)
})

test('single block followed by answer', () => {
  const r = parseThinkBlocks('<think>reasoning</think>The answer is 42.')
  assert.equal(r.think, 'reasoning')
  assert.equal(r.answer, 'The answer is 42.')
  assert.equal(r.thinking, false)
})

test('open block without close marksw content as still thinking', () => {
  const r = parseThinkBlocks('<think>still reasoning')
  assert.equal(r.think, 'still reasoning')
  assert.equal(r.answer, '')
  assert.equal(r.thinking, true)
})

test('multiple blocks keep every segment and drop the tags (#3099)', () => {
  const raw = '<think>round one</think>partial answer<think>round two</think>final answer'
  const r = parseThinkBlocks(raw)
  assert.equal(r.think, 'round one\nround two')
  assert.equal(r.answer, 'partial answerfinal answer')
  assert.equal(r.thinking, false)
})

test('streaming growth: partial open tag text is still thinking', () => {
  // fullContent accumulates; after "<think>re" the parser must report thinking
  const r1 = parseThinkBlocks('<think>re')
  assert.equal(r1.thinking, true)
  assert.equal(r1.think, 're')
  assert.equal(r1.answer, '')
  // once the close tag arrives the answer flips out cleanly
  const r2 = parseThinkBlocks('<think>re</think>ans')
  assert.equal(r2.thinking, false)
  assert.equal(r2.think, 're')
  assert.equal(r2.answer, 'ans')
})

test('multiline reasoning is preserved inside the block', () => {
  const r = parseThinkBlocks('<think>line1\nline2</think>\n\nvisible')
  assert.equal(r.think, 'line1\nline2')
  assert.equal(r.answer, '\n\nvisible')
})

test('empty and empty-ish inputs', () => {
  assert.deepEqual(parseThinkBlocks(''), { think: '', answer: '', thinking: false })
  assert.deepEqual(parseThinkBlocks('<think></think>'), { think: '', answer: '', thinking: false })
})

test('stray close tag without open stays in the answer text', () => {
  const r = parseThinkBlocks('oops </think> done')
  assert.equal(r.thinking, false)
  assert.equal(r.answer, 'oops </think> done')
})

test('hasThinkMarkup detects partial states', () => {
  assert.equal(hasThinkMarkup('<think>abc'), true)
  assert.equal(hasThinkMarkup('abc</think>'), true)
  assert.equal(hasThinkMarkup('no tags'), false)
})
