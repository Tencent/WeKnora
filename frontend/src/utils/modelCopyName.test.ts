import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MODEL_DISPLAY_NAME_MAX_LEN,
  generateCopyDisplayName,
  modelCopyLabel,
} from './modelCopyName.ts'

test('modelCopyLabel prefers a trimmed display name and otherwise uses name', () => {
  assert.equal(modelCopyLabel({ display_name: ' 生产 GPT ', name: 'gpt-4o' }), '生产 GPT')
  assert.equal(modelCopyLabel({ display_name: '   ', name: 'gpt-4o' }), 'gpt-4o')
  assert.equal(modelCopyLabel({ name: 'qwen3:8b' }), 'qwen3:8b')
})

test('generateCopyDisplayName suffixes the display label and keeps later copies numbered', () => {
  assert.equal(generateCopyDisplayName('生产 GPT', ['生产 GPT'], ' 副本'), '生产 GPT 副本')
  assert.equal(
    generateCopyDisplayName('生产 GPT', ['生产 GPT', '生产 GPT 副本'], ' 副本'),
    '生产 GPT 副本 2',
  )
})

test('generateCopyDisplayName treats a name-only row as occupying that label', () => {
  assert.equal(
    generateCopyDisplayName('gpt-4o', ['gpt-4o', 'gpt-4o 副本'], ' 副本'),
    'gpt-4o 副本 2',
  )
})

test('generateCopyDisplayName fits the label into the display_name column', () => {
  const suffix = ' 副本'
  const base = '名'.repeat(300)
  const first = generateCopyDisplayName(base, [], suffix)
  assert.equal(Array.from(first).length, MODEL_DISPLAY_NAME_MAX_LEN)
  assert.equal(first.endsWith(suffix), true)

  const second = generateCopyDisplayName(base, [first], suffix)
  assert.equal(Array.from(second).length, MODEL_DISPLAY_NAME_MAX_LEN)
  assert.equal(second.endsWith(`${suffix} 2`), true)
  assert.notEqual(first, second)
})
