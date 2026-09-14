import assert from 'node:assert/strict'
import test from 'node:test'

import {
  DEFAULT_MODEL_CONTEXT_WINDOW,
  effectiveContextWindow,
  formatContextWindow,
  formatTokenCount,
  isDefaultContextWindow,
  modelHasContextWindow,
} from './contextWindow.ts'

test('effectiveContextWindow falls back to 200K', () => {
  assert.equal(effectiveContextWindow(undefined), DEFAULT_MODEL_CONTEXT_WINDOW)
  assert.equal(effectiveContextWindow(0), DEFAULT_MODEL_CONTEXT_WINDOW)
  assert.equal(effectiveContextWindow(256000), 256000)
})

test('formatTokenCount uses K/M for round thousands', () => {
  assert.equal(formatTokenCount(128000), '128K')
  assert.equal(formatTokenCount(200000), '200K')
  assert.equal(formatTokenCount(131072), '128K')
  assert.equal(formatTokenCount(1_000_000), '1M')
  assert.equal(formatTokenCount(1048576), '1M')
  assert.equal(formatTokenCount(8192), '8K')
})

test('formatContextWindow uses the default when unset', () => {
  assert.equal(formatContextWindow(undefined), '200K')
  assert.equal(formatContextWindow(128000), '128K')
})

test('modelHasContextWindow is chat and VLM only', () => {
  assert.equal(modelHasContextWindow('KnowledgeQA'), true)
  assert.equal(modelHasContextWindow('VLLM'), true)
  assert.equal(modelHasContextWindow('chat'), true)
  assert.equal(modelHasContextWindow('Embedding'), false)
  assert.equal(modelHasContextWindow('Rerank'), false)
  assert.equal(isDefaultContextWindow(0), true)
  assert.equal(isDefaultContextWindow(200000), false)
})
