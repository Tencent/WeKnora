import assert from 'node:assert/strict'
import test from 'node:test'

import {
  MODEL_IN_USE_ERROR_CODE,
  ModelInUseError,
  modelInUseErrorFromRequest,
  modelUsageAgentSection,
  modelUsageBindingI18nKey,
  modelUsageKnowledgeBaseSection,
  modelUsageListTruncated,
  modelUsageResourceCount,
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
  knowledge_base_total: 2,
  agent_total: 1,
}

test('parses grouped model usage details without dropping multiple bindings', () => {
  assert.deepEqual(parseModelUsageDetails(details), details)
})

test('fills missing totals from the listed collections', () => {
  const { knowledge_base_total, agent_total, ...withoutTotals } = details
  assert.deepEqual(parseModelUsageDetails(withoutTotals), details)
  assert.equal(knowledge_base_total, 2)
  assert.equal(agent_total, 1)
})

test('keeps untruncated totals when the listed collections are capped', () => {
  const parsed = parseModelUsageDetails({
    ...details,
    knowledge_base_total: 80,
    agent_total: 12,
  })
  assert.equal(parsed?.knowledge_base_total, 80)
  assert.equal(parsed?.agent_total, 12)
  assert.equal(modelUsageResourceCount(parsed!.knowledge_bases, parsed!.knowledge_base_total), 80)
  assert.equal(modelUsageListTruncated(parsed!.knowledge_bases, parsed!.knowledge_base_total), true)
  assert.equal(modelUsageListTruncated(parsed!.agents, parsed!.agent_total), true)
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
  assert.deepEqual(
    modelUsageResourceRoute('agent', 'agent-1', ['vlm_model']),
    { path: '/platform/agents', query: { edit: 'agent-1', section: 'multimodal' } },
  )
  assert.deepEqual(
    modelUsageResourceRoute('agent', 'agent-1', ['follow_up_model']),
    { path: '/platform/agents', query: { edit: 'agent-1', section: 'suggestions' } },
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

test('maps agent bindings to the configuration section that owns them', () => {
  assert.equal(modelUsageAgentSection(['chat_model']), 'model')
  assert.equal(modelUsageAgentSection(['rerank_model']), 'model')
  assert.equal(modelUsageAgentSection(['query_understand_model']), 'model')
  assert.equal(modelUsageAgentSection(['vlm_model']), 'multimodal')
  assert.equal(modelUsageAgentSection(['asr_model']), 'multimodal')
  assert.equal(modelUsageAgentSection(['follow_up_model']), 'suggestions')
  assert.equal(modelUsageAgentSection(['future_binding']), 'model')
  assert.equal(modelUsageAgentSection(['chat_model', 'vlm_model']), 'model')
  assert.equal(modelUsageAgentSection(['vlm_model', 'follow_up_model']), 'multimodal')
})
