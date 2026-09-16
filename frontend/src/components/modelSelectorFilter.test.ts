import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import type { ModelConfig } from '@/api/model'

import { filterModelsByType } from './modelSelectorFilter.ts'

const selectorSource = readFileSync(new URL('./ModelSelector.vue', import.meta.url), 'utf8')
const kbEditorSource = readFileSync(new URL('../views/knowledge/KnowledgeBaseEditorModal.vue', import.meta.url), 'utf8')
const agentEditorSource = readFileSync(new URL('../views/agent/AgentEditorModal.vue', import.meta.url), 'utf8')
const modelSettingsSource = readFileSync(new URL('../views/settings/ModelSettings.vue', import.meta.url), 'utf8')

function model(overrides: Partial<ModelConfig> & Pick<ModelConfig, 'id' | 'type'>): ModelConfig {
  return {
    name: overrides.name ?? overrides.id!,
    source: 'remote',
    parameters: {},
    ...overrides,
  }
}

const fixtures: ModelConfig[] = [
  model({ id: 'chat-vision', name: 'MiniMax-M3', display_name: 'MiniMax M3', type: 'KnowledgeQA', parameters: { supports_vision: true } }),
  model({ id: 'chat-text', type: 'KnowledgeQA' }),
  model({ id: 'embed-vision', type: 'Embedding', parameters: { supports_vision: true } }),
  model({ id: 'embed-1', type: 'Embedding' }),
]

test('vision selector includes vision-capable chat models only (ADR 0004)', () => {
  const ids = filterModelsByType(fixtures, 'vision').map((m) => m.id)
  assert.deepEqual(ids, ['chat-vision'])
})

test('vision selector keeps the standard model-name presentation without type suffixes', () => {
  assert.match(selectorSource, /:label="modelDisplayName\(model\)"/)
  assert.doesNotMatch(selectorSource, /modelOptionTypeLabel|modelOptionLabel|vlmOptionType/)
})

test('VLM selection, knowledge-base and agent saves, and deletion preserve the selected model ID', () => {
  assert.match(selectorSource, /:key="model\.id"[\s\S]*:value="model\.id"/)
  assert.match(selectorSource, /emit\('update:selectedModelId', value \|\| ''\)/)
  assert.match(kbEditorSource, /handleMultimodalVLLMChange[\s\S]*vllmModelId = modelId/)
  assert.match(kbEditorSource, /vlm_config = \{[\s\S]*model_id:[\s\S]*vllmModelId/)
  assert.match(agentEditorSource, /model-type="vision"[\s\S]*formData\.config\.vlm_model_id[\s\S]*formData\.config\.vlm_model_id = val/)
  assert.match(agentEditorSource, /const payload = \{ \.\.\.formData\.value, config: serializeAgentPrompts/)
  assert.match(agentEditorSource, /updateAgent\(formData\.value\.id, payload\)/)
  assert.match(modelSettingsSource, /@confirm="deleteModel\(model\._modelType, model\.id\)"/)
  assert.match(modelSettingsSource, /allModels\.value\.find\(m => m\.id === modelId\)/)
  assert.match(modelSettingsSource, /await deleteModelAPI\(modelId\)/)
})

test('vision selector excludes non-vision chat and other types even with supports_vision', () => {
  const ids = filterModelsByType(fixtures, 'vision')
  assert.ok(!ids.some((m) => m.id === 'chat-text'))
  assert.ok(!ids.some((m) => m.id === 'embed-vision'))
})

test('non-vision selectors still filter strictly by type', () => {
  const ids = filterModelsByType(fixtures, 'KnowledgeQA').map((m) => m.id)
  assert.deepEqual(ids, ['chat-vision', 'chat-text'])
})
