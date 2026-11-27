import test from 'node:test'
import assert from 'node:assert/strict'
import { clip, formatScore, describeScope } from '../dist/render.js'

test('clip preserves text within budget', () => {
  const result = clip('Hello world', 20)
  assert.equal(result.truncated, false)
  assert.equal(result.text, 'Hello world')
})

test('clip truncates long passages and appends ellipsis', () => {
  const result = clip('Hello world', 5)
  assert.equal(result.truncated, true)
  assert.equal(result.text, 'Hello…')
})

test('clip does not split surrogate pairs into lone high surrogates', () => {
  // \uD835\uDC00 is mathematical bold capital A (U+1D400)
  // "Prefix \uD835\uDC00 suffix"
  // "Prefix " has length 7.
  // Slicing at length 8 falls exactly between high surrogate (\uD835) and low surrogate (\uDC00).
  const mathStr = 'Prefix \uD835\uDC00 suffix'
  const result = clip(mathStr, 8)
  assert.equal(result.truncated, true)
  // Should back off and not include lone high surrogate \uD835
  assert.equal(result.text, 'Prefix…')
  assert.equal(result.text.includes('\uD835'), false)
  // Check valid JSON roundtrip (JSON parser/gateway will not break)
  assert.doesNotThrow(() => JSON.parse(JSON.stringify({ text: result.text })))
})

test('clip includes surrogate pairs when budget allows both code units', () => {
  const emojiStr = 'Hello 😀 World'
  // 'Hello ' is 6 chars. '😀' is 2 code units. 6 + 2 = 8 chars.
  const result = clip(emojiStr, 8)
  assert.equal(result.truncated, true)
  assert.equal(result.text, 'Hello 😀…')
})

test('clip handles budget of 1 with surrogate pair starting at 0', () => {
  const emojiStr = '😀 trailing'
  const result = clip(emojiStr, 1)
  assert.equal(result.truncated, true)
  assert.equal(result.text, '…')
  assert.equal(result.text.includes('\uD83D'), false)
})

test('formatScore handles finite and missing scores', () => {
  assert.equal(formatScore(0.87654), '0.877')
  assert.equal(formatScore(Number.NaN), 'n/a')
})

test('describeScope formats scopes cleanly', () => {
  assert.equal(describeScope([]), '(deployment default)')
  assert.equal(describeScope(['kb-1', 'kb-2']), 'kb-1, kb-2')
  assert.equal(describeScope(['kb-1', 'kb-2', 'kb-3', 'kb-4']), '4 knowledge bases')
})
