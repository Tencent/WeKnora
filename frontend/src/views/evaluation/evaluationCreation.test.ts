import assert from 'node:assert/strict'
import test from 'node:test'
import { buildEvaluationCreation } from './evaluationCreation'
const draft = { datasetId: 'd', versionId: 'v', knowledgeBaseId: 'kb', chatId: 'chat', rerankId: '', topK: '', seed: '' }
test('creation binds a specific dataset version and leaves optional seed and generation defaults absent', () => {
  assert.deepEqual(buildEvaluationCreation(draft), { dataset_id: 'd', dataset_version_id: 'v', knowledge_base_id: 'kb', chat_id: 'chat' })
  assert.equal(buildEvaluationCreation({ ...draft, seed: '0' }).seed, 0)
  assert.equal(buildEvaluationCreation({ ...draft, seed: 0 }).seed, 0)
  assert.deepEqual(buildEvaluationCreation({ ...draft, topK: 5 }).configuration, { retrieval: { embedding_top_k: 5 } })
})
test('creation rejects incomplete selections and malformed optional parameters', () => {
  assert.throws(() => buildEvaluationCreation({ ...draft, versionId: '' }), /required/)
  for (const seed of ['1.5', '9007199254740992', '1e3']) assert.throws(() => buildEvaluationCreation({ ...draft, seed }), /seed/)
  for (const topK of ['0', '-1', '101', '1.2']) assert.throws(() => buildEvaluationCreation({ ...draft, topK }), /topK/)
  assert.deepEqual(buildEvaluationCreation({ ...draft, topK: '5', rerankId: 'rerank' }).configuration, { retrieval: { embedding_top_k: 5 } })
})
