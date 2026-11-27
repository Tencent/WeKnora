import assert from 'node:assert/strict'
import { test } from 'node:test'

import { clip } from '../dist/render.js'

test('clip never splits an astral character at the passage budget', () => {
  const result = clip(`${'x'.repeat(1199)}𝑄 trailing text`, 1200)
  assert.equal(result.text.isWellFormed(), true, 'clipped passage must contain no unpaired surrogates')
  assert.deepEqual(result, { text: `${'x'.repeat(1199)}…`, truncated: true })
})

test('clip repairs malformed backend text even when no truncation is needed', () => {
  assert.deepEqual(clip(' \r\nA\ud835B\udc00𝑄 \r\n', 100), {
    text: 'A�B�𝑄', truncated: false,
  })
})

test('clip preserves complete pairs and its normalization and budget behavior', () => {
  assert.deepEqual(clip(' \r\n𝑄😀 \r\n', 4), { text: '𝑄😀', truncated: false })
  assert.deepEqual(clip('𝑄😀x', 4), { text: '𝑄😀…', truncated: true })
  assert.deepEqual(clip('𝑄x', 1), { text: '…', truncated: true })
  assert.deepEqual(clip('𝑄x', 0), { text: '…', truncated: true })
  assert.deepEqual(clip('ab cd', 3), { text: 'ab…', truncated: true })
  assert.deepEqual(clip('', 0), { text: '', truncated: false })
})
