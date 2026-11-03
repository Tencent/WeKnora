import assert from 'node:assert/strict'
import test from 'node:test'
import { effectiveModelDefault, effectiveUtilityDefault } from './defaultSettingsState'

const models = [
  { id: 'chat-a', type: 'KnowledgeQA', status: 'active' },
  { id: 'chat-yaml', type: 'KnowledgeQA', status: 'active', is_default: true },
  { id: 'chat-down', type: 'KnowledgeQA', status: 'downloading' },
  { id: 'rerank-a', type: 'Rerank', status: 'active' },
]

test('a usable workspace choice is the effective default', () => {
  const got = effectiveModelDefault(models, 'KnowledgeQA', 'chat-a')
  assert.equal(got.model?.id, 'chat-a')
  assert.equal(got.source, 'workspace')
  assert.equal(got.stale, false)
})

test('an empty slot shows the deployment default', () => {
  const got = effectiveModelDefault(models, 'KnowledgeQA', '')
  assert.equal(got.model?.id, 'chat-yaml')
  assert.equal(got.source, 'system')
  assert.equal(got.stale, false)
})

test('an unusable choice is stale and falls back', () => {
  for (const id of ['gone', 'chat-down', 'rerank-a']) {
    const got = effectiveModelDefault(models, 'KnowledgeQA', id)
    assert.equal(got.model?.id, 'chat-yaml', id)
    assert.equal(got.stale, true, id)
  }
})

test('without any default the first usable model is used, else none', () => {
  const plain = models.map((m) => ({ ...m, is_default: false }))
  assert.equal(effectiveModelDefault(plain, 'KnowledgeQA', '').source, 'auto')
  assert.equal(effectiveModelDefault(plain, 'KnowledgeQA', '').model?.id, 'chat-a')
  assert.equal(effectiveModelDefault(plain, 'Embedding', '').source, 'none')
})

test('the utility slot follows the chat default when unset or stale', () => {
  assert.equal(effectiveUtilityDefault(models, 'chat-a', 'chat-yaml').source, 'workspace')
  const follow = effectiveUtilityDefault(models, '', 'chat-a')
  assert.equal(follow.model?.id, 'chat-a')
  assert.equal(follow.source, 'chat')
  const stale = effectiveUtilityDefault(models, 'gone', '')
  assert.equal(stale.model?.id, 'chat-yaml')
  assert.equal(stale.stale, true)
  assert.equal(effectiveUtilityDefault([], '', '').source, 'none')
})

test('vision-capable chat choices are usable without inheriting chat default flags', () => {
  const models = [
    { id: 'chat', type: 'KnowledgeQA', is_default: true, parameters: { supports_vision: true } },
    { id: 'native', type: 'VLLM', is_default: true },
  ]
  const explicit = effectiveModelDefault(models, 'VLLM', 'chat')
  assert.equal(explicit.source, 'workspace')
  assert.equal(explicit.stale, false)
  assert.equal(explicit.model?.id, 'chat')
  const fallback = effectiveModelDefault(models, 'VLLM', 'deleted')
  assert.equal(fallback.model?.id, 'native')
  assert.equal(fallback.stale, true)
})
