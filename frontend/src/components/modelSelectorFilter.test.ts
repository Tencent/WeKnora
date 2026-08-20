import assert from 'node:assert/strict'
import test from 'node:test'

import type { ModelConfig } from '@/api/model'

import { filterModelsByType } from './modelSelectorFilter.ts'

function model(overrides: Partial<ModelConfig> & Pick<ModelConfig, 'id' | 'type'>): ModelConfig {
  return {
    name: overrides.name ?? overrides.id!,
    source: 'remote',
    parameters: {},
    ...overrides,
  }
}

const fixtures: ModelConfig[] = [
  model({ id: 'vllm-1', type: 'VLLM' }),
  model({ id: 'chat-vision', type: 'KnowledgeQA', parameters: { supports_vision: true } }),
  model({ id: 'chat-text', type: 'KnowledgeQA' }),
  model({ id: 'embed-vision', type: 'Embedding', parameters: { supports_vision: true } }),
  model({ id: 'embed-1', type: 'Embedding' }),
]

test('VLLM selector includes native VLLM and vision-capable chat models', () => {
  const ids = filterModelsByType(fixtures, 'VLLM').map((m) => m.id)
  assert.deepEqual(ids, ['vllm-1', 'chat-vision'])
})

test('VLLM selector excludes non-vision chat and other types even with supports_vision', () => {
  const ids = filterModelsByType(fixtures, 'VLLM')
  assert.ok(!ids.some((m) => m.id === 'chat-text'))
  assert.ok(!ids.some((m) => m.id === 'embed-vision'))
})

test('non-VLLM selectors still filter strictly by type', () => {
  const ids = filterModelsByType(fixtures, 'KnowledgeQA').map((m) => m.id)
  assert.deepEqual(ids, ['chat-vision', 'chat-text'])
})
