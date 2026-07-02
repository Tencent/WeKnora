import assert from 'node:assert/strict'
import test from 'node:test'
import {
  agentHasConfiguredChatModel,
  agentRequiresRerankModel,
  canLocallyConfigureAgent,
  getAgentNotReadyReasonKeys,
  resolveAgentNotReadySection,
  resolveAgentNotReadyHighlight,
} from './agent-readiness.ts'

test('does not treat an unrelated built-in chat model as agent configuration', () => {
  assert.equal(agentHasConfiguredChatModel(
    {},
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), false)
})

test('accepts the chat model explicitly configured on the agent', () => {
  assert.equal(agentHasConfiguredChatModel(
    { model_id: 'builtin-chat' },
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), true)
})

test('rejects a configured chat model that no longer exists', () => {
  assert.equal(agentHasConfiguredChatModel(
    { model_id: 'deleted-chat' },
    [{ id: 'builtin-chat', type: 'KnowledgeQA' }],
  ), false)
})

test('requires rerank when knowledge_search has a knowledge-base scope', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['knowledge_search'],
  }), true)
})

test('does not require rerank when knowledge bases are disabled', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'none',
    allowed_tools: ['knowledge_search'],
  }), false)
})

test('does not require rerank for wiki-only tools', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: ['wiki_search', 'wiki_read_page'],
  }), false)
})

test('matches backend default-tool behavior', () => {
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'all',
    allowed_tools: [],
  }), true)
  assert.equal(agentRequiresRerankModel({
    kb_selection_mode: 'none',
    allowed_tools: [],
  }), false)
})

test('resolveAgentNotReadySection opens model config for model issues', () => {
  assert.equal(resolveAgentNotReadySection(['summary_model']), 'model')
  assert.equal(resolveAgentNotReadySection(['rerank_model']), 'model')
  assert.equal(resolveAgentNotReadySection(['summary_model', 'allowed_tools']), 'model')
})

test('resolveAgentNotReadySection opens tools config when only tools are missing', () => {
  assert.equal(resolveAgentNotReadySection(['allowed_tools']), 'tools')
})

test('resolveAgentNotReadyHighlight returns the first missing item', () => {
  assert.equal(resolveAgentNotReadyHighlight(['summary_model', 'rerank_model']), 'summary_model')
  assert.equal(resolveAgentNotReadyHighlight(['allowed_tools']), 'allowed_tools')
  assert.equal(resolveAgentNotReadyHighlight([]), undefined)
})

test('getAgentNotReadyReasonKeys flags missing chat model', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    {},
    [{ id: 'chat-1', type: 'KnowledgeQA' }],
    { isAgentMode: false, isSharedAgent: false },
  ), ['summary_model'])
})

test('getAgentNotReadyReasonKeys requires rerank only in agent mode with KB search', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    { kb_selection_mode: 'all', allowed_tools: ['knowledge_search'] },
    [{ id: 'chat-1', type: 'KnowledgeQA' }, { id: 'rerank-1', type: 'Rerank' }],
    { isAgentMode: true, isSharedAgent: false },
  ), ['summary_model', 'rerank_model'])
})

test('getAgentNotReadyReasonKeys does not treat current shared context as shared without sourceTenantId', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    { model_id: 'deleted-chat' },
    [{ id: 'chat-1', type: 'KnowledgeQA' }],
    { isAgentMode: false, isSharedAgent: false },
  ), ['summary_model'])
})

test('getAgentNotReadyReasonKeys treats empty allowed_tools as ready via backend defaults', () => {
  assert.deepEqual(getAgentNotReadyReasonKeys(
    {
      model_id: 'chat-1',
      rerank_model_id: 'rerank-1',
      kb_selection_mode: 'all',
      allowed_tools: [],
    },
    [{ id: 'chat-1', type: 'KnowledgeQA' }, { id: 'rerank-1', type: 'Rerank' }],
    { isAgentMode: true, isSharedAgent: false },
  ), [])
})

test('canLocallyConfigureAgent is false for shared agents', () => {
  assert.equal(canLocallyConfigureAgent('42'), false)
  assert.equal(canLocallyConfigureAgent(undefined), true)
  assert.equal(canLocallyConfigureAgent(''), true)
})
