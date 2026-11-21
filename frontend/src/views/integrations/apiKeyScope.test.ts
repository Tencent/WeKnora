import assert from 'node:assert/strict'
import test from 'node:test'

import {
  buildKnowledgeBasePermissionsPayload,
  defaultKnowledgeBaseGrants,
  knowledgeBaseGrantsForID,
  normalizeAPIKeyKnowledgeBaseIDs,
  summarizeKnowledgeBasePermissions,
} from './apiKeyScope.ts'

/**
 * 验证完全授权 Key 的 null/undefined 范围会转换为空数组，避免列表渲染读取 length 时白屏。
 * 传入服务端可能返回的空值，期望返回表示“全部知识库”的空数组。
 */
test('normalizes missing API key knowledge base scope to an empty array', () => {
  assert.deepEqual(normalizeAPIKeyKnowledgeBaseIDs(null), [])
  assert.deepEqual(normalizeAPIKeyKnowledgeBaseIDs(undefined), [])
})

/**
 * 验证 scoped Key 的知识库 ID 会被完整复制，且返回值不是原数组，避免编辑表单污染列表数据。
 * 传入两个知识库 ID，期望按原顺序返回一份新数组。
 */
test('copies configured API key knowledge base scope', () => {
  const ids = ['kb-1', 'kb-2']
  const normalized = normalizeAPIKeyKnowledgeBaseIDs(ids)

  assert.deepEqual(normalized, ids)
  assert.notEqual(normalized, ids)
  assert.deepEqual(normalizeAPIKeyKnowledgeBaseIDs([]), [])
})

test('defaults per-KB grants from global retrieve/ingest/manage_kbs', () => {
  assert.deepEqual(defaultKnowledgeBaseGrants(['retrieve', 'chat']), ['retrieve'])
  assert.deepEqual(
    defaultKnowledgeBaseGrants(['chat', 'ingest', 'manage_kbs']),
    ['retrieve', 'ingest', 'manage_kbs'],
  )
})

test('inherits global grants when a KB has no overlay entry', () => {
  const grants = knowledgeBaseGrantsForID(
    'kb-2',
    ['kb-1', 'kb-2'],
    { 'kb-1': ['retrieve'] },
    ['retrieve', 'ingest'],
  )
  assert.deepEqual(grants, ['retrieve', 'ingest'])
})

test('omits payload when every KB still matches the global default', () => {
  const payload = buildKnowledgeBasePermissionsPayload(
    ['kb-1', 'kb-2'],
    { 'kb-1': ['retrieve', 'ingest'] },
    ['retrieve', 'ingest'],
  )
  assert.deepEqual(payload, {})
})

test('sends an overlay when one KB has ingest removed', () => {
  const payload = buildKnowledgeBasePermissionsPayload(
    ['kb-1', 'kb-2'],
    { 'kb-1': ['retrieve'] },
    ['retrieve', 'ingest'],
  )
  assert.deepEqual(payload['kb-1'], ['retrieve'])
  assert.deepEqual(payload['kb-2'], ['retrieve', 'ingest'])
})

test('summarizes editable and manageable counts from the overlay', () => {
  const summary = summarizeKnowledgeBasePermissions(
    ['kb-1', 'kb-2', 'kb-3'],
    { 'kb-1': ['retrieve'], 'kb-3': ['retrieve', 'ingest', 'manage_kbs'] },
    ['retrieve', 'ingest', 'manage_kbs'],
  )
  assert.deepEqual(summary, { total: 3, editable: 2, manageable: 2 })
})
