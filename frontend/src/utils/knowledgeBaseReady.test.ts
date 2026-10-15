import assert from 'node:assert/strict'
import test from 'node:test'
import { isKnowledgeBaseReady } from './knowledgeBaseReady'

test('setup status and upload guard use the same model requirements', () => {
  assert.equal(isKnowledgeBaseReady(null), false)
  assert.equal(isKnowledgeBaseReady({ embedding_model_id: 'embedding' }), false)
  assert.equal(isKnowledgeBaseReady({ summary_model_id: 'chat' }), false)
  assert.equal(isKnowledgeBaseReady({ summary_model_id: 'chat', embedding_model_id: 'embedding' }), true)
  assert.equal(isKnowledgeBaseReady({ summary_model_id: 'chat', indexing_strategy: { vector_enabled: false, keyword_enabled: false } }), true)
  assert.equal(isKnowledgeBaseReady({ summary_model_id: 'chat', indexing_strategy: { keyword_enabled: true } }), false)
})
