import assert from 'node:assert/strict'
import test from 'node:test'

import { normalizeAPIKeyKnowledgeBaseIDs } from './apiKeyScope.ts'

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
