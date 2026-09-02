import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MODEL_IN_USE_ERROR_CODE,
  ModelInUseError,
  modelInUseErrorFromRequest,
  modelUsageBindingI18nKey,
  modelUsageKnowledgeBaseSection,
  modelUsageResourceRoute,
  parseModelUsageDetails,
} from './modelUsage'

const details = {
  knowledge_bases: [
    { id: 'kb-1', name: 'Product docs', bindings: ['vlm_model'] },
    { id: 'kb-2', name: 'Engineering', bindings: ['vlm_model'] },
  ],
  agents: [
    { id: 'agent-1', name: 'Support', bindings: ['chat_model', 'follow_up_model'] },
  ],
  long_term_memory: { bindings: ['extract_model'] },
}

test('parses grouped model usage details without dropping multiple bindings', () => {
  assert.deepEqual(parseModelUsageDetails(details), details)
})

test('recognizes only the dedicated model-in-use code with a valid payload', () => {
  const conflict = modelInUseErrorFromRequest({
    status: 400,
    success: false,
    error: {
      code: MODEL_IN_USE_ERROR_CODE,
      message: 'backwards-compatible server message',
      details,
    },
  })

  assert.ok(conflict instanceof ModelInUseError)
  assert.deepEqual(conflict.details, details)
  assert.equal(modelInUseErrorFromRequest({ error: { code: 1000, details } }), null)
})

test('rejects malformed details instead of guessing from the server message', () => {
  assert.equal(parseModelUsageDetails({ knowledge_bases: [], agents: [] }), null)
  assert.equal(parseModelUsageDetails({
    knowledge_bases: [],
    agents: [],
    long_term_memory: { bindings: [] },
  }), null)
  assert.equal(parseModelUsageDetails({
    knowledge_bases: [{ id: 'kb-1', name: 'Docs', bindings: [] }],
    agents: [],
    long_term_memory: { bindings: [] },
  }), null)
  assert.equal(parseModelUsageDetails({
    knowledge_bases: [{ id: 'kb-1', name: 'Docs', bindings: 'vlm_model' }],
    agents: [],
    long_term_memory: { bindings: [] },
  }), null)
  assert.equal(modelInUseErrorFromRequest({
    error: {
      code: MODEL_IN_USE_ERROR_CODE,
      message: 'model is used by 1 knowledge base(s)',
      details: null,
    },
  }), null)
})

test('keeps navigation and localization stable for all resource kinds', () => {
  assert.deepEqual(
    modelUsageResourceRoute('knowledge_base', 'kb/with slash'),
    { path: '/platform/knowledge-bases/kb%2Fwith%20slash' },
  )
  assert.deepEqual(
    modelUsageResourceRoute('agent', 'agent-1'),
    { path: '/platform/agents', query: { edit: 'agent-1', section: 'model' } },
  )
  assert.equal(modelUsageBindingI18nKey('vlm_model'), 'modelSettings.usage.bindings.vlm_model')
  assert.equal(modelUsageBindingI18nKey('future_binding'), 'modelSettings.usage.bindings.unknown')
})

test('maps knowledge-base bindings to the configuration section that owns them', () => {
  assert.equal(modelUsageKnowledgeBaseSection(['embedding_model']), 'models')
  assert.equal(modelUsageKnowledgeBaseSection(['summary_model']), 'models')
  assert.equal(modelUsageKnowledgeBaseSection(['wiki_synthesis_model']), 'models')
  assert.equal(modelUsageKnowledgeBaseSection(['image_processing_model']), 'multimodal')
  assert.equal(modelUsageKnowledgeBaseSection(['vlm_model']), 'multimodal')
  assert.equal(modelUsageKnowledgeBaseSection(['asr_model']), 'asr')
  assert.equal(modelUsageKnowledgeBaseSection(['future_binding']), 'models')
  assert.equal(modelUsageKnowledgeBaseSection(['summary_model', 'vlm_model']), 'models')
})
